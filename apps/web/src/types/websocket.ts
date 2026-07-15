/**
 * WebSocket event types matching the server's WsEvent enum.
 */

// Event type discriminators
export type WsEventType =
  | 'report_created'
  | 'report_updated'
  | 'report_registered'
  | 'report_entry_updated'
  | 'suites_available';

// Test statistics included in report_updated events
export interface TestStatsPayload {
  passed: number;
  failed: number;
  skipped: number;
  flaky?: number;
  total: number;
}

// Payload for report_created event
export interface ReportCreatedPayload {
  report_id: string;
  framework: string;

  repository?: string;
  ref?: string;
  sha?: string;
  actor?: string;
  run_id?: string;
  pr_number?: number;
  created_at: string;
}

// Payload for report_updated event
export interface ReportUpdatedPayload {
  report_id: string;
  status: string;
  completed_reports?: number;
  test_stats?: TestStatsPayload;
  updated_at: string;
}

// Payload for report_registered event
export interface ReportRegisteredPayload {
  report_group_id: string;
  report_id: string;
  display_name: string;
  gh_job_id?: string;
  gh_job_name?: string;
  status: string;
  created_at: string;
}

// Payload for report_entry_updated event
export interface ReportEntryUpdatedPayload {
  report_group_id: string;
  report_id: string;
  status: string;
  updated_at: string;
}

// Payload for suites_available event
export interface SuitesAvailablePayload {
  report_id: string;
  suite_count: number;
}

// Union type for all event payloads
export type WsEventPayload =
  | ReportCreatedPayload
  | ReportUpdatedPayload
  | ReportRegisteredPayload
  | ReportEntryUpdatedPayload
  | SuitesAvailablePayload;

// Individual event types
export interface ReportCreatedEvent {
  type: 'report_created';
  payload: ReportCreatedPayload;
  timestamp: string;
}

export interface ReportUpdatedEvent {
  type: 'report_updated';
  payload: ReportUpdatedPayload;
  timestamp: string;
}

export interface ReportRegisteredEvent {
  type: 'report_registered';
  payload: ReportRegisteredPayload;
  timestamp: string;
}

export interface ReportEntryUpdatedEvent {
  type: 'report_entry_updated';
  payload: ReportEntryUpdatedPayload;
  timestamp: string;
}

export interface SuitesAvailableEvent {
  type: 'suites_available';
  payload: SuitesAvailablePayload;
  timestamp: string;
}

// Union type for all WebSocket events
export type WsEventMessage =
  | ReportCreatedEvent
  | ReportUpdatedEvent
  | ReportRegisteredEvent
  | ReportEntryUpdatedEvent
  | SuitesAvailableEvent;
