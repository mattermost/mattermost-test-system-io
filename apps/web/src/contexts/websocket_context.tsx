/**
 * WebSocket context for global connection state management.
 *
 * Provides a single WebSocket connection that can be shared across
 * all components, with automatic reconnection and status tracking.
 */

import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  useCallback,
  type ReactNode,
} from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  type ConnectionStatus,
  ReconnectingWebSocket,
  getWebSocketUrl,
} from '../services/websocket';
import type { WsEventMessage } from '../types/websocket';

interface WebSocketContextValue {
  /** Current connection status */
  status: ConnectionStatus;
  /** Manually trigger reconnection */
  reconnect: () => void;
  /** Check if connected */
  isConnected: boolean;
}

const WebSocketContext = createContext<WebSocketContextValue | null>(null);

interface WebSocketProviderProps {
  children: ReactNode;
}

/**
 * Provider component that manages the WebSocket connection.
 * Should be placed near the top of the component tree.
 */
export function WebSocketProvider({ children }: WebSocketProviderProps) {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected');
  const wsRef = useRef<ReconnectingWebSocket | null>(null);
  const queryClient = useQueryClient();

  // Handle incoming WebSocket messages
  const handleMessage = useCallback(
    (event: WsEventMessage) => {
      console.log('[WS] Received event:', event.type);

      switch (event.type) {
        case 'report_created':
          // Invalidate the reports list
          queryClient.invalidateQueries({ queryKey: ['reports'] });
          break;

        case 'report_updated':
          // Invalidate both list and specific report
          queryClient.invalidateQueries({ queryKey: ['reports'] });
          queryClient.invalidateQueries({
            queryKey: ['report-with-jobs', event.payload.report_id],
          });
          break;

        case 'job_created':
        case 'job_updated':
          // Invalidate the specific report and reports list (for job count)
          queryClient.invalidateQueries({
            queryKey: ['report-with-jobs', event.payload.report_id],
          });
          queryClient.invalidateQueries({ queryKey: ['reports'] });
          break;

        case 'suites_available':
          // Invalidate suites for the report
          queryClient.invalidateQueries({
            queryKey: ['report', event.payload.report_id, 'suites'],
          });
          break;
      }
    },
    [queryClient],
  );

  // Initialize WebSocket connection
  useEffect(() => {
    const ws = new ReconnectingWebSocket(getWebSocketUrl(), {
      onStatusChange: setStatus,
      onMessage: handleMessage,
      onError: (error) => {
        console.error('[WS] Connection error:', error);
      },
    });

    ws.connect();
    wsRef.current = ws;

    return () => {
      ws.disconnect();
      wsRef.current = null;
    };
  }, [handleMessage]);

  // Reconnect function
  const reconnect = useCallback(() => {
    wsRef.current?.reconnect();
  }, []);

  const value: WebSocketContextValue = {
    status,
    reconnect,
    isConnected: status === 'connected',
  };

  return <WebSocketContext.Provider value={value}>{children}</WebSocketContext.Provider>;
}

/**
 * Hook to access the WebSocket context.
 * Must be used within a WebSocketProvider.
 */
export function useWebSocketContext(): WebSocketContextValue {
  const context = useContext(WebSocketContext);
  if (!context) {
    throw new Error('useWebSocketContext must be used within a WebSocketProvider');
  }
  return context;
}

/**
 * Hook to get just the connection status.
 * Convenience wrapper around useWebSocketContext.
 */
export function useWebSocketStatus() {
  const { status, reconnect, isConnected } = useWebSocketContext();
  return { status, reconnect, isConnected };
}
