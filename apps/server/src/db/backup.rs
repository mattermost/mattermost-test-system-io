//! Database and files backup service.
//!
//! Creates tar.gz backups containing database and uploaded files on minor version bumps.

use std::fs::File;
use std::path::{Path, PathBuf};

use chrono::Utc;
use flate2::write::GzEncoder;
use flate2::Compression;
use tar::Builder;
use tracing::{info, warn};

use crate::error::{AppError, AppResult};

/// Creates a gzipped tar backup of the database and files directory.
///
/// Backup filename format: `v{version}_{YYYYMMDD_HHMMSS}.tar.gz`
/// Contents:
/// - Database file (*.db) and WAL files (*.db-wal, *.db-shm)
/// - Files directory (data/files/*)
///
/// Returns the path to the backup file, or None if backup was skipped.
pub async fn create_backup(
    database_url: &str,
    data_dir: &Path,
    backup_dir: &Path,
    version: &str,
) -> AppResult<Option<PathBuf>> {
    // Only backup local file databases
    if !database_url.starts_with("file:") {
        info!("Skipping backup for non-file database");
        return Ok(None);
    }

    let db_path = database_url.strip_prefix("file:").unwrap_or(database_url);
    let db_file = Path::new(db_path);

    // Skip if database doesn't exist yet (first run)
    if !db_file.exists() {
        info!("Database file does not exist yet, skipping backup");
        return Ok(None);
    }

    // Create backup directory if it doesn't exist
    tokio::fs::create_dir_all(backup_dir)
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to create backup directory: {}", e)))?;

    // Generate backup filename with version and timestamp
    let timestamp = Utc::now().format("%Y%m%d_%H%M%S");
    let backup_filename = format!("v{}_{}.tar.gz", version, timestamp);
    let backup_path = backup_dir.join(&backup_filename);

    // Clone paths for blocking task
    let db_file = db_file.to_path_buf();
    let data_dir = data_dir.to_path_buf();
    let backup_path_clone = backup_path.clone();

    // Create tar.gz in blocking task
    tokio::task::spawn_blocking(move || create_tar_gz(&db_file, &data_dir, &backup_path_clone))
        .await
        .map_err(|e| AppError::FileSystem(format!("Backup task failed: {}", e)))??;

    info!("Created backup: {}", backup_path.display());

    Ok(Some(backup_path))
}

/// Create a tar.gz archive containing database and files.
fn create_tar_gz(db_file: &Path, data_dir: &Path, backup_path: &Path) -> AppResult<()> {
    let file = File::create(backup_path)
        .map_err(|e| AppError::FileSystem(format!("Failed to create backup file: {}", e)))?;

    let encoder = GzEncoder::new(file, Compression::default());
    let mut archive = Builder::new(encoder);

    // Add database file
    if db_file.exists() {
        let db_name = db_file
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("database.db");
        archive
            .append_path_with_name(db_file, db_name)
            .map_err(|e| {
                AppError::FileSystem(format!("Failed to add database to backup: {}", e))
            })?;

        // Add WAL files if they exist
        let db_wal = db_file.with_extension("db-wal");
        if db_wal.exists() {
            let wal_name = format!("{}-wal", db_name);
            archive
                .append_path_with_name(&db_wal, &wal_name)
                .map_err(|e| {
                    AppError::FileSystem(format!("Failed to add WAL file to backup: {}", e))
                })?;
        }

        let db_shm = db_file.with_extension("db-shm");
        if db_shm.exists() {
            let shm_name = format!("{}-shm", db_name);
            archive
                .append_path_with_name(&db_shm, &shm_name)
                .map_err(|e| {
                    AppError::FileSystem(format!("Failed to add SHM file to backup: {}", e))
                })?;
        }
    }

    // Add files directory if it exists
    if data_dir.exists() && data_dir.is_dir() {
        archive
            .append_dir_all("files", data_dir)
            .map_err(|e| AppError::FileSystem(format!("Failed to add files to backup: {}", e)))?;
    }

    // Finish the archive
    archive
        .into_inner()
        .map_err(|e| AppError::FileSystem(format!("Failed to finish tar: {}", e)))?
        .finish()
        .map_err(|e| AppError::FileSystem(format!("Failed to finish gzip: {}", e)))?;

    Ok(())
}

/// Removes old backup files, keeping only the most recent N backups.
pub async fn cleanup_old_backups(backup_dir: &Path, keep_count: usize) -> AppResult<()> {
    let mut entries: Vec<_> = Vec::new();

    let mut dir = match tokio::fs::read_dir(backup_dir).await {
        Ok(dir) => dir,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            return Ok(());
        }
        Err(e) => {
            return Err(AppError::FileSystem(format!(
                "Failed to read backup directory: {}",
                e
            )));
        }
    };

    while let Some(entry) = dir
        .next_entry()
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to read backup entry: {}", e)))?
    {
        let path = entry.path();
        // Match .tar.gz backup files
        let is_backup = path
            .file_name()
            .and_then(|n| n.to_str())
            .map(|n| n.ends_with(".tar.gz"))
            .unwrap_or(false);

        if is_backup {
            if let Ok(metadata) = entry.metadata().await {
                if let Ok(modified) = metadata.modified() {
                    entries.push((path, modified));
                }
            }
        }
    }

    // Sort by modification time (newest first)
    entries.sort_by(|a, b| b.1.cmp(&a.1));

    // Remove old backups
    for (path, _) in entries.into_iter().skip(keep_count) {
        if let Err(e) = tokio::fs::remove_file(&path).await {
            warn!("Failed to remove old backup {}: {}", path.display(), e);
        } else {
            info!("Removed old backup: {}", path.display());
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[tokio::test]
    async fn test_skip_backup_for_nonexistent_db() {
        let backup_dir = tempdir().unwrap();
        let data_dir = tempdir().unwrap();
        let result = create_backup(
            "file:./nonexistent.db",
            data_dir.path(),
            backup_dir.path(),
            "0.1.0",
        )
        .await;
        assert!(result.is_ok());
        assert!(result.unwrap().is_none());
    }
}
