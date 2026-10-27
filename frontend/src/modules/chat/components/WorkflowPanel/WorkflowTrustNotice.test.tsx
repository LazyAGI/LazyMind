import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { WorkflowTrustNotice } from './WorkflowTrustNotice';

const api = vi.hoisted(() => ({ fetch: vi.fn(), patch: vi.fn() }));
vi.mock('@/modules/user/uiPreferencesApi', () => ({
  fetchUserUiPreferences: api.fetch,
  patchUserUiPreferences: api.patch,
  USER_UI_PREFERENCES_CHANGED_EVENT: 'test:preferences-changed',
}));
const closeName = '关闭提示，以后不再显示';
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  api.fetch.mockReset().mockResolvedValue({ workflow_trust_notice_dismissed: false });
  api.patch.mockReset().mockResolvedValue({ workflow_trust_notice_dismissed: true });
});
afterEach(cleanup);

it('persists dismissal and stays hidden when the panel is mounted again', async () => {
  const view = render(<WorkflowTrustNotice />);
  fireEvent.click(await screen.findByRole('button', { name: closeName }));
  await waitFor(() => expect(screen.queryByRole('note')).not.toBeInTheDocument());
  expect(api.patch).toHaveBeenCalledTimes(1);
  expect(api.patch).toHaveBeenCalledWith({ workflow_trust_notice_dismissed: true });
  view.unmount();
  api.fetch.mockResolvedValue({ workflow_trust_notice_dismissed: true });
  render(<WorkflowTrustNotice />);
  await waitFor(() => expect(api.fetch).toHaveBeenCalledTimes(2));
  expect(screen.queryByRole('note')).not.toBeInTheDocument();
});

it('waits for saving and prevents duplicate submissions', async () => {
  let finish!: (value: unknown) => void;
  api.patch.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  render(<WorkflowTrustNotice />);
  const close = await screen.findByRole('button', { name: closeName });
  fireEvent.click(close);
  fireEvent.click(close);
  expect(close).toBeDisabled();
  expect(screen.getByRole('note')).toBeVisible();
  expect(api.patch).toHaveBeenCalledTimes(1);
  await act(async () => finish({ workflow_trust_notice_dismissed: true }));
  expect(screen.queryByRole('note')).not.toBeInTheDocument();
});

it.each(['network', 'unsupported'])('keeps the notice retryable after a %s save failure', async failure => {
  if (failure === 'network') api.patch.mockRejectedValueOnce(new Error('offline'));
  else api.patch.mockResolvedValueOnce({});
  render(<WorkflowTrustNotice />);
  fireEvent.click(await screen.findByRole('button', { name: closeName }));
  expect(await screen.findByRole('alert')).toHaveTextContent('未能保存关闭设置，请重试。');
  fireEvent.click(screen.getByRole('button', { name: closeName }));
  await waitFor(() => expect(screen.queryByRole('note')).not.toBeInTheDocument());
});

it('synchronizes mounted notices without letting an older load restore them', async () => {
  let finish!: (value: unknown) => void;
  api.fetch.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  render(<><WorkflowTrustNotice /><WorkflowTrustNotice /></>);
  act(() => window.dispatchEvent(new CustomEvent('test:preferences-changed', {
    detail: { workflow_trust_notice_dismissed: true },
  })));
  await act(async () => finish({ workflow_trust_notice_dismissed: false }));
  expect(screen.queryByRole('note')).not.toBeInTheDocument();
});

it('shows the notice when loading fails and reads the new account preference on account change', async () => {
  api.fetch.mockRejectedValueOnce(new Error('offline'));
  render(<WorkflowTrustNotice />);
  expect(await screen.findByRole('button', { name: closeName })).toBeEnabled();
  api.fetch.mockResolvedValue({ workflow_trust_notice_dismissed: true });
  act(() => window.dispatchEvent(new Event('lazymind:user-change')));
  await waitFor(() => expect(api.fetch).toHaveBeenCalledTimes(2));
  expect(screen.queryByRole('note')).not.toBeInTheDocument();
});
