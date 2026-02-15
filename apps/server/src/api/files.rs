//! File serving API handlers.
//!
//! Proxies file requests to S3 storage.

use actix_web::{HttpResponse, web};
use tracing::debug;

use crate::error::{AppError, AppResult};
use crate::services::Storage;

/// Serve a file from S3 storage.
///
/// Proxies requests to S3 and returns the file with appropriate content type.
pub async fn serve_file(
    storage: web::Data<Storage>,
    path: web::Path<String>,
) -> AppResult<HttpResponse> {
    let key = path.into_inner();

    // Validate the key format (should start with reports/)
    if !key.starts_with("reports/") {
        return Err(AppError::NotFound("Invalid file path".to_string()));
    }

    debug!("Serving file from S3: {}", key);

    // Get file from S3
    let (data, content_type) = storage.get(&key).await?;

    // Build response with appropriate content type
    let content_type = content_type.unwrap_or_else(|| {
        // Infer from extension
        let ext = key.rsplit('.').next().unwrap_or("");
        Storage::content_type_for_extension(ext).to_string()
    });

    Ok(HttpResponse::Ok().content_type(content_type).body(data))
}

/// Configure file routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    // Serve files from S3: /files/{s3_key:.*}
    cfg.service(web::resource("/files/{key:.*}").route(web::get().to(serve_file)));
}
