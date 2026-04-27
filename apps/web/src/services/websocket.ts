/**
 * WebSocket client for real-time updates.
 *
 * Provides automatic reconnection with exponential backoff.
 *
 * In addition to the generic `onMessage` callback, this module exposes a
 * targeted subscription API for Test Shard Orchestration runs:
 *   subscribeToOrchestrationRun(identity, callback)
 *
 * Orchestration subscribers receive only events whose composite identity
 * matches their subscription. Subscribe/unsubscribe frames are sent over
 * the same WebSocket connection — this module never opens a second socket.
 */

import {
  compositeIdentityKey,
  type CompositeIdentity,
  type OrchestrationEvent,
  type OrchestrationEventType,
} from '@/types/orchestration';
import type { WsEventMessage } from '@/types/websocket';

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'reconnecting';

export interface WebSocketClientOptions {
  /** Base delay for reconnection attempts (ms). Default: 1000 */
  baseReconnectDelay?: number;
  /** Maximum delay between reconnection attempts (ms). Default: 30000 */
  maxReconnectDelay?: number;
  /** Maximum number of reconnection attempts. Default: 10 */
  maxReconnectAttempts?: number;
  /** Callback when connection status changes */
  onStatusChange?: (status: ConnectionStatus) => void;
  /** Callback when a non-orchestration message is received */
  onMessage?: (event: WsEventMessage) => void;
  /** Callback when an error occurs */
  onError?: (error: Event) => void;
}

const DEFAULT_OPTIONS: Required<
  Omit<WebSocketClientOptions, 'onStatusChange' | 'onMessage' | 'onError'>
> = {
  baseReconnectDelay: 1000,
  maxReconnectDelay: 30000,
  maxReconnectAttempts: 10,
};

const ORCHESTRATION_EVENT_TYPES: ReadonlySet<string> = new Set<OrchestrationEventType>([
  'orchestration.run.started',
  'orchestration.unit.leased',
  'orchestration.unit.completed',
  'orchestration.lease.expired',
  'orchestration.run.completed',
  'orchestration.run.timed_out',
]);

function isOrchestrationEvent(value: unknown): value is OrchestrationEvent {
  if (!value || typeof value !== 'object') return false;
  const type = (value as { type?: unknown }).type;
  return typeof type === 'string' && ORCHESTRATION_EVENT_TYPES.has(type);
}

/**
 * WebSocket client with automatic reconnection support.
 */
export class ReconnectingWebSocket {
  private ws: WebSocket | null = null;
  private url: string;
  private options: Required<
    Omit<WebSocketClientOptions, 'onStatusChange' | 'onMessage' | 'onError'>
  > &
    Pick<WebSocketClientOptions, 'onStatusChange' | 'onMessage' | 'onError'>;
  private reconnectAttempts = 0;
  private reconnectTimeoutId: ReturnType<typeof setTimeout> | null = null;
  private _status: ConnectionStatus = 'disconnected';
  private manualClose = false;
  /** Frames queued while the socket was not yet open. Flushed on connect. */
  private pendingFrames: string[] = [];

  constructor(url: string, options: WebSocketClientOptions = {}) {
    this.url = url;
    this.options = { ...DEFAULT_OPTIONS, ...options };
    // The most recently constructed client becomes the orchestration transport.
    // The provider creates a single client for the app's lifetime, so this is
    // effectively a singleton registration; the indirection lets the
    // subscribeToOrchestrationRun() module export work without the provider
    // having to wire it up explicitly.
    registerOrchestrationTransport(this);
  }

  /** Current connection status */
  get status(): ConnectionStatus {
    return this._status;
  }

