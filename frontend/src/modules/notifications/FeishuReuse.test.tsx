import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { message, Modal, type ModalFuncProps } from 'antd';
import { CHANNEL_ACCOUNTS_CHANGED_EVENT } from '@/modules/channelGateway/api';
import { TerminalConnectionPage } from '@/modules/channelGateway';

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), create: vi.fn(), detail: vi.fn(), refs: vi.fn(),
  pause: vi.fn(), resume: vi.fn(), unbind: vi.fn(), cancel: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/modules/channelGateway/api')>(),
  listChannelAccounts: mocks.accounts, createConnectionSession: mocks.create,
  pauseChannelAccount: mocks.pause, resumeChannelAccount: mocks.resume,
  disconnectChannelAccount: mocks.unbind, cancelConnectionSession: mocks.cancel,
}));
vi.mock('./api', async importOriginal => ({ ...await importOriginal<typeof import('./api')>(),
  getAccountDetail: mocks.detail, getReferences: mocks.refs }));
vi.mock('react-i18next', async importOriginal => {
  const t = (key: string, values?: { count?: number }) => key === 'notifications.enabledCount' ? `${values?.count} accounts` : key;
  return { ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t }) };
});

const original = { id: 'existing-feishu', provider: 'feishu', label: 'My Feishu robot',
  status: 'connected', runtime_status: 'running', updated_at: '2026-09-18' };
function mount(status = 'connected') {
  const row = { ...original, status };
  mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [row] : [] }));
  mocks.detail.mockResolvedValue({ ...row, primary_recipient: null, notification_reference_count: 0 });
  return render(<MemoryRouter><TerminalConnectionPage initialProvider="feishu" /></MemoryRouter>);
}
async function expand() {
  await screen.findByText(original.label);
  const disclosure = document.querySelector('details')!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.refs.mockResolvedValue({ items: [], next_cursor: '', total: 0 });
  mocks.cancel.mockResolvedValue(undefined);
  mocks.pause.mockResolvedValue(undefined);
  mocks.resume.mockResolvedValue(original);
  mocks.unbind.mockResolvedValue(undefined);
  mocks.create.mockResolvedValue({ id: 'new-session', provider: 'feishu', mode: 'qr_code',
    status: 'connected', account: original, allowed_actions: [] });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it('ordinary Feishu disconnect pauses the account instead of erasing its credentials', async () => {
  let confirmation: { onOk?: () => unknown } | undefined;
  vi.spyOn(Modal, 'confirm').mockImplementation((config: ModalFuncProps) => { confirmation = config; return { destroy: vi.fn(), update: vi.fn() }; });
  mount(); await expand();
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.disconnect' }));
  await waitFor(() => expect(confirmation).toBeDefined());
  await act(async () => { await confirmation?.onOk?.(); });
  expect(mocks.pause).toHaveBeenCalledWith(original.id);
  expect(mocks.unbind).not.toHaveBeenCalled();
});

it('reconnect restores the selected paused robot without a QR registration', async () => {
  mount('disconnected'); await expand();
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.reconnect' }));
  await waitFor(() => expect(mocks.resume).toHaveBeenCalledWith(original.id, { silentError: true }));
  expect(mocks.create).not.toHaveBeenCalled();
});

it.each(['feishu', 'wechat', 'wecom'] as const)('waits for %s runtime readiness before reporting reconnect success', async provider => {
  const success = vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
  const info = vi.spyOn(message, 'info').mockImplementation(() => undefined as never);
  let row = { ...original, provider, status: 'disconnected', runtime_status: 'stopped' };
  mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: p === provider ? [row] : [] }));
  mocks.detail.mockImplementation(() => Promise.resolve({ ...row, notification_reference_count: 0 }));
  mocks.resume.mockImplementation(() => {
    row = { ...row, status: 'connected', runtime_status: 'starting' };
    return Promise.resolve(row);
  });
  render(<MemoryRouter><TerminalConnectionPage initialProvider={provider} /></MemoryRouter>);
  await expand();
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.reconnect' }));
  await waitFor(() => expect(info).toHaveBeenCalledWith('notifications.connectingHint'));
  expect(success).not.toHaveBeenCalled();
  expect(document.querySelector('.notification-connection-count')).toHaveTextContent('0 accounts');
  expect(screen.getAllByText('notifications.connecting').length).toBeGreaterThan(0);

  row = { ...row, runtime_status: 'running' };
  act(() => { window.dispatchEvent(new Event(CHANNEL_ACCOUNTS_CHANGED_EVENT)); });
  await waitFor(() => expect(success).toHaveBeenCalledWith('notifications.connected'));
  expect(document.querySelector('.notification-connection-count')).toHaveTextContent('1 accounts');
  act(() => { window.dispatchEvent(new Event(CHANNEL_ACCOUNTS_CHANGED_EVENT)); });
  await waitFor(() => expect(screen.getByRole('button', { name: 'notifications.disconnect' })).toBeVisible());
  expect(success).toHaveBeenCalledTimes(1);
});

