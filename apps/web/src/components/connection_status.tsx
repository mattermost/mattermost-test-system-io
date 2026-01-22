/**
 * Connection status indicator component.
 *
 * Shows the current WebSocket connection status as a colored circle.
 * Pulsates when connected, with tooltips showing status on hover.
 * Loading states (connecting/reconnecting) are delayed by 1 second to avoid flicker.
 */

import { useState, useEffect } from 'react';
import { useWebSocketStatus } from '../contexts/websocket_context';
import type { ConnectionStatus as Status } from '../services/websocket';

const statusConfig: Record<
  Status,
  { tooltip: string; bgColor: string; animate: boolean; clickable: boolean }
> = {
  connected: {
    tooltip: 'Live updates active',
    bgColor: 'bg-green-500',
    animate: true,
    clickable: false,
  },
  connecting: {
    tooltip: 'Connecting to server...',
    bgColor: 'bg-yellow-500',
    animate: true,
    clickable: false,
  },
  disconnected: {
    tooltip: 'Disconnected - click to reconnect',
    bgColor: 'bg-red-500',
    animate: false,
    clickable: true,
  },
  reconnecting: {
    tooltip: 'Reconnecting...',
    bgColor: 'bg-orange-500',
    animate: true,
    clickable: false,
  },
};

const LOADING_DELAY_MS = 1000;

export function ConnectionStatus() {
  const { status, reconnect } = useWebSocketStatus();
  const [displayStatus, setDisplayStatus] = useState<Status | null>(null);

  useEffect(() => {
    const isLoadingState = status === 'connecting' || status === 'reconnecting';

    if (isLoadingState) {
      // Delay showing loading states by 1 second
      const timer = setTimeout(() => {
        setDisplayStatus(status);
      }, LOADING_DELAY_MS);

      return () => clearTimeout(timer);
    } else {
      // Show resolved states immediately
      setDisplayStatus(status);
    }
  }, [status]);

  // Don't render anything until we have a resolved state or loading delay has passed
  if (displayStatus === null) {
    return null;
  }

  const config = statusConfig[displayStatus];

  return (
    <button
      type="button"
      onClick={() => config.clickable && reconnect()}
      disabled={!config.clickable}
      className={`relative flex items-center justify-center p-2 rounded-full transition-colors ${
        config.clickable
          ? 'hover:bg-gray-100 dark:hover:bg-gray-800 cursor-pointer'
          : 'cursor-default'
      }`}
      title={config.tooltip}
      aria-label={config.tooltip}
    >
      {/* Pulsating ring for connected state */}
      {config.animate && (
        <span
          className={`absolute h-3 w-3 rounded-full ${config.bgColor} opacity-75 animate-ping`}
        />
      )}
      {/* Solid circle */}
      <span className={`relative h-2.5 w-2.5 rounded-full ${config.bgColor}`} />
    </button>
  );
}