  /** Connect to the WebSocket server */
  connect(): void {
    if (this.ws?.readyState === WebSocket.OPEN || this.ws?.readyState === WebSocket.CONNECTING) {
      return;
    }

    this.manualClose = false;
    this.setStatus('connecting');

    try {
      this.ws = new WebSocket(this.url);
      this.setupEventHandlers();
    } catch (error) {
      console.error('[WS] Failed to create WebSocket:', error);
      this.setStatus('disconnected');
      this.scheduleReconnect();
    }
  }

  /** Disconnect from the WebSocket server */
  disconnect(): void {
    this.manualClose = true;
    this.clearReconnectTimeout();

    if (this.ws) {
      this.ws.close(1000, 'Client closing');
      this.ws = null;
    }

    this.setStatus('disconnected');
    this.reconnectAttempts = 0;

    // Release ourselves from the orchestration transport singleton so a
    // future client (e.g. on provider remount in dev StrictMode) can take over.
    if (activeClient === this) {
      activeClient = null;
    }
  }

  /** Manually trigger a reconnection */
  reconnect(): void {
    this.disconnect();
    this.manualClose = false;
    this.connect();
  }

  /**
   * Send a JSON-serializable payload over the socket. If the socket is not
   * yet open, the frame is queued and flushed on the next `onopen`.
   */
  send(payload: unknown): void {
    const frame = JSON.stringify(payload);
    if (this.ws?.readyState === WebSocket.OPEN) {
      try {
        this.ws.send(frame);
      } catch (error) {
        console.error('[WS] Failed to send frame:', error);
        this.pendingFrames.push(frame);
      }
      return;
    }
    this.pendingFrames.push(frame);
  }

  private setupEventHandlers(): void {
    if (!this.ws) return;

    this.ws.onopen = () => {
      console.log('[WS] Connected');
      this.reconnectAttempts = 0;
      this.setStatus('connected');
      this.flushPendingFrames();
    };

    this.ws.onclose = (event) => {
      console.log('[WS] Disconnected:', event.code, event.reason);

      if (!this.manualClose && this.reconnectAttempts < this.options.maxReconnectAttempts) {
        this.scheduleReconnect();
      } else {
        this.setStatus('disconnected');
      }
    };

    this.ws.onmessage = (event) => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(event.data);
      } catch (error) {
        console.error('[WS] Failed to parse message:', error);
        return;
      }

      if (isOrchestrationEvent(parsed)) {
        dispatchOrchestrationEvent(parsed);
        return;
      }

      this.options.onMessage?.(parsed as WsEventMessage);
    };

    this.ws.onerror = (error) => {
      console.error('[WS] Error:', error);
      this.options.onError?.(error);
    };
  }

  private flushPendingFrames(): void {
    if (this.ws?.readyState !== WebSocket.OPEN) return;
    const frames = this.pendingFrames;
    this.pendingFrames = [];
    for (const frame of frames) {
      try {
        this.ws.send(frame);
      } catch (error) {
        console.error('[WS] Failed to flush queued frame:', error);
        this.pendingFrames.push(frame);
      }
    }
  }

  private scheduleReconnect(): void {
    if (this.manualClose) return;

    this.setStatus('reconnecting');

    const delay = Math.min(
      this.options.baseReconnectDelay * Math.pow(2, this.reconnectAttempts),
      this.options.maxReconnectDelay,
    );

    console.log(`[WS] Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts + 1})`);

    this.reconnectTimeoutId = setTimeout(() => {
      this.reconnectAttempts++;
      this.connect();
    }, delay);
  }

  private clearReconnectTimeout(): void {
    if (this.reconnectTimeoutId) {
      clearTimeout(this.reconnectTimeoutId);
      this.reconnectTimeoutId = null;
    }
  }

  private setStatus(status: ConnectionStatus): void {
    if (this._status !== status) {
      this._status = status;
      this.options.onStatusChange?.(status);
    }
  }
}

/**
 * Get the WebSocket URL based on the current environment.
 */
