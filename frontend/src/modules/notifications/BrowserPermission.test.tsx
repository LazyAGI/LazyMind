import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import BrowserPermission from './BrowserPermission';
const state = vi.hoisted(() => ({ supported: true }));
vi.mock('./browser', () => ({ browserNotificationsSupported: () => state.supported }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
let requestPermission: ReturnType<typeof vi.fn>;
beforeEach(() => {
  state.supported = true;
  requestPermission = vi.fn().mockResolvedValue('granted');
  vi.stubGlobal('Notification', { permission: 'default', requestPermission });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('requests permission only from a user gesture and displays its actual result', async () => {
  render(<BrowserPermission />);
  expect(requestPermission).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'notifications.browserAuthorize' }));
  await screen.findByText('notifications.browserPermission_granted');
  expect(requestPermission).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole('button')).toBeNull();
});
it('explains blocked permissions and detects site-setting changes on focus', async () => {
  Object.assign(Notification, { permission: 'denied' }); render(<BrowserPermission />);
  expect(screen.getByText('notifications.browserPermission_denied')).toBeInTheDocument();
  expect(screen.queryByRole('button')).toBeNull();
  Object.assign(Notification, { permission: 'granted' }); fireEvent(window, new Event('focus'));
  await screen.findByText('notifications.browserPermission_granted');
});
it('reports unsupported environments without prompting', () => {
  state.supported = false; render(<BrowserPermission />);
  expect(screen.getByText('notifications.browserUnsupported')).toBeInTheDocument();
  expect(requestPermission).not.toHaveBeenCalled();
});
it('keeps permission ungranted when the prompt is dismissed or fails', async () => {
  requestPermission.mockRejectedValue(new Error('permission unavailable')); render(<BrowserPermission />);
  fireEvent.click(screen.getByRole('button'));
  await waitFor(() => expect(screen.getByRole('button')).not.toHaveClass('ant-btn-loading'));
  expect(screen.getByText('notifications.browserPermission_default')).toBeInTheDocument();
});
