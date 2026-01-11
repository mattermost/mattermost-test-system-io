import { BrowserRouter, Routes, Route } from "react-router-dom";
import { HomePage } from "./pages/home_page";
import { ReportPage } from "./pages/report_page";
import { ThemeProvider } from "./contexts/theme_context";
import { ThemeToggle } from "./components/theme_toggle";

export function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <div className="min-h-screen bg-gray-50 dark:bg-gray-900">
          <header className="border-b border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
            <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
              <div className="flex items-center justify-between py-4">
                <h1 className="text-xl font-semibold text-gray-900 dark:text-white">
                  <a
                    href="/"
                    className="hover:text-gray-700 dark:hover:text-gray-300"
                  >
                    Rust Report Viewer
                  </a>
                </h1>
                <ThemeToggle />
              </div>
            </div>
          </header>
          <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
            <Routes>
              <Route path="/" element={<HomePage />} />
              <Route path="/reports" element={<HomePage />} />
              <Route path="/reports/:id" element={<ReportPage />} />
            </Routes>
          </main>
        </div>
      </BrowserRouter>
    </ThemeProvider>
  );
}
