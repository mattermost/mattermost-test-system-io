//! Request logging middleware for detailed API request/response logging.

use actix_web::dev::{forward_ready, Service, ServiceRequest, ServiceResponse, Transform};
use actix_web::Error;
use futures_util::future::LocalBoxFuture;
use std::future::{ready, Ready};
use std::time::Instant;
use tracing::{info, warn};

/// Request logger middleware factory.
pub struct RequestLogger;

impl<S, B> Transform<S, ServiceRequest> for RequestLogger
where
    S: Service<ServiceRequest, Response = ServiceResponse<B>, Error = Error>,
    S::Future: 'static,
    B: 'static,
{
    type Response = ServiceResponse<B>;
    type Error = Error;
    type InitError = ();
    type Transform = RequestLoggerMiddleware<S>;
    type Future = Ready<Result<Self::Transform, Self::InitError>>;

    fn new_transform(&self, service: S) -> Self::Future {
        ready(Ok(RequestLoggerMiddleware { service }))
    }
}

/// Request logger middleware service.
pub struct RequestLoggerMiddleware<S> {
    service: S,
}

impl<S, B> Service<ServiceRequest> for RequestLoggerMiddleware<S>
where
    S: Service<ServiceRequest, Response = ServiceResponse<B>, Error = Error>,
    S::Future: 'static,
    B: 'static,
{
    type Response = ServiceResponse<B>;
    type Error = Error;
    type Future = LocalBoxFuture<'static, Result<Self::Response, Self::Error>>;

    forward_ready!(service);

    fn call(&self, req: ServiceRequest) -> Self::Future {
        let start = Instant::now();
        let method = req.method().to_string();
        let path = req.path().to_string();
        let query = req.query_string().to_string();
        let remote_addr = req
            .connection_info()
            .realip_remote_addr()
            .unwrap_or("unknown")
            .to_string();
        let user_agent = req
            .headers()
            .get("user-agent")
            .and_then(|v| v.to_str().ok())
            .unwrap_or("unknown")
            .to_string();

        // Extract API key prefix for logging (first 8 chars only)
        let api_key_info = req
            .headers()
            .get("x-api-key")
            .and_then(|v| v.to_str().ok())
            .map(|k| {
                if k.len() >= 8 {
                    format!("{}...", &k[..8])
                } else {
                    "invalid".to_string()
                }
            })
            .unwrap_or_else(|| "none".to_string());

        // Log request start
        info!(
            target: "api",
            method = %method,
            path = %path,
            query = %query,
            remote_addr = %remote_addr,
            user_agent = %user_agent,
            api_key = %api_key_info,
            "→ Request started"
        );

        let fut = self.service.call(req);

        Box::pin(async move {
            let res = fut.await?;
            let elapsed = start.elapsed();
            let status = res.status();
            let status_code = status.as_u16();

            // Log based on status
            if status.is_success() {
                info!(
                    target: "api",
                    method = %method,
                    path = %path,
                    status = %status_code,
                    duration_ms = %elapsed.as_millis(),
                    "← Request completed"
                );
            } else if status.is_client_error() {
                warn!(
                    target: "api",
                    method = %method,
                    path = %path,
                    status = %status_code,
                    duration_ms = %elapsed.as_millis(),
                    "← Client error"
                );
            } else {
                warn!(
                    target: "api",
                    method = %method,
                    path = %path,
                    status = %status_code,
                    duration_ms = %elapsed.as_millis(),
                    "← Server error"
                );
            }

            Ok(res)
        })
    }
}
