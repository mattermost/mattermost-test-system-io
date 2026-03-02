//! S3 storage service for file uploads.
//!
//! Handles all S3 operations including presigned URLs, delete, and listing.
//! Supports both AWS S3 and MinIO for development.

use aws_config::BehaviorVersion;
use aws_sdk_s3::Client;
use aws_sdk_s3::config::{Credentials, Region};
use tracing::info;

use crate::config::StorageSettings;
use crate::error::{AppError, AppResult};

/// S3 storage client wrapper.
#[derive(Clone)]
pub struct Storage {
    client: Client,
    bucket: String,
}

impl Storage {
    /// Create a new S3 storage client from configuration.
    ///
    /// When `access_key` and `secret_key` are provided, uses static credentials (for MinIO/dev).
    /// When they are absent, uses the default AWS credential chain (IAM role, env vars, etc.).
    pub async fn new(config: &StorageSettings) -> AppResult<Self> {
        let region = Region::new(config.region.clone());

        let client = if let (Some(access_key), Some(secret_key)) =
            (&config.access_key, &config.secret_key)
        {
            // Static credentials (MinIO / explicit keys)
            let credentials = Credentials::new(access_key, secret_key, None, None, "tsio");

            let mut s3_config_builder = aws_sdk_s3::Config::builder()
                .behavior_version(BehaviorVersion::latest())
                .region(region)
                .credentials_provider(credentials)
                .force_path_style(true); // Required for MinIO

            if let Some(ref endpoint) = config.endpoint {
                s3_config_builder = s3_config_builder.endpoint_url(endpoint);
            }

            Client::from_conf(s3_config_builder.build())
        } else {
            // IAM role / default AWS credential chain
            let aws_config = aws_config::defaults(BehaviorVersion::latest())
                .region(aws_config::Region::new(config.region.clone()))
                .load()
                .await;

            Client::new(&aws_config)
        };

        let storage = Self {
            client,
            bucket: config.bucket.clone(),
        };

        // Verify bucket exists or create it
        storage.ensure_bucket_exists().await?;

        info!("S3 storage initialized: bucket={}", config.bucket);

        Ok(storage)
    }

    /// Ensure the bucket exists, creating it if necessary.
    async fn ensure_bucket_exists(&self) -> AppResult<()> {
        match self.client.head_bucket().bucket(&self.bucket).send().await {
            Ok(_) => {
                info!("S3 bucket '{}' exists", self.bucket);
                Ok(())
            }
            Err(e) => {
                // Check if it's a "not found" error
                let service_error = e.into_service_error();
                if service_error.is_not_found() {
                    info!("Creating S3 bucket '{}'", self.bucket);
                    self.client
                        .create_bucket()
                        .bucket(&self.bucket)
                        .send()
                        .await
                        .map_err(|e| {
                            AppError::Storage(format!("Failed to create bucket: {}", e))
                        })?;
                    info!("S3 bucket '{}' created", self.bucket);
                    Ok(())
                } else {
                    Err(AppError::Storage(format!(
                        "Failed to access bucket '{}': {}",
                        self.bucket, service_error
                    )))
                }
            }
        }
    }

    /// Get the content type for a file based on its extension.
    pub fn content_type_for_extension(ext: &str) -> &'static str {
        match ext.to_lowercase().as_str() {
            "html" | "htm" => "text/html",
            "css" => "text/css",
            "js" => "application/javascript",
            "json" => "application/json",
            "png" => "image/png",
            "jpg" | "jpeg" => "image/jpeg",
            "gif" => "image/gif",
            "svg" => "image/svg+xml",
            "ico" => "image/x-icon",
            "woff" => "font/woff",
            "woff2" => "font/woff2",
            "ttf" => "font/ttf",
            "eot" => "application/vnd.ms-fontobject",
            "zip" => "application/zip",
            "txt" => "text/plain",
            "xml" => "application/xml",
            _ => "application/octet-stream",
        }
    }

    /// Upload a file to S3.
    ///
    /// # Arguments
    /// * `key` - The S3 object key where the file will be uploaded
    /// * `data` - The file contents as bytes
    /// * `content_type` - Optional content type for the upload
    pub async fn put(&self, key: &str, data: Vec<u8>, content_type: Option<&str>) -> AppResult<()> {
        let body = aws_sdk_s3::primitives::ByteStream::from(data);
        let mut request = self
            .client
            .put_object()
            .bucket(&self.bucket)
            .key(key)
            .body(body);

        if let Some(ct) = content_type {
            request = request.content_type(ct);
        }

        request
            .send()
            .await
            .map_err(|e| AppError::Storage(format!("Failed to upload file to S3: {}", e)))?;

        Ok(())
    }

    /// Get a file from S3.
    ///
    /// # Arguments
    /// * `key` - The S3 object key to retrieve
    ///
    /// # Returns
    /// The file contents as bytes and content type
    pub async fn get(&self, key: &str) -> AppResult<(Vec<u8>, Option<String>)> {
        let response = self
            .client
            .get_object()
            .bucket(&self.bucket)
            .key(key)
            .send()
            .await
            .map_err(|e| {
                let service_error = e.into_service_error();
                if service_error.is_no_such_key() {
                    AppError::NotFound(format!("File not found: {}", key))
                } else {
                    AppError::Storage(format!("Failed to get file from S3: {}", service_error))
                }
            })?;

        let content_type = response.content_type().map(String::from);
        let data = response
            .body
            .collect()
            .await
            .map_err(|e| AppError::Storage(format!("Failed to read S3 response body: {}", e)))?
            .into_bytes()
            .to_vec();

        Ok((data, content_type))
    }

    /// Build an S3 key prefix for a report's files.
    ///
    /// # Arguments
    /// * `report_group_id` - The report group UUID
    /// * `report_id` - The individual report UUID
    ///
    /// # Returns
    /// S3 key prefix in format: reports/{report_group_id}/entries/{report_id}
    pub fn report_key_prefix(report_group_id: &str, report_id: &str) -> String {
        format!("reports/{}/entries/{}", report_group_id, report_id)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_report_key_prefix() {
        let prefix = Storage::report_key_prefix("group-123", "report-456");
        assert_eq!(prefix, "reports/group-123/entries/report-456");
    }

    #[test]
    fn test_content_type_for_extension() {
        assert_eq!(Storage::content_type_for_extension("html"), "text/html");
        assert_eq!(Storage::content_type_for_extension("HTML"), "text/html");
        assert_eq!(Storage::content_type_for_extension("css"), "text/css");
        assert_eq!(
            Storage::content_type_for_extension("js"),
            "application/javascript"
        );
        assert_eq!(
            Storage::content_type_for_extension("json"),
            "application/json"
        );
        assert_eq!(Storage::content_type_for_extension("png"), "image/png");
        assert_eq!(
            Storage::content_type_for_extension("unknown"),
            "application/octet-stream"
        );
    }
}
