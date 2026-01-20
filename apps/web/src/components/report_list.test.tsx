import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter } from 'react-router-dom';
import { ReportList } from './report_list';

// Mock the API module
vi.mock('../services/api', () => ({
  useReports: vi.fn(),
  useClientConfig: vi.fn(() => ({ data: { base_url: 'http://localhost:8080' } })),
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
            status: 'complete' as const,
            framework: 'playwright' as const,
            expected_jobs: 5,
            jobs_complete: 5,
            github_metadata: {
              branch: 'main',
              pr_number: 123,
            },
          },
        ],
        pagination: { page: 1, limit: 100, total: 1, total_pages: 1 },
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    // Framework name (multiple elements due to mobile/desktop layouts)
    expect(screen.getAllByText('Playwright').length).toBeGreaterThan(0);
    // Jobs progress (5/5 appears in both layouts)
    expect(screen.getAllByText('5/5').length).toBeGreaterThan(0);
    // PR number
    expect(screen.getAllByText('#123').length).toBeGreaterThan(0);
  });

  it('renders Cypress report cards with framework indicator', () => {
    vi.mocked(useReports).mockReturnValue({
      data: {
        reports: [
          {
            id: 'cypress-report-123456789abc',
            created_at: '2026-01-10T14:00:00Z',
            status: 'complete' as const,
            framework: 'cypress' as const,
            expected_jobs: 10,
            jobs_complete: 8,
            github_metadata: {
              branch: 'develop',
            },
          },
        ],
        pagination: { page: 1, limit: 100, total: 1, total_pages: 1 },
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    // Framework name - Cypress (multiple elements due to mobile/desktop layouts)
    expect(screen.getAllByText('Cypress').length).toBeGreaterThan(0);
    // Jobs progress (incomplete - 8/10)
    expect(screen.getAllByText('8/10').length).toBeGreaterThan(0);
  });

  it('shows pagination when multiple pages', () => {
    vi.mocked(useReports).mockReturnValue({
      data: {
        reports: Array(100)
          .fill(null)
          .map((_, i) => ({
            id: `report-${i}`,
            created_at: '2026-01-10T12:00:00Z',
            status: 'complete' as const,
            framework: 'playwright' as const,
            expected_jobs: 1,
            jobs_complete: 1,
          })),
        pagination: { page: 1, limit: 100, total: 150, total_pages: 2 },
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReports>);

    render(<ReportList />, { wrapper: createWrapper() });

    expect(screen.getByText(/showing 1 to 100 of 150/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument();
  });
});
