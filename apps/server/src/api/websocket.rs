//! WebSocket handler for real-time updates.
//!
//! Handles WebSocket upgrade requests and streams events to connected clients.

use actix_web::{web, HttpRequest, HttpResponse};
use actix_ws::Message;
use futures_util::StreamExt;
use std::time::{Duration, Instant};
use tokio::sync::broadcast::error::RecvError;
use tracing::{debug, info, warn};

use crate::services::EventBroadcaster;

/// Ping interval for keeping connections alive.
const PING_INTERVAL: Duration = Duration::from_secs(30);

/// Timeout for receiving pong response.
const PONG_TIMEOUT: Duration = Duration::from_secs(10);

/// WebSocket handler - upgrades HTTP connection to WebSocket.
pub async fn websocket_handler(
    req: HttpRequest,
    stream: web::Payload,
    broadcaster: web::Data<EventBroadcaster>,
) -> Result<HttpResponse, actix_web::Error> {
    let (response, session, msg_stream) = actix_ws::handle(&req, stream)?;

    // Get client info for logging
    let client_addr = req
        .connection_info()
        .realip_remote_addr()
        .map(String::from)
        .unwrap_or_else(|| "unknown".to_string());

    info!(client = %client_addr, "WebSocket connection established");

    // Spawn the connection handler task
    actix_web::rt::spawn(handle_websocket_connection(
        session,
        msg_stream,
        broadcaster.get_ref().clone(),
        client_addr,
    ));

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
