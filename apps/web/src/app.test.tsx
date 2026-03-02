import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter } from 'react-router-dom';
import { describe, it, expect } from 'vitest';
import { HomePage } from './pages/home_page';

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>{children}</BrowserRouter>
    </QueryClientProvider>
  );
}

describe('HomePage', () => {
  it('renders the view toggle', () => {
    render(<HomePage />, { wrapper: createWrapper() });
    expect(screen.getByText('Grouped')).toBeInTheDocument();
    expect(screen.getByText('Individual')).toBeInTheDocument();
  });
});
