import { BrowserRouter, Routes, Route, Link, useParams } from 'react-router-dom';
import { HomePage } from '@/pages/home_page';
import { FilteredReportPage } from '@/pages/filtered_report_page';
import { FilteredReportsPage } from '@/pages/filtered_reports_page';
import { CommitReportsPage } from '@/pages/commit_reports_page';
import { ReportDetailPage } from '@/pages/report_detail_page';
import { ThemeProvider } from '@/contexts/theme_context';
import { ThemeToggle } from '@/components/theme_toggle';
import { ConnectionStatus } from '@/components/connection_status';
import { LoginButton } from '@/components/login_button';
import { Footer } from '@/components/footer';

const SHA_RE = /^[0-9a-f]{7,40}$/i;

/**
 * For /reports/:param — resolves ambiguous single-segment paths.
 * SHA-like strings (7-40 hex) → CommitReportsPage
 * Everything else → FilteredReportsPage (repo name filter)
 */
function RepoOrShaResolver() {
  const { param } = useParams<{ param: string }>();
  if (param && SHA_RE.test(param)) {
    return <CommitReportsPage />;
  }
  return <FilteredReportsPage />;
}

export function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <div className="min-h-screen min-w-[480px] bg-gray-50 dark:bg-gray-900">
          <header className="border-b border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
            <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
              <div className="flex items-center justify-between py-4">
                <h1 className="text-xl font-semibold text-gray-900 dark:text-white">
                  <Link to="/" className="hover:text-gray-700 dark:hover:text-gray-300">
                    Mattermost Test System IO
                  </Link>
                </h1>
                <div className="flex items-center gap-3">
                  <ConnectionStatus />
                  <LoginButton />
                  <ThemeToggle />
                </div>
              </div>
            </div>
          </header>
          <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
            <Routes>
              <Route path="/" element={<HomePage />} />
              <Route path="/reports" element={<HomePage />} />
              {/* Explicit prefixed routes */}
              <Route path="/reports/r/:id" element={<ReportDetailPage />} />
              <Route path="/reports/g/:id" element={<ReportDetailPage />} />
              <Route path="/reports/c/:sha" element={<CommitReportsPage />} />
              {/* Consolidated report view */}
              <Route path="/reports/:repo/:branch/:commit/:name" element={<FilteredReportPage />} />
              {/* Filtered views by repo/branch/commit */}
              <Route path="/reports/:repo/:branch/:commit" element={<FilteredReportsPage />} />
              <Route path="/reports/:repo/:branch" element={<FilteredReportsPage />} />
              {/* Single segment: SHA → commit lookup, otherwise → repo filter */}
              <Route path="/reports/:param" element={<RepoOrShaResolver />} />
            </Routes>
          </main>
          <Footer />
        </div>
      </BrowserRouter>
    </ThemeProvider>
  );
}
