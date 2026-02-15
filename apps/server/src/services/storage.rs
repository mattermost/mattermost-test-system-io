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
    pub async fn new(config: &StorageSettings) -> AppResult<Self> {
        let credentials =
            Credentials::new(&config.access_key, &config.secret_key, None, None, "tsio");

        let region = Region::new(config.region.clone());

        let mut s3_config_builder = aws_sdk_s3::Config::builder()
            .behavior_version(BehaviorVersion::latest())
            .region(region)
            .credentials_provider(credentials)
            .force_path_style(true); // Required for MinIO

        // Use custom endpoint for MinIO in development
        if let Some(ref endpoint) = config.endpoint {
            s3_config_builder = s3_config_builder.endpoint_url(endpoint);
        }

        let s3_config = s3_config_builder.build();
        let client = Client::from_conf(s3_config);

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

    /// Build an S3 key prefix for a job's files.
    ///
    /// # Arguments
    /// * `report_id` - The report UUID
    /// * `job_id` - The job UUID
    ///
    /// # Returns
    /// S3 key prefix in format: reports/{report_id}/jobs/{job_id}
    pub fn job_key_prefix(report_id: &str, job_id: &str) -> String {
        format!("reports/{}/jobs/{}", report_id, job_id)
    }

    /// Build an S3 key for a job file.
    ///
    /// # Arguments
    /// * `report_id` - The report UUID
    /// * `job_id` - The job UUID
    /// * `filename` - The filename within the job
    ///
    /// # Returns
    /// S3 key in format: reports/{report_id}/jobs/{job_id}/{filename}
    pub fn job_key(report_id: &str, job_id: &str, filename: &str) -> String {
        format!("reports/{}/jobs/{}/{}", report_id, job_id, filename)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_job_key_prefix() {
        let prefix = Storage::job_key_prefix("report-123", "job-456");
        assert_eq!(prefix, "reports/report-123/jobs/job-456");
    }

    #[test]
    fn test_job_key() {
        let key = Storage::job_key("report-123", "job-456", "index.html");
        assert_eq!(key, "reports/report-123/jobs/job-456/index.html");
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
