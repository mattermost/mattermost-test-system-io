import { describe, expect, it } from 'vitest';
import { formatTimeline } from './timeline';

// All assertions use the literal timezone-shifted text the helper would
// emit on the test runner. To keep the suite stable across CI hosts we
// pin to UTC inputs and assert against shape (segment ordering, headline
// presence) rather than locale-formatted strings, except where we
// regex-match the expected family.

describe('formatTimeline', () => {
  it('returns empty when nothing is provided', () => {
    const out = formatTimeline({});
    expect(out.hasContent).toBe(false);
    expect(out.segments).toEqual([]);
    expect(out.headline).toBeNull();
  });

  it('renders begin only when no further moments are known', () => {
    const out = formatTimeline({ beginAt: '2026-05-06T11:50:00Z' });
    expect(out.hasContent).toBe(true);
    expect(out.segments.map((s) => s.kind)).toEqual(['begin']);
    expect(out.headline).not.toBeNull();
  });

  it('preserves chronological order including retest', () => {
    const out = formatTimeline({
      beginAt: '2026-05-06T11:50:00Z',
      firstTestAt: '2026-05-06T11:55:00Z',
      firstRetestAt: '2026-05-06T12:08:00Z',
      lastTestAt: '2026-05-06T12:09:00Z',
    });
    expect(out.segments.map((s) => s.kind)).toEqual([
      'begin',
      'firstTest',
      'firstRetest',
      'lastTest',
    ]);
  });

  it('omits firstRetest when not provided', () => {
    const out = formatTimeline({
      beginAt: '2026-05-06T11:50:00Z',
      firstTestAt: '2026-05-06T11:55:00Z',
      lastTestAt: '2026-05-06T12:09:00Z',
    });
    expect(out.segments.map((s) => s.kind)).toEqual(['begin', 'firstTest', 'lastTest']);
  });

  it('emits a single date headline when all moments share a calendar day (UTC noon)', () => {
    // 12:00 UTC lands on the same calendar day in nearly every timezone
    // the test runner could possibly use, so the same-day branch is
    // exercised reliably.
    const out = formatTimeline({
      beginAt: '2026-05-06T12:00:00Z',
      firstTestAt: '2026-05-06T12:05:00Z',
      lastTestAt: '2026-05-06T12:10:00Z',
    });
    expect(out.headline).not.toBeNull();
    // Time-only segments should NOT contain month names; date headline is rendered separately.
    for (const s of out.segments) {
      expect(s.text).not.toMatch(/\b(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\b/);
    }
  });

  it('drops the headline and prefixes each segment with date when moments span multiple days', () => {
    const out = formatTimeline({
      beginAt: '2026-05-06T12:00:00Z',
      firstTestAt: '2026-05-07T12:00:00Z',
      lastTestAt: '2026-05-07T13:00:00Z',
    });
    expect(out.headline).toBeNull();
    // Each segment carries date+time, so a month abbreviation should appear.
    for (const s of out.segments) {
      expect(s.text).toMatch(/\b(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\b/);
    }
  });

  it('skips invalid timestamps gracefully', () => {
    const out = formatTimeline({
      beginAt: 'not-a-date',
      firstTestAt: '2026-05-06T12:00:00Z',
    });
    expect(out.segments.map((s) => s.kind)).toEqual(['firstTest']);
  });

  it('labels segments with their human form', () => {
    const out = formatTimeline({
      beginAt: '2026-05-06T12:00:00Z',
      firstTestAt: '2026-05-06T12:05:00Z',
      firstRetestAt: '2026-05-06T12:08:00Z',
      lastTestAt: '2026-05-06T12:10:00Z',
    });
    expect(out.segments.map((s) => s.label)).toEqual([
      'Begin',
      'First test',
      'First retest',
      'Last test',
    ]);
  });
});