it.each(['feishu', 'wechat', 'wecom'] as const)('never reports success when %s reconnect starts then fails', async provider => {
  const success = vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
  vi.spyOn(message, 'info').mockImplementation(() => undefined as never);
  const error = vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
  let row = { ...original, provider, status: 'disconnected', runtime_status: 'stopped' };
  mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: p === provider ? [row] : [] }));
  mocks.detail.mockImplementation(() => Promise.resolve({ ...row, notification_reference_count: 0 }));
  mocks.resume.mockImplementation(() => {
    row = { ...row, status: 'connected', runtime_status: 'starting' };
    return Promise.resolve(row);
  });
  render(<MemoryRouter><TerminalConnectionPage initialProvider={provider} /></MemoryRouter>);
  await expand();
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.reconnect' }));
  await waitFor(() => expect(screen.getAllByText('notifications.connecting').length).toBeGreaterThan(0));
  row = { ...row, runtime_status: 'failed' };
  act(() => { window.dispatchEvent(new Event(CHANNEL_ACCOUNTS_CHANGED_EVENT)); });
  await waitFor(() => expect(error).toHaveBeenCalledWith('notifications.connectionFailed'));
  expect(success).not.toHaveBeenCalled();
  expect(document.querySelector('.notification-connection-count')).toHaveTextContent('0 accounts');
});

it('a paused account only exposes reconnect in its card footer', async () => {
  mount('disconnected'); await expand();
  expect(await screen.findByRole('button', { name: 'notifications.reconnect' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'notifications.unbind' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'notifications.reauthorize' })).not.toBeInTheDocument();
});

it('shows an in-progress Feishu connection instead of offering a conflicting reconnect', async () => {
  let status = 'provisioning';
  mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [{ ...original, status }] : [] }));
  mocks.detail.mockImplementation(() => Promise.resolve({ ...original, status, notification_reference_count: 0 }));
  render(<MemoryRouter><TerminalConnectionPage initialProvider="feishu" /></MemoryRouter>);
  await screen.findByText(original.label);
  const disclosure = document.querySelector('details')!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
  expect(screen.getAllByText('notifications.connecting').length).toBeGreaterThan(0);
  expect(await within(disclosure).findByText('notifications.connectionInProgressHint')).toBeInTheDocument();
  expect(within(disclosure).queryByRole('button', { name: 'notifications.reconnect' })).not.toBeInTheDocument();
  expect(mocks.resume).not.toHaveBeenCalled();

  status = 'connected';
  await waitFor(() => expect(within(disclosure).getByRole('button', { name: 'notifications.disconnect' })).toBeInTheDocument(), { timeout: 4000 });
  expect(mocks.resume).not.toHaveBeenCalled();
});

