//! WebSocket handler for real-time updates.
//!
//! Handles WebSocket upgrade requests and streams events to connected clients.
//!
//! # Authentication
//! The WebSocket endpoint requires authentication before upgrading the connection.
//! Supported methods (same as REST API, checked in order):
//! 1. `X-Admin-Key` header
//! 2. `X-API-Key` header (database-backed)
//! 3. `Authorization: Bearer <token>` (GitHub Actions OIDC)
//! 4. `tsio_session` cookie (GitHub OAuth session JWT)

use actix_web::{HttpRequest, HttpResponse, web};
use actix_ws::Message;
use futures_util::StreamExt;
use std::time::{Duration, Instant};
use tokio::sync::broadcast::error::RecvError;
use tracing::{debug, info, warn};

use crate::auth::ApiKeyAuth;
use crate::config::{ADMIN_KEY_HEADER, API_KEY_HEADER};
use crate::db::DbPool;
use crate::error::ErrorResponse;
use crate::services::EventBroadcaster;

/// Ping interval for keeping connections alive.
const PING_INTERVAL: Duration = Duration::from_secs(30);

/// Timeout for receiving pong response.
const PONG_TIMEOUT: Duration = Duration::from_secs(10);

/// WebSocket handler - authenticates then upgrades HTTP connection to WebSocket.
///
/// Authentication is performed before the WebSocket upgrade so that unauthenticated
/// requests are rejected with a proper HTTP 401 response rather than an open socket.
pub async fn websocket_handler(
    req: HttpRequest,
    stream: web::Payload,
    broadcaster: web::Data<EventBroadcaster>,
    pool: web::Data<DbPool>,
) -> Result<HttpResponse, actix_web::Error> {
    // Authenticate before upgrading. We build and drive the ApiKeyAuth future
    // manually so we can return a structured 401 without upgrading the socket.
    use actix_web::dev::Payload;
    let auth_result = {
        let mut payload = Payload::None;
        let fut = <ApiKeyAuth as actix_web::FromRequest>::from_request(&req, &mut payload);
        fut.await
    };

    if let Err(auth_err) = auth_result {
        warn!(
            client = %req.connection_info().realip_remote_addr().unwrap_or("unknown"),
            header_key = %req.headers().get(API_KEY_HEADER).is_some(),
            header_admin = %req.headers().get(ADMIN_KEY_HEADER).is_some(),
            "WebSocket authentication failed"
        );
        return Ok(actix_web::HttpResponse::Unauthorized().json(ErrorResponse {
            error: "UNAUTHORIZED".to_string(),
            message: auth_err.to_string(),
        }));
    }

    let auth = auth_result.unwrap();

    // Get client info for logging
    let client_addr = req
        .connection_info()
        .realip_remote_addr()
        .map(String::from)
        .unwrap_or_else(|| "unknown".to_string());

    let (response, session, msg_stream) = actix_ws::handle(&req, stream)?;

    info!(
        client = %client_addr,
        key_id = %auth.caller.key_id,
        role = %auth.caller.role,
        "WebSocket connection established"
    );

    // Spawn the connection handler task
    actix_web::rt::spawn(handle_websocket_connection(
        session,
        msg_stream,
        broadcaster.get_ref().clone(),
        client_addr,
    ));

    // pool is required by ApiKeyAuth extractor; keep it in scope until auth completes
    drop(pool);

    Ok(response)
}

/// Handles an individual WebSocket connection.
async fn handle_websocket_connection(
    mut session: actix_ws::Session,
    mut msg_stream: actix_ws::MessageStream,
    broadcaster: EventBroadcaster,
    client_addr: String,
) {
    // Subscribe to broadcast events
    let mut rx = broadcaster.subscribe();

    // Track last activity for ping/pong
    let mut last_pong = Instant::now();
    let mut ping_interval = tokio::time::interval(PING_INTERVAL);

    loop {
        tokio::select! {
            // Handle incoming WebSocket messages from client
            Some(msg_result) = msg_stream.next() => {
                match msg_result {
                    Ok(msg) => {
                        match msg {
                            Message::Ping(bytes) => {
                                debug!(client = %client_addr, "Received ping");
                                if session.pong(&bytes).await.is_err() {
                                    break;
                                }
                            }
                            Message::Pong(_) => {
                                debug!(client = %client_addr, "Received pong");
                                last_pong = Instant::now();
                            }
                            Message::Text(text) => {
                                // Log received text messages (for future subscription support)
                                debug!(client = %client_addr, message = %text, "Received text message");
                            }
                            Message::Close(reason) => {
                                info!(client = %client_addr, reason = ?reason, "Client requested close");
                                break;
                            }
                            _ => {}
                        }
                    }
                    Err(e) => {
                        warn!(client = %client_addr, error = %e, "WebSocket message error");
                        break;
                    }
                }
            }

            // Forward broadcast events to this client
            event_result = rx.recv() => {
                match event_result {
                    Ok(event) => {
                        match serde_json::to_string(&event) {
                            Ok(json) => {
                                if session.text(json).await.is_err() {
                                    warn!(client = %client_addr, "Failed to send event, closing connection");
                                    break;
                                }
                            }
                            Err(e) => {
                                warn!(error = %e, "Failed to serialize event");
                            }
                        }
                    }
                    Err(RecvError::Lagged(count)) => {
                        warn!(client = %client_addr, missed = count, "Client lagged, missed events");
                        // Continue - client will get future events
                    }
                    Err(RecvError::Closed) => {
                        info!(client = %client_addr, "Broadcast channel closed");
                        break;
                    }
                }
            }

            // Send periodic pings
            _ = ping_interval.tick() => {
                // Check if we've received a pong recently
                if last_pong.elapsed() > PING_INTERVAL + PONG_TIMEOUT {
                    warn!(client = %client_addr, "Pong timeout, closing connection");
                    break;
                }

                // Send ping
                if session.ping(b"").await.is_err() {
                    warn!(client = %client_addr, "Failed to send ping, closing connection");
                    break;
                }
            }
        }
    }

    // Clean up
    let _ = session.close(None).await;
    info!(client = %client_addr, "WebSocket connection closed");
}

/// Configure WebSocket routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(web::resource("/ws").route(web::get().to(websocket_handler)));
}
