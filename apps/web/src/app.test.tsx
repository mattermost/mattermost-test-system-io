import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect } from 'vitest';
import { HomePage } from './pages/home_page';

function createWrapper(initialEntry = '/') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
    </QueryClientProvider>
  );
}

describe('HomePage', () => {
  it('hides the Grouped/Individual toggle by default', () => {
    render(<HomePage />, { wrapper: createWrapper() });
    expect(screen.queryByText('Grouped')).not.toBeInTheDocument();
    expect(screen.queryByText('Individual')).not.toBeInTheDocument();
  });

  it('shows the Grouped/Individual toggle when ?individual=1 is set', () => {
    render(<HomePage />, { wrapper: createWrapper('/?individual=1') });
    expect(screen.getByText('Grouped')).toBeInTheDocument();
    expect(screen.getByText('Individual')).toBeInTheDocument();
  });
});