it('keeps Feishu registration running when the connection page closes', async () => {
  mocks.create.mockResolvedValue({ id: 'active-registration', provider: 'feishu', mode: 'qr_code',
    status: 'waiting_scan', poll_after_ms: 1000, allowed_actions: ['cancel'] });
  const view = mount();
  fireEvent.click(await screen.findByRole('button', { name: /channelGateway.feishu.startScan/ }));
  await screen.findByText('channelGateway.feishu.sessionStatusMap.waiting_scan');
  view.unmount();
  expect(mocks.cancel).not.toHaveBeenCalled();
});

it('shows the QR connection design directly and preserves explicit new-robot intent', async () => {
  mount(); await screen.findByText(original.label);
  expect(mocks.create).not.toHaveBeenCalled();
  const scan = await screen.findByRole('button', { name: /channelGateway.feishu.startScan/ });
  fireEvent.click(scan);
  await waitFor(() => expect(mocks.create).toHaveBeenCalledWith('feishu', expect.objectContaining({ createNew: true })));
});

it('connected account cards only expose disconnect even when a return callback exists', async () => {
  const second = { ...original, id: 'second-feishu' };
  mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [original, second] : [] }));
  mocks.detail.mockResolvedValue({ ...original, primary_recipient: null, notification_reference_count: 0 });
  const useAccount = vi.fn();
  render(<MemoryRouter><TerminalConnectionPage initialProvider="feishu" onUseAccount={useAccount} /></MemoryRouter>);
  await waitFor(() => expect(screen.getAllByText(original.label)).toHaveLength(2));
  expect(useAccount).not.toHaveBeenCalled();
  const disclosure = document.querySelectorAll('details')[1]!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
  expect(within(disclosure).queryByRole('button', { name: 'notifications.useAccount' })).not.toBeInTheDocument();
  expect(within(disclosure).getByRole('button', { name: 'notifications.disconnect' })).toBeInTheDocument();
  expect(useAccount).not.toHaveBeenCalled();
  expect(mocks.create).not.toHaveBeenCalled();
});

it('missing credentials offer original-robot reauthorization after resume fails', async () => {
  mocks.resume.mockRejectedValueOnce({ response: { data: { error: { code: 'FEISHU_REAUTHORIZATION_REQUIRED' } } } });
  // Keep reauthorization pending until its UI and original-account request are observed.
  let finishReauthorization!: () => void;
  mocks.create.mockImplementationOnce(() => new Promise(resolve => {
    finishReauthorization = () => resolve({ id: 'reauthorized-session', provider: 'feishu', mode: 'qr_code',
      status: 'connected', account: original, allowed_actions: [] });
  }));
  mount('disconnected'); await expand();
  const reconnect = await screen.findByRole('button', { name: 'notifications.reconnect' });
  await act(async () => { fireEvent.click(reconnect); });
  await screen.findByText('notifications.reconnectPlatform');
  await waitFor(() => expect(mocks.create).toHaveBeenCalledWith('feishu', expect.objectContaining({
    accountId: original.id, reauthorize: true,
  })));
  expect(mocks.unbind).not.toHaveBeenCalled();
  await act(async () => { finishReauthorization(); });
  expect(screen.queryByText('notifications.reconnectPlatform')).not.toBeInTheDocument();
});

it('an unbound account can reauthorize the original robot without creating another app', async () => {
  const row = { ...original, status: 'disconnected', binding_status: 'unbound' };
  mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [row] : [] }));
  mocks.detail.mockResolvedValue({ ...row, primary_recipient: null, notification_reference_count: 0 });
  render(<MemoryRouter><TerminalConnectionPage initialProvider="feishu" /></MemoryRouter>);
  await expand();
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.reauthorize' }));
  fireEvent.click(await screen.findByRole('button', { name: /channelGateway.feishu.startScan/ }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledWith('feishu', expect.objectContaining({
    accountId: original.id, reauthorize: true,
  })));
  expect(mocks.create.mock.calls[0][1].createNew).toBeUndefined();
  expect(mocks.resume).not.toHaveBeenCalled();
});
