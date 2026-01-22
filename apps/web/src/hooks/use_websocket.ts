/**
 * React hooks for WebSocket connection management and event handling.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  type ConnectionStatus,
  ReconnectingWebSocket,
  getWebSocketUrl,
} from '../services/websocket';
import type { WsEventMessage } from '../types/websocket';

/**
 * Hook for managing the WebSocket connection.
 * Returns the current connection status and control functions.
 */
export function useWebSocket() {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected');
  const wsRef = useRef<ReconnectingWebSocket | null>(null);
  const onMessageCallbackRef = useRef<((event: WsEventMessage) => void) | null>(null);

  const connect = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.disconnect();
    }

    const ws = new ReconnectingWebSocket(getWebSocketUrl(), {
      onStatusChange: setStatus,
      onMessage: (event) => {
        onMessageCallbackRef.current?.(event);
      },
    });

    ws.connect();
    wsRef.current = ws;
  }, []);

  const disconnect = useCallback(() => {
    wsRef.current?.disconnect();
    wsRef.current = null;
  }, []);

  const reconnect = useCallback(() => {
    wsRef.current?.reconnect();
  }, []);

  const setOnMessage = useCallback((callback: (event: WsEventMessage) => void) => {
    onMessageCallbackRef.current = callback;
  }, []);

  // Connect on mount, disconnect on unmount
  useEffect(() => {
    connect();
    return () => {
      disconnect();
    };
  }, [connect, disconnect]);

  return {
    status,
    connect,
    disconnect,
    reconnect,
    setOnMessage,
  };
}

/**
 * Hook that handles WebSocket events and invalidates React Query cache accordingly.
 * This should be used at the top level of the app to ensure events are processed globally.
 */
export function useWebSocketEvents() {
  const queryClient = useQueryClient();
  const { status, reconnect, setOnMessage } = useWebSocket();

  useEffect(() => {
    setOnMessage((event: WsEventMessage) => {
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
    });
  }, [queryClient, setOnMessage]);

  return { status, reconnect };
}

/**
 * Hook for subscribing to events for a specific report.
 * Filters events to only those relevant to the specified report.
 */
export function useReportWebSocketEvents(reportId: string | undefined) {
  const queryClient = useQueryClient();
  const { status, reconnect, setOnMessage } = useWebSocket();

  useEffect(() => {
    if (!reportId) return;

    setOnMessage((event: WsEventMessage) => {
      // Only process events for this specific report
      const eventReportId =
        'report_id' in event.payload ? event.payload.report_id : null;

      if (eventReportId !== reportId) {
        return;
      }

      console.log('[WS] Received event for report:', event.type, reportId);

      switch (event.type) {
        case 'report_updated':
          queryClient.invalidateQueries({
            queryKey: ['report-with-jobs', reportId],
          });
          break;

        case 'job_created':
        case 'job_updated':
          queryClient.invalidateQueries({
            queryKey: ['report-with-jobs', reportId],
          });
          break;

        case 'suites_available':
          queryClient.invalidateQueries({
            queryKey: ['report', reportId, 'suites'],
          });
          break;
      }
    });
  }, [reportId, queryClient, setOnMessage]);

  return { status, reconnect };
}
