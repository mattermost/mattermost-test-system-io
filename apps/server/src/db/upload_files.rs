//! Database operations for tracking individual file uploads.
//!
//! Files are registered during initialization and marked as uploaded during transfer.

use rusqlite::Connection;
use uuid::Uuid;

use crate::error::{AppError, AppResult};

/// Register files for a report during initialization.
/// Returns the IDs of the created file records.
pub fn register_files(
    conn: &Connection,
    report_id: &str,
    filenames: &[String],
) -> AppResult<Vec<String>> {
    let mut ids = Vec::with_capacity(filenames.len());

    for filename in filenames {
        let id = Uuid::new_v4().to_string();

        conn.execute(
            "INSERT INTO upload_files (id, report_id, filename) VALUES (?1, ?2, ?3)",
            [&id, report_id, filename],
        )
        .map_err(|e| AppError::Database(format!("Failed to register file {}: {}", filename, e)))?;

        ids.push(id);
    }

    Ok(ids)
}

/// Mark a file as uploaded with its size.
/// Returns true if the file was found and updated.
pub fn mark_uploaded(
    conn: &Connection,
    report_id: &str,
    filename: &str,
    file_size: i64,
) -> AppResult<bool> {
    let rows = conn
        .execute(
            "UPDATE upload_files
             SET file_size = ?1, uploaded_at = datetime('now')
             WHERE report_id = ?2 AND filename = ?3 AND uploaded_at IS NULL",
            rusqlite::params![file_size, report_id, filename],
        )
        .map_err(|e| AppError::Database(format!("Failed to mark file uploaded: {}", e)))?;

    Ok(rows > 0)
}

/// Delete file records for a report.
/// Called during cleanup or when a report is deleted.
pub fn delete_for_report(conn: &Connection, report_id: &str) -> AppResult<usize> {
    let rows = conn
        .execute("DELETE FROM upload_files WHERE report_id = ?1", [report_id])
        .map_err(|e| AppError::Database(format!("Failed to delete file records: {}", e)))?;

    Ok(rows)
}

/// File upload status from batch query.
#[derive(Debug, Clone)]
pub struct FileStatus {
    pub is_uploaded: bool,
}

/// Get status of multiple files in a single query (batch operation).
/// Returns a map of filename -> FileStatus.
/// Files not in the result are not registered.
pub fn get_files_status(
    conn: &Connection,
    report_id: &str,
    filenames: &[String],
) -> AppResult<std::collections::HashMap<String, FileStatus>> {
    use std::collections::HashMap;

    if filenames.is_empty() {
        return Ok(HashMap::new());
    }

    // Build IN clause with placeholders
    let placeholders: Vec<String> = (0..filenames.len()).map(|i| format!("?{}", i + 2)).collect();
    let query = format!(
        "SELECT filename, uploaded_at IS NOT NULL as is_uploaded
         FROM upload_files
         WHERE report_id = ?1 AND filename IN ({})",
        placeholders.join(", ")
    );

    let mut stmt = conn
        .prepare(&query)
        .map_err(|e| AppError::Database(format!("Failed to prepare batch query: {}", e)))?;

    // Build params: report_id + all filenames
    let mut params: Vec<&dyn rusqlite::ToSql> = Vec::with_capacity(filenames.len() + 1);
    params.push(&report_id);
    for filename in filenames {
        params.push(filename);
    }

    let rows = stmt
        .query_map(rusqlite::params_from_iter(params), |row| {
            Ok((row.get::<_, String>(0)?, row.get::<_, bool>(1)?))
        })
        .map_err(|e| AppError::Database(format!("Failed to query file statuses: {}", e)))?;

    let mut result = HashMap::new();
    for row in rows {
        let (filename, is_uploaded) =
            row.map_err(|e| AppError::Database(format!("Failed to read file status: {}", e)))?;
        result.insert(filename, FileStatus { is_uploaded });
    }

    Ok(result)
}

/// Get upload progress stats in a single query (batch operation).
/// Returns (total_files, uploaded_count, all_uploaded).
pub fn get_upload_progress(conn: &Connection, report_id: &str) -> AppResult<(i64, i64, bool)> {
    let result: (i64, i64) = conn
        .query_row(
            "SELECT
                COUNT(*) as total,
                COUNT(CASE WHEN uploaded_at IS NOT NULL THEN 1 END) as uploaded
             FROM upload_files
             WHERE report_id = ?1",
            [report_id],
            |row| Ok((row.get(0)?, row.get(1)?)),
        )
        .map_err(|e| AppError::Database(format!("Failed to get upload progress: {}", e)))?;

    let (total, uploaded) = result;
    Ok((total, uploaded, total == uploaded && total > 0))
}

/// Get reports with incomplete uploads older than the specified hours.
/// Used by cleanup task to remove stale uploads.
pub fn get_incomplete_reports_older_than(conn: &Connection, hours: u64) -> AppResult<Vec<String>> {
    let mut stmt = conn
        .prepare(
            "SELECT DISTINCT report_id FROM upload_files
             WHERE uploaded_at IS NULL
             AND created_at < datetime('now', ?1)",
        )
        .map_err(|e| AppError::Database(format!("Failed to prepare query: {}", e)))?;

    let modifier = format!("-{} hours", hours);
    let report_ids = stmt
        .query_map([&modifier], |row| row.get::<_, String>(0))
        .map_err(|e| AppError::Database(format!("Failed to query incomplete reports: {}", e)))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| AppError::Database(format!("Failed to read incomplete reports: {}", e)))?;

    Ok(report_ids)
}
