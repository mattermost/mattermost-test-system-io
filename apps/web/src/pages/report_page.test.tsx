import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ReportPage } from './report_page';

// Mock the API module
vi.mock('../services/api', () => ({
  useReport: vi.fn(),
  useReportSuites: vi.fn(() => ({
    data: { suites: [] },
    isLoading: false,
    error: null,
  })),
  useSearchTestCases: vi.fn(() => ({
    data: { results: [] },
    isLoading: false,
    error: null,
  })),
  useClientConfig: vi.fn(() => ({
    data: { min_search_length: 3, upload_timeout_ms: 3600000, enable_html_view: true },
    isLoading: false,
    error: null,
  })),
  getReportHtmlUrl: vi.fn((id) => `/api/v1/reports/${id}/html`),
}));

import { useReport, useReportSuites } from '../services/api';

const createWrapper = (reportId: string) => {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });

  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[`/reports/${reportId}`]}>
        <Routes>
          <Route path="/reports/:id" element={children} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
};

describe('ReportPage', () => {
  const mockReportId = '123e4567-e89b-12d3-a456-426614174000';

  it('shows loading state while fetching', () => {
    vi.mocked(useReport).mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
    } as ReturnType<typeof useReport>);

    const { container } = render(<ReportPage />, {
      wrapper: createWrapper(mockReportId),
    });

    // Loader2 icon is rendered with animate-spin class
    expect(container.querySelector('.animate-spin')).toBeInTheDocument();
  });

  it('shows error state when report not found', () => {
    vi.mocked(useReport).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('Report not found'),
    } as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    expect(screen.getByText(/error loading report/i)).toBeInTheDocument();
  });

  it('renders report details when loaded with files', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T12:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        framework: 'playwright',
        framework_version: '1.57.0',
        has_files: true,
        stats: {
          start_time: '2026-01-10T11:00:00Z',
          duration_ms: 60000,
          expected: 10,
          skipped: 2,
          unexpected: 1,
          flaky: 0,
        },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    expect(screen.getByText('Report Details')).toBeInTheDocument();
    expect(screen.getByText(/Playwright v1\.57\.0/)).toBeInTheDocument();
    // Check tabs are present via navigation
    const nav = screen.getByRole('navigation', { name: 'Tabs' });
    expect(nav).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Test Results' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'HTML Report' })).toBeInTheDocument();
  });

  it('hides HTML Report tab when files have been deleted', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T12:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        framework: 'playwright',
        framework_version: '1.57.0',
        has_files: true,
        files_deleted_at: '2026-01-10T18:00:00Z',
        stats: {
          start_time: '2026-01-10T11:00:00Z',
          duration_ms: 60000,
          expected: 10,
          skipped: 2,
          unexpected: 1,
          flaky: 0,
        },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    expect(screen.getByText('Report Details')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Test Results' })).toBeInTheDocument();
    // HTML Report tab should not be present when files_deleted_at is set
    expect(screen.queryByRole('button', { name: 'HTML Report' })).not.toBeInTheDocument();
  });

  it('hides HTML Report tab when has_files is false', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T12:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        framework: 'playwright',
        framework_version: '1.57.0',
        has_files: false,
        stats: {
          start_time: '2026-01-10T11:00:00Z',
          duration_ms: 60000,
          expected: 10,
          skipped: 2,
          unexpected: 1,
          flaky: 0,
        },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    expect(screen.getByText('Report Details')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Test Results' })).toBeInTheDocument();
    // HTML Report tab should not be present when has_files is false
    expect(screen.queryByRole('button', { name: 'HTML Report' })).not.toBeInTheDocument();
  });

  it('renders iframe with HTML report URL when HTML tab is clicked', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T12:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        has_files: true,
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    // Click on HTML Report tab (find within navigation)
    const nav = screen.getByRole('navigation', { name: 'Tabs' });
    const htmlTabButton = nav.querySelector('button:nth-child(2)') as HTMLElement;
    fireEvent.click(htmlTabButton);

    const iframe = screen.getByTitle('HTML Report');
    expect(iframe).toBeInTheDocument();
    expect(iframe).toHaveAttribute('src', `/api/v1/reports/${mockReportId}/html`);
  });

  it('shows error message when extraction failed', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T12:00:00Z',
        extraction_status: 'failed' as const,
        error_message: 'Invalid JSON format',
        file_path: mockReportId,
        has_files: true,
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    expect(screen.getByText('Extraction Error')).toBeInTheDocument();
    expect(screen.getByText('Invalid JSON format')).toBeInTheDocument();
  });

  it('renders Cypress report details with framework indicator', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T14:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        framework: 'cypress',
        framework_version: '13.7.0',
        has_files: true,
        stats: {
          start_time: '2026-01-10T13:30:00Z',
          duration_ms: 45000,
          expected: 25,
          skipped: 3,
          unexpected: 2,
          flaky: 0,
        },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    expect(screen.getByText('Report Details')).toBeInTheDocument();
    // Framework info - Cypress with version (capitalized)
    expect(screen.getByText(/Cypress v13\.7\.0/)).toBeInTheDocument();
    // Tabs should be present
    expect(screen.getByRole('button', { name: 'Test Results' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'HTML Report' })).toBeInTheDocument();
  });

  it('renders Cypress report with test suites', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T14:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        framework: 'cypress',
        framework_version: '13.7.0',
        has_files: true,
        stats: {
          start_time: '2026-01-10T13:30:00Z',
          duration_ms: 45000,
          expected: 8,
          skipped: 1,
          unexpected: 1,
          flaky: 0,
        },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    vi.mocked(useReportSuites).mockReturnValue({
      data: {
        suites: [
          {
            id: 1,
            title: 'Login Flow',
            file_path: 'cypress/e2e/login.cy.ts',
            specs_count: 5,
            passed_count: 4,
            failed_count: 1,
          },
          {
            id: 2,
            title: 'Dashboard',
            file_path: 'cypress/e2e/dashboard.cy.ts',
            specs_count: 5,
            passed_count: 4,
            failed_count: 0,
            skipped_count: 1,
          },
        ],
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReportSuites>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    // Test suites section should be visible
    expect(screen.getByText('Test Suites (2)')).toBeInTheDocument();
    expect(screen.getByText('Login Flow')).toBeInTheDocument();
    expect(screen.getByText('Dashboard')).toBeInTheDocument();
  });

  it('shows test suites from database', () => {
    vi.mocked(useReport).mockReturnValue({
      data: {
        id: mockReportId,
        created_at: '2026-01-10T12:00:00Z',
        extraction_status: 'completed' as const,
        file_path: mockReportId,
        framework: 'playwright',
        framework_version: '1.57.0',
        has_files: true,
        stats: {
          start_time: '2026-01-10T11:00:00Z',
          duration_ms: 60000,
          expected: 10,
          skipped: 2,
          unexpected: 1,
          flaky: 0,
        },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof useReport>);

    vi.mocked(useReportSuites).mockReturnValue({
      data: {
        suites: [
          {
            id: 1,
            title: 'Login Tests',
            file_path: 'tests/login.spec.ts',
            specs_count: 5,
            passed_count: 4,
            failed_count: 1,
          },
        ],
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useReportSuites>);

    render(<ReportPage />, { wrapper: createWrapper(mockReportId) });

    // Test suites section should be visible
    expect(screen.getByText('Test Suites (1)')).toBeInTheDocument();
    expect(screen.getByText('Login Tests')).toBeInTheDocument();
  });
});
