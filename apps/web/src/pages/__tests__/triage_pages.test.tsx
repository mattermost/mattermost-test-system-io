import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TriageHealthPage } from '@/pages/triage_health_page';
import { TriageVerdictPage } from '@/pages/triage_verdict_page';

function show(page: 'health' | 'verdicts/123') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/triage/${page}`]}>
        <Routes>
          <Route path="/triage/health" element={<TriageHealthPage />} />
          <Route path="/triage/verdicts/:id" element={<TriageVerdictPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
afterEach(() => vi.unstubAllGlobals());
describe('Triage pages', () => {
  it('shows trunk health and requests the selected classification', async () => {
    const fetcher = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              identity_id: 'test',
              repository: 'mattermost/mobile',
              framework: 'detox',
              file: 'a.js',
              full_title: 'MM-T1 works',
              mm_t_id: 'MM-T1',
              lane: 'ios',
              base_ref: 'main',
              classification: 'broken',
              instability_rate: 0.4,
              window_runs: 10,
              history: ['passed', 'failed'],
            },
          ],
          next_cursor: '',
        }),
        { status: 200 },
      ),
    );
    fetcher.mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            items: [
              {
                identity_id: 'test',
                repository: 'mattermost/mobile',
                framework: 'detox',
                file: 'a.js',
                full_title: 'MM-T1 works',
                lane: 'ios',
                base_ref: 'main',
                classification: 'broken',
                instability_rate: 0.4,
                window_runs: 10,
                history: ['passed', 'failed'],
              },
            ],
            next_cursor: '',
          }),
          { status: 200 },
        ),
      ),
    );
    vi.stubGlobal('fetch', fetcher);
    show('health');
    expect(await screen.findByRole('link', { name: 'MM-T1 works' })).toHaveAttribute(
      'href',
      '/triage/tests/test',
    );
    expect(screen.getByRole('img')).toHaveAccessibleName('Last 2 trunk runs: passed, failed');
    fireEvent.change(screen.getByLabelText('Classification'), { target: { value: 'broken' } });
    await waitFor(() =>
      expect(fetcher).toHaveBeenLastCalledWith(
        expect.stringContaining('classification=broken'),
        expect.any(Object),
      ),
    );
  });
  it('keeps human override visible and lists blocking findings first', async () => {
    const finding = (id: string, blocking: boolean) => ({
      identity_id: id,
      file: 'a.ts',
      full_title: id,
      lane: 'ios',
      class: blocking ? 'REGRESSION' : 'BROKEN_ON_TRUNK',
      blocking,
      reason: 'Evidence explanation',
      trunk: { runs: 10, fails: 2, flaky: 0, instability_rate: 0.25, consecutive_fails: 0 },
      pr: { fail_count: 1, error_excerpt: '', failure_locus: '', signature_match: true },
      links: { tsio_case_url: '/reports/g/group', tsio_history_url: `/triage/tests/${id}` },
    });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            id: '123',
            report_group_id: 'group',
            mode: 'enforce',
            verdict: 'FAILURE',
            confidence: 0.8,
            computed_at: '2026-09-16T00:00:00Z',
            engine_version: 'triage-1',
            counts: { passed: 10, failed: 2, exonerated: 1, blocking: 1, infra: 0 },
            findings: [finding('known issue', false), finding('new regression', true)],
            thresholds_used: { min_trunk_runs: 5 },
            human_override: {
              actor: 'reviewer',
              label: 'E2E/Verified',
              resulting_state: 'success',
              at: '2026-09-16T01:00:00Z',
            },
          }),
          { status: 200 },
        ),
      ),
    );
    show('verdicts/123');
    expect(await screen.findByText('Human override: success')).toBeInTheDocument();
    const headings = screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent);
    expect(headings).toEqual([
      'Blocking findings (1)',
      'Exonerated and neutral findings (1)',
      'Thresholds used',
    ]);
    expect(screen.getByText('FAILURE')).toBeInTheDocument();
  });
});
