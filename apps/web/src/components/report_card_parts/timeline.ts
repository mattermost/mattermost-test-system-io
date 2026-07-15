/**
 * formatTimeline shapes the orchestration timeline (begin → first test →
 * first retest → last test) for the report-summary header. When every
 * non-null moment falls on the same calendar date, it returns a single
 * date headline plus time-only segments; otherwise it returns no
 * headline and each segment carries its own date+time so a cross-day
 * run reads unambiguously.
 *
 * Pure function — exported separately from the JSX component so it's
 * unit-testable without a render harness.
 */

export interface TimelineInput {
  beginAt?: string | null;
  firstTestAt?: string | null;
  firstRetestAt?: string | null;
  lastTestAt?: string | null;
}

export type TimelineSegmentKind = 'begin' | 'firstTest' | 'firstRetest' | 'lastTest';

export interface TimelineSegment {
  kind: TimelineSegmentKind;
  /** Human label rendered before the timestamp ("Begin", "First test", …). */
  label: string;
  /** Time-only or date-time text, depending on whether all segments share a date. */
  text: string;
}

export interface FormattedTimeline {
  /** Date headline shown once at the front when every segment falls on it; null when segments span multiple days. */
  headline: string | null;
  /** Ordered chronologically: begin → first test → first retest (when present) → last test (when present). */
  segments: TimelineSegment[];
  /** True when at least `beginAt` resolved into a segment. */
  hasContent: boolean;
}

const SEGMENT_LABELS: Record<TimelineSegmentKind, string> = {
  begin: 'Begin',
  firstTest: 'First test',
  firstRetest: 'First retest',
  lastTest: 'Last test',
};

interface RawSegment {
  kind: TimelineSegmentKind;
  date: Date;
}

export function formatTimeline(input: TimelineInput): FormattedTimeline {
  const raw: RawSegment[] = [];
  const push = (kind: TimelineSegmentKind, iso?: string | null): void => {
    if (!iso) return;
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return;
    raw.push({ kind, date });
  };
  push('begin', input.beginAt);
  push('firstTest', input.firstTestAt);
  push('firstRetest', input.firstRetestAt);
  push('lastTest', input.lastTestAt);

  if (raw.length === 0) {
    return { headline: null, segments: [], hasContent: false };
  }

  const sameDay = raw.every((s) => sameLocalDate(s.date, raw[0]!.date));

  if (sameDay) {
    return {
      headline: formatDateHeadline(raw[0]!.date),
      segments: raw.map((s) => ({
        kind: s.kind,
        label: SEGMENT_LABELS[s.kind],
        text: formatTimeOnly(s.date),
      })),
      hasContent: true,
    };
  }

  return {
    headline: null,
    segments: raw.map((s) => ({
      kind: s.kind,
      label: SEGMENT_LABELS[s.kind],
      text: formatDateAndTime(s.date),
    })),
    hasContent: true,
  };
}

function sameLocalDate(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

function formatDateHeadline(d: Date): string {
  return d.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

function formatTimeOnly(d: Date): string {
  return d.toLocaleTimeString(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  });
}

function formatDateAndTime(d: Date): string {
  return d.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}
