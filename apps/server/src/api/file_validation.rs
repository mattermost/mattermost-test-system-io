//! Shared file validation utilities for upload endpoints.
//!
//! Contains path safety checks, extension validation, content-type inference,
//! and per-file-type validators used by the report registration and file upload endpoints.

/// Allowed image extensions for screenshot uploads.
pub const ALLOWED_IMAGE_EXTENSIONS: &[&str] = &["png", "jpg", "jpeg", "gif", "webp"];

/// Allowed file extensions for JSON test result uploads.
pub const ALLOWED_JSON_EXTENSIONS: &[&str] = &["json"];

/// Maximum screenshot file size (10 MB).
pub const MAX_SCREENSHOT_SIZE: i64 = 10 * 1024 * 1024;

/// Maximum JSON file size (50 MB).
pub const MAX_JSON_FILE_SIZE: i64 = 50 * 1024 * 1024;

/// Check that a caller-supplied relative file path is safe against path traversal.
///
/// Normalises the path using `std::path::Path` component iteration and rejects any
/// path that, after normalisation, would escape the current directory.
pub fn is_safe_path(path: &str) -> bool {
    use std::path::{Component, Path};

    if path.is_empty() || path.contains('\0') {
        return false;
    }

    if path.starts_with('/') || path.starts_with('\\') {
        return false;
    }

    for component in Path::new(path).components() {
        match component {
            Component::ParentDir => return false,
            Component::Prefix(_) | Component::RootDir => return false,
            _ => {}
        }
    }

    true
}

/// Validate a screenshot file and return rejection reason if invalid.
pub fn validate_screenshot(path: &str, size: Option<i64>) -> Option<String> {
    if path.is_empty() {
        return Some("Empty file path".to_string());
    }

    if !is_safe_path(path) {
        return Some("Path traversal not allowed".to_string());
    }

    let extension = path.rsplit('.').next().unwrap_or("").to_lowercase();
    if !ALLOWED_IMAGE_EXTENSIONS.contains(&extension.as_str()) {
        return Some(format!(
            "File extension '{}' not allowed. Allowed: {:?}",
            extension, ALLOWED_IMAGE_EXTENSIONS
        ));
    }

    if let Some(size) = size {
        if size > MAX_SCREENSHOT_SIZE {
            return Some(format!(
                "File size {} exceeds maximum {} bytes",
                size, MAX_SCREENSHOT_SIZE
            ));
        }
        if size < 0 {
            return Some("Invalid file size".to_string());
        }
    }

    None
}

/// Validate a JSON file and return rejection reason if invalid.
pub fn validate_json_file(path: &str, size: Option<i64>) -> Option<String> {
    if path.is_empty() {
        return Some("Empty file path".to_string());
    }

    if !is_safe_path(path) {
        return Some("Path traversal not allowed".to_string());
    }

    let extension = path.rsplit('.').next().unwrap_or("").to_lowercase();
    if !ALLOWED_JSON_EXTENSIONS.contains(&extension.as_str()) {
        return Some(format!(
            "File extension '{}' not allowed. Allowed: {:?}",
            extension, ALLOWED_JSON_EXTENSIONS
        ));
    }

    if let Some(size) = size {
        if size > MAX_JSON_FILE_SIZE {
            return Some(format!(
                "File size {} exceeds maximum {} bytes",
                size, MAX_JSON_FILE_SIZE
            ));
        }
        if size < 0 {
            return Some("Invalid file size".to_string());
        }
    }

    None
}

/// Infer content type from file extension.
pub fn infer_content_type(path: &str) -> &'static str {
    let ext = path.rsplit('.').next().unwrap_or("").to_lowercase();
    match ext.as_str() {
        "html" | "htm" => "text/html",
        "css" => "text/css",
        "js" => "application/javascript",
        "json" => "application/json",
        "map" => "application/json",
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "svg" => "image/svg+xml",
        "webp" => "image/webp",
        "ico" => "image/x-icon",
        "woff" => "font/woff",
        "woff2" => "font/woff2",
        "ttf" => "font/ttf",
        "eot" => "application/vnd.ms-fontobject",
        "otf" => "font/otf",
        "txt" => "text/plain",
        "md" => "text/markdown",
        _ => "application/octet-stream",
    }
}

/// Extract test name from screenshot path.
/// Path format: "test-name/screenshot.png" -> "test-name"
/// Path format: "test-name/subdir/screenshot.png" -> "test-name" (full dir)
/// Path format: "screenshot.png" -> "" (root level)
pub fn extract_test_name(path: &str) -> String {
    if let Some(pos) = path.rfind('/') {
        path[..pos].to_string()
    } else {
        String::new()
    }
}
