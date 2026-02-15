/**
 * WebSocket event types matching the server's WsEvent enum.
 */

// Event type discriminators
export type WsEventType =
  | 'report_created'
  | 'report_updated'
  | 'job_created'
  | 'job_updated'
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
  expected_jobs: number;
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
  completed_jobs?: number;
  test_stats?: TestStatsPayload;
  updated_at: string;
}

// Payload for job_created event
export interface JobCreatedPayload {
  report_id: string;
  job_id: string;
  display_name: string;
  github_job_id?: string;
  github_job_name?: string;
  status: string;
  created_at: string;
}

// Payload for job_updated event
export interface JobUpdatedPayload {
  report_id: string;
  job_id: string;
  status: string;
  html_url?: string;
  updated_at: string;
}

// Payload for suites_available event
export interface SuitesAvailablePayload {
  report_id: string;
  job_id: string;
  suite_count: number;
}

// Union type for all event payloads
export type WsEventPayload =
  | ReportCreatedPayload
  | ReportUpdatedPayload
  | JobCreatedPayload
  | JobUpdatedPayload
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

export interface JobCreatedEvent {
  type: 'job_created';
  payload: JobCreatedPayload;
  timestamp: string;
}

export interface JobUpdatedEvent {
  type: 'job_updated';
  payload: JobUpdatedPayload;
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
  | JobCreatedEvent
  | JobUpdatedEvent
  | SuitesAvailableEvent;