export function getWebSocketUrl(): string {
  // Use environment variable if set
  const envUrl = import.meta.env.VITE_WS_URL;
  if (envUrl) {
    return envUrl;
  }

  // Derive from current location
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const host = window.location.host;

  // In development, connect directly to the backend (Vite HTTP proxy
  // doesn't reliably handle WebSocket upgrades)
  if (import.meta.env.DEV) {
    return `ws://localhost:8080/api/v1/ws`;
  }

  return `${protocol}//${host}/api/v1/ws`;
}

// ─── Orchestration subscription registry ───────────────────────────────────

/**
 * Active `ReconnectingWebSocket` used as the transport for orchestration
 * subscribe/unsubscribe frames. The provider sets this when it constructs
 * its singleton client; subscriptions opened before the provider mounts (or
 * across reconnects) are buffered as pending frames on the client.
 */
let activeClient: ReconnectingWebSocket | null = null;

/** Per-identity callback registry, keyed by `compositeIdentityKey`. */
const orchestrationSubscribers = new Map<string, Array<(event: OrchestrationEvent) => void>>();

/** Identities for which we have already issued a `subscribe.orchestration` frame. */
const subscribedIdentities = new Map<string, CompositeIdentity>();

/**
 * Register a `ReconnectingWebSocket` as the transport used for orchestration
 * subscriptions. The provider calls this once after constructing its client.
 * On reconnect, any identities already in `subscribedIdentities` have their
 * subscribe frames re-sent so the server-side subscriber list is restored.
 */
export function registerOrchestrationTransport(client: ReconnectingWebSocket | null): void {
  activeClient = client;
  if (!client) return;
  // Re-issue subscribe frames for any identities we believe we are subscribed
  // to, e.g. across a reconnect. Frames are queued until the socket opens.
  for (const identity of subscribedIdentities.values()) {
    client.send({ type: 'subscribe.orchestration', identity });
  }
}

function dispatchOrchestrationEvent(event: OrchestrationEvent): void {
  // Match subscribers by composite identity key. The server scopes events by
  // identity already, but a single client may hold multiple subscriptions
  // and the demux happens here.
  const key = compositeIdentityKey(event.identity);
  const callbacks = orchestrationSubscribers.get(key);
  if (!callbacks) return;
  for (const cb of callbacks) {
    try {
      cb(event);
    } catch (error) {
      console.error('[WS] Orchestration subscriber threw:', error);
    }
  }
}

/**
 * Subscribe to live orchestration events for a single run, identified by its
 * composite identity. Returns an `unsubscribe` function that removes the
 * callback and, when no other callbacks remain for that identity, sends an
 * `unsubscribe.orchestration` frame.
 *
 * Frames are sent over the active `ReconnectingWebSocket` registered via
 * `registerOrchestrationTransport`. If no transport is registered yet the
 * subscription is recorded locally; the next call to
 * `registerOrchestrationTransport` will issue the subscribe frame.
 */
export function subscribeToOrchestrationRun(
  identity: CompositeIdentity,
  callback: (event: OrchestrationEvent) => void,
): () => void {
  const key = compositeIdentityKey(identity);

  const existing = orchestrationSubscribers.get(key);
  if (existing) {
    existing.push(callback);
  } else {
    orchestrationSubscribers.set(key, [callback]);
  }

  if (!subscribedIdentities.has(key)) {
    subscribedIdentities.set(key, identity);
    activeClient?.send({ type: 'subscribe.orchestration', identity });
  }

  return () => {
    const callbacks = orchestrationSubscribers.get(key);
    if (!callbacks) return;
    const idx = callbacks.indexOf(callback);
    if (idx >= 0) callbacks.splice(idx, 1);
    if (callbacks.length === 0) {
      orchestrationSubscribers.delete(key);
      const remembered = subscribedIdentities.get(key);
      subscribedIdentities.delete(key);
      if (remembered) {
        activeClient?.send({ type: 'unsubscribe.orchestration', identity: remembered });
      }
    }
  };
}
