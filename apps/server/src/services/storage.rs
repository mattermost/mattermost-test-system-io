//! S3 storage service for file uploads.
//!
//! Handles all S3 operations including upload, download, delete, and listing.
//! Supports both AWS S3 and MinIO for development.
//! Uses multipart upload for files larger than 5MB.

use std::time::Duration;

use aws_config::BehaviorVersion;
use aws_sdk_s3::Client;
use aws_sdk_s3::config::{Credentials, Region};
use aws_sdk_s3::presigning::PresigningConfig;
use aws_sdk_s3::primitives::ByteStream;
use aws_sdk_s3::types::CompletedMultipartUpload;
use aws_sdk_s3::types::CompletedPart;
use tracing::{debug, error, info};

use crate::config::S3Config;
use crate::error::{AppError, AppResult};

/// Threshold for using multipart upload (5MB).
const MULTIPART_THRESHOLD: usize = 5 * 1024 * 1024;

/// Part size for multipart upload (5MB - minimum for S3).
const MULTIPART_PART_SIZE: usize = 5 * 1024 * 1024;

/// S3 storage client wrapper.
#[derive(Clone)]
pub struct Storage {
    client: Client,
    bucket: String,
}

impl Storage {
    /// Create a new S3 storage client from configuration.
    pub async fn new(config: &S3Config) -> AppResult<Self> {
        let credentials = Credentials::new(
            &config.access_key,
            &config.secret_key,
            None,
            None,
            "rust-report-server",
        );

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

    /// Upload a file to S3.
    ///
    /// Automatically uses multipart upload for files larger than 5MB.
    ///
    /// # Arguments
    /// * `key` - The S3 object key (path within the bucket)
    /// * `data` - The file contents as bytes
    /// * `content_type` - Optional MIME type for the file
    pub async fn upload(
        &self,
        key: &str,
        data: Vec<u8>,
        content_type: Option<&str>,
    ) -> AppResult<()> {
        // Use multipart upload for large files
        if data.len() > MULTIPART_THRESHOLD {
            return self.upload_multipart(key, data, content_type).await;
        }

        let body = ByteStream::from(data);

        let mut request = self
            .client
            .put_object()
            .bucket(&self.bucket)
            .key(key)
            .body(body);

        if let Some(ct) = content_type {
            request = request.content_type(ct);
        }

        request.send().await.map_err(|e| {
            error!("Failed to upload to S3: key={}, error={}", key, e);
            AppError::Storage(format!("Failed to upload file: {}", e))
        })?;

        Ok(())
    }

    /// Upload a large file using multipart upload.
    ///
    /// This method splits the file into parts and uploads them in parallel,
    /// then completes the multipart upload.
    ///
    /// # Arguments
    /// * `key` - The S3 object key (path within the bucket)
    /// * `data` - The file contents as bytes
    /// * `content_type` - Optional MIME type for the file
    async fn upload_multipart(
        &self,
        key: &str,
        data: Vec<u8>,
        content_type: Option<&str>,
    ) -> AppResult<()> {
        let file_size = data.len();
        debug!(
            "Starting multipart upload for key={}, size={}MB",
            key,
            file_size / (1024 * 1024)
        );

        // 1. Initiate multipart upload
        let mut create_request = self
            .client
            .create_multipart_upload()
            .bucket(&self.bucket)
            .key(key);

        if let Some(ct) = content_type {
            create_request = create_request.content_type(ct);
        }

        let create_response = create_request.send().await.map_err(|e| {
            error!(
                "Failed to initiate multipart upload: key={}, error={}",
                key, e
            );
            AppError::Storage(format!("Failed to initiate multipart upload: {}", e))
        })?;

        let upload_id = create_response.upload_id().ok_or_else(|| {
            AppError::Storage("No upload ID returned from multipart upload initiation".to_string())
        })?;

        // 2. Upload parts
        let mut completed_parts: Vec<CompletedPart> = Vec::new();
        let mut part_number = 1i32;
        let mut offset = 0usize;

        while offset < file_size {
            let end = std::cmp::min(offset + MULTIPART_PART_SIZE, file_size);
            let part_data = data[offset..end].to_vec();
            let part_size = part_data.len();

            debug!(
                "Uploading part {} for key={} ({}KB)",
                part_number,
                key,
                part_size / 1024
            );

            let upload_result = self
                .client
                .upload_part()
                .bucket(&self.bucket)
                .key(key)
                .upload_id(upload_id)
                .part_number(part_number)
                .body(ByteStream::from(part_data))
                .send()
                .await;

            match upload_result {
                Ok(response) => {
                    let e_tag = response.e_tag().map(|s| s.to_string());
                    completed_parts.push(
                        CompletedPart::builder()
                            .set_e_tag(e_tag)
                            .part_number(part_number)
                            .build(),
                    );
                }
                Err(e) => {
                    // Abort the multipart upload on failure
                    let _ = self
                        .client
                        .abort_multipart_upload()
                        .bucket(&self.bucket)
                        .key(key)
                        .upload_id(upload_id)
                        .send()
                        .await;

                    error!(
                        "Failed to upload part {}: key={}, error={}",
                        part_number, key, e
                    );
                    return Err(AppError::Storage(format!(
                        "Failed to upload part {}: {}",
                        part_number, e
                    )));
                }
            }

            offset = end;
            part_number += 1;
        }

        // 3. Complete multipart upload
        let completed_upload = CompletedMultipartUpload::builder()
            .set_parts(Some(completed_parts))
            .build();

        self.client
            .complete_multipart_upload()
            .bucket(&self.bucket)
            .key(key)
            .upload_id(upload_id)
            .multipart_upload(completed_upload)
            .send()
            .await
            .map_err(|e| {
                error!(
                    "Failed to complete multipart upload: key={}, error={}",
                    key, e
                );
                AppError::Storage(format!("Failed to complete multipart upload: {}", e))
            })?;

        info!(
            "Completed multipart upload for key={}, size={}MB, parts={}",
            key,
            file_size / (1024 * 1024),
            part_number - 1
        );

        Ok(())
    }

    /// Upload a file to S3 from a stream.
    ///
    /// # Arguments
    /// * `key` - The S3 object key (path within the bucket)
    /// * `stream` - The byte stream to upload
    /// * `content_type` - Optional MIME type for the file
    /// * `content_length` - The size of the content in bytes
    #[allow(dead_code)]
    pub async fn upload_stream(
        &self,
        key: &str,
        stream: ByteStream,
        content_type: Option<&str>,
        content_length: i64,
    ) -> AppResult<()> {
        let mut request = self
            .client
            .put_object()
            .bucket(&self.bucket)
            .key(key)
            .body(stream)
            .content_length(content_length);

        if let Some(ct) = content_type {
            request = request.content_type(ct);
        }

        request.send().await.map_err(|e| {
            error!("Failed to upload stream to S3: key={}, error={}", key, e);
            AppError::Storage(format!("Failed to upload file: {}", e))
        })?;

        Ok(())
    }

    /// Download a file from S3.
    ///
    /// # Arguments
    /// * `key` - The S3 object key to download
    ///
    /// # Returns
    /// The file contents as bytes, or None if not found
    pub async fn download(&self, key: &str) -> AppResult<Option<Vec<u8>>> {
        match self
            .client
            .get_object()
            .bucket(&self.bucket)
            .key(key)
            .send()
            .await
        {
            Ok(response) => {
                let data = response.body.collect().await.map_err(|e| {
                    AppError::Storage(format!("Failed to read S3 object body: {}", e))
                })?;
                Ok(Some(data.into_bytes().to_vec()))
            }
            Err(e) => {
                let service_error = e.into_service_error();
                if service_error.is_no_such_key() {
                    Ok(None)
                } else {
                    Err(AppError::Storage(format!(
                        "Failed to download from S3: {}",
                        service_error
                    )))
                }
            }
        }
    }

    /// Check if an object exists in S3.
    ///
    /// # Arguments
    /// * `key` - The S3 object key to check
    #[allow(dead_code)]
    pub async fn exists(&self, key: &str) -> AppResult<bool> {
        match self
            .client
            .head_object()
            .bucket(&self.bucket)
            .key(key)
            .send()
            .await
        {
            Ok(_) => Ok(true),
            Err(e) => {
                let service_error = e.into_service_error();
                if service_error.is_not_found() {
                    Ok(false)
                } else {
                    Err(AppError::Storage(format!(
                        "Failed to check object existence: {}",
                        service_error
                    )))
                }
            }
        }
    }

    /// Delete a file from S3.
    ///
    /// # Arguments
    /// * `key` - The S3 object key to delete
    pub async fn delete(&self, key: &str) -> AppResult<()> {
        self.client
            .delete_object()
            .bucket(&self.bucket)
            .key(key)
            .send()
            .await
            .map_err(|e| {
                error!("Failed to delete from S3: key={}, error={}", key, e);
                AppError::Storage(format!("Failed to delete file: {}", e))
            })?;

        Ok(())
    }

    /// Delete all objects with a given prefix (effectively deleting a "directory").
    ///
    /// # Arguments
    /// * `prefix` - The prefix (directory path) to delete
    #[allow(dead_code)]
    pub async fn delete_prefix(&self, prefix: &str) -> AppResult<u64> {
        let mut deleted_count = 0u64;
        let mut continuation_token: Option<String> = None;

        loop {
            let mut request = self
                .client
                .list_objects_v2()
                .bucket(&self.bucket)
                .prefix(prefix);

            if let Some(token) = continuation_token {
                request = request.continuation_token(token);
            }

            let response = request.send().await.map_err(|e| {
                AppError::Storage(format!("Failed to list objects for deletion: {}", e))
            })?;

            if let Some(contents) = response.contents {
                for object in contents {
                    if let Some(key) = object.key {
                        self.delete(&key).await?;
                        deleted_count += 1;
                    }
                }
            }

            if response.is_truncated == Some(true) {
                continuation_token = response.next_continuation_token;
            } else {
                break;
            }
        }

        if deleted_count > 0 {
            info!(
                "Deleted {} objects with prefix '{}' from S3",
                deleted_count, prefix
            );
        }

        Ok(deleted_count)
    }

    /// List all objects with a given prefix.
    ///
    /// # Arguments
    /// * `prefix` - The prefix to filter objects
    pub async fn list(&self, prefix: &str) -> AppResult<Vec<String>> {
        let mut keys = Vec::new();
        let mut continuation_token: Option<String> = None;

        loop {
            let mut request = self
                .client
                .list_objects_v2()
                .bucket(&self.bucket)
                .prefix(prefix);

            if let Some(token) = continuation_token {
                request = request.continuation_token(token);
            }

            let response = request
                .send()
                .await
                .map_err(|e| AppError::Storage(format!("Failed to list objects: {}", e)))?;

            if let Some(contents) = response.contents {
                for object in contents {
                    if let Some(key) = object.key {
                        keys.push(key);
                    }
                }
            }

            if response.is_truncated == Some(true) {
                continuation_token = response.next_continuation_token;
            } else {
                break;
            }
        }

        Ok(keys)
    }

    /// Generate a pre-signed URL for downloading a file.
    ///
    /// # Arguments
    /// * `key` - The S3 object key
    /// * `expires_in` - How long the URL should be valid
    #[allow(dead_code)]
    pub async fn presigned_download_url(
        &self,
        key: &str,
        expires_in: Duration,
    ) -> AppResult<String> {
        let presigning_config = PresigningConfig::expires_in(expires_in)
            .map_err(|e| AppError::Storage(format!("Invalid presigning duration: {}", e)))?;

        let presigned = self
            .client
            .get_object()
            .bucket(&self.bucket)
            .key(key)
            .presigned(presigning_config)
            .await
            .map_err(|e| AppError::Storage(format!("Failed to generate presigned URL: {}", e)))?;

        Ok(presigned.uri().to_string())
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

    /// Build an S3 key for a report file.
    ///
    /// # Arguments
    /// * `report_id` - The report UUID
    /// * `filename` - The filename within the report
    pub fn report_key(report_id: &str, filename: &str) -> String {
        format!("reports/{}/{}", report_id, filename)
    }

    /// Extract report ID from an S3 key.
    #[allow(dead_code)]
    pub fn extract_report_id(key: &str) -> Option<&str> {
        // Key format: reports/{report_id}/{filename}
        let parts: Vec<&str> = key.splitn(3, '/').collect();
        if parts.len() >= 2 && parts[0] == "reports" {
            Some(parts[1])
        } else {
            None
        }
    }

    /// Get the bucket name.
    #[allow(dead_code)]
    pub fn bucket(&self) -> &str {
        &self.bucket
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_report_key() {
        let key = Storage::report_key("abc-123", "index.html");
        assert_eq!(key, "reports/abc-123/index.html");
    }

    #[test]
    fn test_extract_report_id() {
        assert_eq!(
            Storage::extract_report_id("reports/abc-123/index.html"),
            Some("abc-123")
        );
        assert_eq!(
            Storage::extract_report_id("reports/abc-123/data/trace.zip"),
            Some("abc-123")
        );
        assert_eq!(Storage::extract_report_id("other/path"), None);
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
