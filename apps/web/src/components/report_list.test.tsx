import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter } from 'react-router-dom';
import { ReportList } from './report_list';

// Mock the API module
vi.mock('../services/api', () => ({
  useReports: vi.fn(),
}));

import { useReports } from '../services/api';

const createWrapper = () => {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });

  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>{children}</BrowserRouter>
    </QueryClientProvider>
  );
};

describe('ReportList', () => {
  it('shows loading state while fetching', () => {
    vi.mocked(useReports).mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
    } as ReturnType<typeof useReports>);

    const { container } = render(<ReportList />, { wrapper: createWrapper() });

    // Loader2 icon is rendered with animate-spin class
    expect(container.querySelector('.animate-spin')).toBeInTheDocument();
  });

  it('shows error state when fetch fails', () => {
    vi.mocked(useReports).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('Failed to fetch'),
    } as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    expect(screen.getByText(/error loading reports/i)).toBeInTheDocument();
  });

  it('shows empty state when no reports', () => {
    vi.mocked(useReports).mockReturnValue({
      data: {
        reports: [],
        pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    expect(screen.getByText(/no reports yet/i)).toBeInTheDocument();
  });

  it('renders report cards when reports exist', () => {
    vi.mocked(useReports).mockReturnValue({
      data: {
        reports: [
          {
            id: '123e4567-e89b-12d3-a456-426614174000',
            created_at: '2026-01-10T12:00:00Z',
            extraction_status: 'completed' as const,
            framework: 'playwright',
            framework_version: '1.57.0',
            stats: {
              start_time: '2026-01-10T11:00:00Z',
              duration_ms: 60000,
              expected: 10,
              skipped: 2,
              unexpected: 1,
              flaky: 0,
            },
          },
        ],
        pagination: { page: 1, limit: 100, total: 1, total_pages: 1 },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    // Report ID (first 8 chars)
    expect(screen.getByText('123e4567')).toBeInTheDocument();
    // Framework info
    expect(screen.getByText(/playwright v1\.57\.0/i)).toBeInTheDocument();
    // Stats - now displayed as just numbers with icons
    expect(screen.getByText('10')).toBeInTheDocument(); // passed count
    // Failed count "1" appears twice (row number + failed count), use getAllByText
    expect(screen.getAllByText('1').length).toBeGreaterThanOrEqual(1);
  });

  it('renders Cypress report cards with framework indicator', () => {
    vi.mocked(useReports).mockReturnValue({
      data: {
        reports: [
          {
            id: 'cypress-report-123456789abc',
            created_at: '2026-01-10T14:00:00Z',
            extraction_status: 'completed' as const,
            framework: 'cypress',
            framework_version: '13.7.0',
            stats: {
              start_time: '2026-01-10T13:30:00Z',
              duration_ms: 45000,
              expected: 25,
              skipped: 3,
              unexpected: 2,
              flaky: 0,
            },
          },
        ],
        pagination: { page: 1, limit: 100, total: 1, total_pages: 1 },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    // Report ID (first 8 chars)
    expect(screen.getByText('cypress-')).toBeInTheDocument();
    // Framework info - Cypress with version
    expect(screen.getByText(/cypress v13\.7\.0/i)).toBeInTheDocument();
    // Stats
    expect(screen.getByText('25')).toBeInTheDocument(); // passed count
    expect(screen.getByText('2')).toBeInTheDocument(); // failed count
  });

  it('shows pagination when multiple pages', () => {
    vi.mocked(useReports).mockReturnValue({
      data: {
        reports: Array(100)
          .fill(null)
          .map((_, i) => ({
            id: `report-${i}`,
            created_at: '2026-01-10T12:00:00Z',
            extraction_status: 'completed' as const,
          })),
        pagination: { page: 1, limit: 100, total: 150, total_pages: 2 },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    expect(screen.getByText(/showing 1 to 100 of 150/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument();
  });
});
