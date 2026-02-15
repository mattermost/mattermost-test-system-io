/**
 * WebSocket client for real-time updates.
 *
 * Provides automatic reconnection with exponential backoff.
 */

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
  /** Callback when a message is received */
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

  constructor(url: string, options: WebSocketClientOptions = {}) {
    this.url = url;
    this.options = { ...DEFAULT_OPTIONS, ...options };
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
  }

  /** Manually trigger a reconnection */
  reconnect(): void {
    this.disconnect();
    this.manualClose = false;
    this.connect();
  }

  private setupEventHandlers(): void {
    if (!this.ws) return;

    this.ws.onopen = () => {
      console.log('[WS] Connected');
      this.reconnectAttempts = 0;
      this.setStatus('connected');
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
      try {
        const message: WsEventMessage = JSON.parse(event.data);
        this.options.onMessage?.(message);
      } catch (error) {
        console.error('[WS] Failed to parse message:', error);
      }
    };

    this.ws.onerror = (error) => {
      console.error('[WS] Error:', error);
      this.options.onError?.(error);
    };
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

  // In development, API is on port 8080
  if (import.meta.env.DEV) {
    return `ws://localhost:8080/api/v1/ws`;
  }

  return `${protocol}//${host}/api/v1/ws`;
}
