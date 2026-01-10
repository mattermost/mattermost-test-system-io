//! Cleanup service for deleting expired report files.

use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use chrono::Utc;
use tokio::time::interval;
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::db::{queries, DbPool};

/// Configuration for the cleanup service.
#[derive(Clone)]
pub struct CleanupConfig {
    /// Directory where report files are stored
    pub data_dir: PathBuf,
    /// File retention period in hours
    pub retention_hours: u64,
    /// How often to run cleanup (in seconds)
    pub interval_secs: u64,
}

/// Start the cleanup background task.
///
/// This spawns a tokio task that periodically checks for and deletes
/// report files that have exceeded the retention period.
pub fn start_cleanup_task(pool: Arc<DbPool>, config: CleanupConfig) {
    tokio::spawn(async move {
        info!(
            "Starting cleanup service (retention: {} hours, interval: {} seconds)",
            config.retention_hours, config.interval_secs
        );

        let mut ticker = interval(Duration::from_secs(config.interval_secs));

        loop {
            ticker.tick().await;

            if let Err(e) = run_cleanup(&pool, &config).await {
                error!("Cleanup task error: {}", e);
            }
        }
    });
}

/// Run a single cleanup cycle.
async fn run_cleanup(
    pool: &DbPool,
    config: &CleanupConfig,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // Calculate cutoff time
    let cutoff = Utc::now() - chrono::Duration::hours(config.retention_hours as i64);

    // Get reports older than the retention period - scope the connection
    let reports = {
        let conn = pool.connection();
        get_reports_for_cleanup(&conn, cutoff)?
    };

    if reports.is_empty() {
        return Ok(());
    }

    info!("Found {} reports eligible for file cleanup", reports.len());

    let mut deleted_count = 0;
    let mut error_count = 0;

    for (report_id, file_path) in reports {
        let report_dir = config.data_dir.join(&file_path);

        if report_dir.exists() {
            match tokio::fs::remove_dir_all(&report_dir).await {
                Ok(_) => {
                    info!("Deleted files for report {}", report_id);

                    // Mark files as deleted in database
                    if let Ok(uuid) = Uuid::parse_str(&report_id) {
                        let conn = pool.connection();
                        if let Err(e) = queries::mark_report_files_deleted(&conn, uuid) {
                            warn!(
                                "Failed to mark files deleted for report {}: {}",
                                report_id, e
                            );
                        }
                    }

                    deleted_count += 1;
                }
                Err(e) => {
                    warn!("Failed to delete files for report {}: {}", report_id, e);
                    error_count += 1;
                }
            }
        }
    }

    if deleted_count > 0 || error_count > 0 {
        info!(
            "Cleanup complete: {} deleted, {} errors",
            deleted_count, error_count
        );
    }

    Ok(())
}

/// Get reports that are older than the cutoff time and have not had files deleted yet.
fn get_reports_for_cleanup(
    conn: &rusqlite::Connection,
    cutoff: chrono::DateTime<Utc>,
) -> Result<Vec<(String, String)>, rusqlite::Error> {
    let mut stmt = conn.prepare(
        "SELECT id, file_path FROM reports
         WHERE created_at < ?1
         AND deleted_at IS NULL
         AND files_deleted_at IS NULL
         ORDER BY created_at ASC
         LIMIT 100",
    )?;

    let cutoff_str = cutoff.format("%Y-%m-%dT%H:%M:%S%.fZ").to_string();

    let reports = stmt
        .query_map([cutoff_str], |row| {
            Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
        })?
        .collect::<Result<Vec<_>, _>>()?;

    Ok(reports)
}
