//! Event broadcaster for WebSocket real-time updates.
//!
//! Uses tokio::sync::broadcast to fan-out events to all connected WebSocket clients.

use tokio::sync::broadcast;

use crate::models::WsEventMessage;

/// Default capacity for the broadcast channel.
const DEFAULT_CHANNEL_CAPACITY: usize = 1000;

/// Event broadcaster that distributes events to all connected WebSocket clients.
#[derive(Clone)]
pub struct EventBroadcaster {
    sender: broadcast::Sender<WsEventMessage>,
}

impl EventBroadcaster {
    /// Create a new EventBroadcaster with the default capacity.
    pub fn new() -> Self {
        Self::with_capacity(DEFAULT_CHANNEL_CAPACITY)
    }

    /// Create a new EventBroadcaster with a specific capacity.
    pub fn with_capacity(capacity: usize) -> Self {
        let (sender, _) = broadcast::channel(capacity);
        Self { sender }
    }

    /// Subscribe to receive events.
    /// Returns a receiver that will receive all future events.
    pub fn subscribe(&self) -> broadcast::Receiver<WsEventMessage> {
        self.sender.subscribe()
    }

    /// Broadcast an event to all subscribers.
    /// Returns the number of receivers that received the event.
    /// If there are no subscribers, returns 0 (does not error).
    pub fn send(&self, event: WsEventMessage) -> usize {
        // Ignore errors when there are no subscribers
        self.sender.send(event).unwrap_or(0)
    }
}

impl Default for EventBroadcaster {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::WsEvent;
    use uuid::Uuid;

    #[tokio::test]
    async fn test_broadcast_to_multiple_receivers() {
        let broadcaster = EventBroadcaster::new();

        let mut rx1 = broadcaster.subscribe();
        let mut rx2 = broadcaster.subscribe();

        let event = WsEventMessage::new(WsEvent::report_created(Uuid::now_v7()));

        let count = broadcaster.send(event);
        assert_eq!(count, 2);

        // Both receivers should get the event
        assert!(rx1.recv().await.is_ok());
        assert!(rx2.recv().await.is_ok());
    }

    #[test]
    fn test_no_subscribers_no_error() {
        let broadcaster = EventBroadcaster::new();

        let event = WsEventMessage::new(WsEvent::report_updated(Uuid::now_v7()));

        // Should not panic or error, just return 0
        let count = broadcaster.send(event);
        assert_eq!(count, 0);
    }
}
