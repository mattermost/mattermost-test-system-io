import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { DivergenceBadge } from '@/components/orchestration/divergence_badge';

describe('DivergenceBadge', () => {
  it('renders both source statuses verbatim', () => {
    render(<DivergenceBadge orchestrationStatus="failed" artifactStatus="passed" />);
    const badge = screen.getByRole('status');
    expect(badge).toHaveTextContent(/Orchestration:\s*failed/);
    expect(badge).toHaveTextContent(/Artifacts:\s*passed/);
  });

  it('exposes a tooltip referencing both sources', () => {
    render(<DivergenceBadge orchestrationStatus="passed" artifactStatus="failed" />);
    const badge = screen.getByRole('status');
    const title = badge.getAttribute('title') ?? '';
    expect(title).toMatch(/Orchestration recorded "passed"/);
    expect(title).toMatch(/artifacts say "failed"/);
  });

  it('keeps the labels distinct when both statuses agree on label text but differ', () => {
    // Sanity check that both fields render even for non-pass/fail statuses.
    render(<DivergenceBadge orchestrationStatus="timedOut" artifactStatus="failed" />);
    const badge = screen.getByRole('status');
    expect(badge).toHaveTextContent(/Orchestration:\s*timedOut/);
    expect(badge).toHaveTextContent(/Artifacts:\s*failed/);
  });
});
