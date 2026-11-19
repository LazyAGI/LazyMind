import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import App from './App';

vi.mock('./router', async () => {
  const { lazy, Suspense } = await import('react');
  const StalePage = lazy(() => Promise.reject(
    new TypeError('Failed to fetch dynamically imported module: /assets/CloudDocumentsLayout-old.js'),
  ));
  return {
    default: () => <Suspense fallback={null}><StalePage /></Suspense>,
  };
});
vi.mock('./runtime/managedBrowser', () => ({ startManagedBrowserSync: () => undefined }));
vi.mock('./modules/notifications/browser', () => ({ startBrowserNotifications: () => undefined }));

it('offers retry when an outdated page cannot load its route bundle', async () => {
  const log = vi.spyOn(console, 'error').mockImplementation(() => {});
  try {
    render(<App />);
    expect(await screen.findByRole('button', { name: /重试|retry/i })).toBeVisible();
    expect(screen.queryByText(/CloudDocumentsLayout-old/)).not.toBeInTheDocument();
  } finally {
    log.mockRestore();
  }
});
