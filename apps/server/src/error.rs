//! Domain error types for Test System IO.
//!
//! Uses thiserror for ergonomic error handling with automatic Display implementations.

use actix_web::{HttpResponse, ResponseError};
use std::fmt;

/// Application-level errors.
#[derive(Debug, thiserror::Error)]
pub enum AppError {
    /// Database operation failed
    #[error("Database error: {0}")]
    Database(String),

    /// Resource not found
    #[error("{0} not found")]
    NotFound(String),

    /// Invalid input data
    #[error("Invalid input: {0}")]
    InvalidInput(String),

    /// Authentication failed
    #[error("Unauthorized: {0}")]
    Unauthorized(String),

    /// Storage (S3) operation failed
    #[error("Storage error: {0}")]
    Storage(String),
}

impl ResponseError for AppError {
    fn error_response(&self) -> HttpResponse {
        let (status, error_code, response_message) = match self {
            AppError::Database(err_str) => {
                tracing::error!("Database error: {}", err_str);
                (
                    actix_web::http::StatusCode::INTERNAL_SERVER_ERROR,
                    "DATABASE_ERROR",
                    "An internal database error occurred".to_string(),
                )
            }
            AppError::NotFound(_) => (
                actix_web::http::StatusCode::NOT_FOUND,
                "NOT_FOUND",
                self.to_string(),
            ),
            AppError::InvalidInput(_) => (
                actix_web::http::StatusCode::BAD_REQUEST,
                "INVALID_INPUT",
                self.to_string(),
            ),
            AppError::Unauthorized(_) => (
                actix_web::http::StatusCode::UNAUTHORIZED,
                "UNAUTHORIZED",
                self.to_string(),
            ),
            AppError::Storage(_) => (
                actix_web::http::StatusCode::INTERNAL_SERVER_ERROR,
                "STORAGE_ERROR",
                self.to_string(),
            ),
        };

        HttpResponse::build(status).json(ErrorResponse {
            error: error_code.to_string(),
            message: response_message,
        })
    }
}

/// Error response body matching OpenAPI schema.
#[derive(Debug, serde::Serialize, utoipa::ToSchema)]
pub struct ErrorResponse {
    pub error: String,
    pub message: String,
}

impl fmt::Display for ErrorResponse {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.error, self.message)
    }
}

/// Convenience type alias for Results with AppError.
pub type AppResult<T> = Result<T, AppError>;

// Conversion implementations for common error types

impl From<serde_json::Error> for AppError {
    fn from(err: serde_json::Error) -> Self {
        AppError::InvalidInput(format!("JSON parsing error: {}", err))
    }
}

impl From<sea_orm::DbErr> for AppError {
    fn from(err: sea_orm::DbErr) -> Self {
        AppError::Database(err.to_string())
    }
}

impl From<uuid::Error> for AppError {
    fn from(err: uuid::Error) -> Self {
        AppError::InvalidInput(format!("Invalid UUID: {}", err))
    }
}
