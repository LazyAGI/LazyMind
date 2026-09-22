import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { Modal, type ModalFuncProps } from 'antd';
import { TerminalConnectionPage } from '@/modules/channelGateway';
import type { ChannelAccount } from '@/modules/channelGateway/api';
import RuleEditor from './RuleEditor';

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), detail: vi.fn(), refs: vi.fn(), archive: vi.fn(), rename: vi.fn(), groups: vi.fn(), targets: vi.fn(), create: vi.fn(), cancel: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/modules/channelGateway/api')>(),
  listChannelAccounts: mocks.accounts, archiveChannelAccount: mocks.archive, renameChannelAccount: mocks.rename, createConnectionSession: mocks.create, cancelConnectionSession: mocks.cancel,
}));
vi.mock('./api', async importOriginal => ({ ...await importOriginal<typeof import('./api')>(),
  getAccountDetail: mocks.detail, getReferences: mocks.refs, getGroups: mocks.groups, getTargets: mocks.targets }));
vi.mock('react-i18next', async importOriginal => {
  const t = (key: string) => key;
  return { ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t }) };
});

const original = { id: 'ca-original', provider: 'feishu', label: 'Work assistant', status: 'disconnected',
  binding_status: 'unbound', runtime_status: 'stopped', updated_at: '2026-09-18',
  identity: { app_id: 'cli_original', authorized_name: 'Alice', authorized_id: 'ou_original' } } as ChannelAccount;
let rows: ChannelAccount[];
beforeEach(() => {
  vi.clearAllMocks(); mocks.groups.mockResolvedValue({ items: [], next_cursor: '' }); rows = [{ ...original }];
  mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: p === 'feishu' ? [...rows] : [] }));
  mocks.detail.mockImplementation((id: string) => Promise.resolve({ ...rows.find(row => row.id === id), primary_recipient: null, notification_reference_count: 1 }));
  mocks.refs.mockResolvedValue({ items: [{ id: 'task', kind: 'schedule', name: 'Daily summary', enabled: true }], total: 1, next_cursor: '' });
  mocks.archive.mockImplementation((id: string) => { rows = rows.filter(row => row.id !== id); return Promise.resolve(); });
  mocks.rename.mockImplementation((id: string, label: string) => { rows = rows.map(row => row.id === id ? { ...row, label } : row); return Promise.resolve(rows[0]); });
  mocks.targets.mockResolvedValue({ items: [], next_cursor: '' }); mocks.cancel.mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
async function mount() {
  render(<MemoryRouter><TerminalConnectionPage initialProvider="feishu" /></MemoryRouter>);
  const label = await screen.findAllByText(original.label);
  const disclosure = label[0].closest('details')!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
  await waitFor(() => expect(within(disclosure).getByRole('button', { name: rows[0].status === 'connected' ? 'notifications.disconnect' : 'notifications.reconnect' })).toBeEnabled());
}

// The connection page intentionally uses robot names rather than authorization
// identities and no longer exposes a remark editor. Keep that UI contract intact.
it('shows the saved robot name and unbound state without authorization identifiers', async () => {
  await mount();
  expect(screen.getByText('notifications.unbound')).toBeVisible();
  expect(screen.getAllByText(original.label).length).toBeGreaterThan(0);
  for (const identity of ['Alice', 'cli_original', 'ou_original']) {
    expect(screen.queryByText(identity)).not.toBeInTheDocument();
  }
  expect(rows[0].identity).toEqual(original.identity);
});

it('preserves account identity without exposing the removed remark editor', async () => {
  await mount();
  expect(screen.queryByRole('button', { name: 'notifications.editAccountLabel' })).not.toBeInTheDocument();
  expect(screen.queryByRole('textbox', { name: 'notifications.accountRemark' })).not.toBeInTheDocument();
  expect(mocks.rename).not.toHaveBeenCalled();
  expect(mocks.archive).not.toHaveBeenCalled();
  expect(rows[0]).toEqual(original);
});

it('removes only the selected unbound record after showing impact confirmation', async () => {
  let confirmation: { onOk?: () => unknown; content?: unknown } | undefined;
  vi.spyOn(Modal, 'confirm').mockImplementation((config: ModalFuncProps) => { confirmation = config; return { destroy: vi.fn(), update: vi.fn() }; });
  rows.push({ ...original, id: 'second', label: 'Personal assistant' });
  await mount();
  const first = screen.getAllByText(original.label)[0].closest('details')!;
  fireEvent.click(within(first).getByRole('button', { name: 'notifications.removeAccount' }));
  await waitFor(() => expect(confirmation).toBeDefined());
  expect(mocks.refs).toHaveBeenCalledWith(original.id);
  expect(mocks.archive).not.toHaveBeenCalled();
  await act(async () => { await confirmation?.onOk?.(); });
  await waitFor(() => expect(screen.queryAllByText(original.label)).toHaveLength(0));
  expect(within(screen.getAllByText('Personal assistant')[0].closest('details')!.querySelector('summary')!).getByText('Personal assistant')).toBeVisible();
  expect(mocks.archive).toHaveBeenCalledWith(original.id);
});

it.each(['connected', 'paused'] as const)('does not offer record deletion while %s', async binding => {
  rows = [{ ...original, binding_status: binding, status: binding === 'connected' ? 'connected' : 'disconnected' }];
  await mount();
  expect(screen.queryByRole('button', { name: 'notifications.removeAccount' })).not.toBeInTheDocument();
});

it('blocks removal if the reference query fails', async () => {
  mocks.refs.mockRejectedValue(new Error('Unavailable'));
  const confirm = vi.spyOn(Modal, 'confirm');
  await mount();
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.removeAccount' }));
  await screen.findByRole('alert');
  expect(confirm).not.toHaveBeenCalled();
  expect(mocks.archive).not.toHaveBeenCalled();
});

it('selects same-name robots by account id without changing their display names', async () => {
  rows = [
    { ...original, label: 'Assistant', status: 'connected', binding_status: 'connected' },
    { ...original, id: 'second', label: 'Assistant', status: 'connected', binding_status: 'connected',
      identity: { app_id: 'cli_second', authorized_name: 'Bob', authorized_id: 'ou_second' } },
  ];
  const config = { events: { succeeded: { enabled: true, content: 'summary' as const },
    failed: { enabled: false, content: 'summary' as const }, waiting: { enabled: false, content: 'summary' as const } },
  channels: { feishu: { enabled: true, account_id: original.id } } };
  const onChange = vi.fn();
  render(<MemoryRouter><RuleEditor value={config} onChange={onChange} variant="task" /></MemoryRouter>);
  const select = await screen.findByRole('combobox', { name: 'notifications.account' });
  await waitFor(() => expect(select).not.toBeDisabled());
  fireEvent.mouseDown(select);
  // Ant Design renders virtualized option rows separately from its a11y list.
  await waitFor(() => expect(document.querySelectorAll('.ant-select-item-option')).toHaveLength(2));
  const options = document.querySelectorAll<HTMLElement>('.ant-select-item-option');
  expect(options[0]).toHaveTextContent('Assistant');
  expect(options[1]).toHaveTextContent('Assistant');
  fireEvent.click(options[1]);
  await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({
    ...config, channels: { feishu: { enabled: true, account_id: 'second' } },
  }));
  expect(screen.queryByText('Assistant · Bob · cli_second')).not.toBeInTheDocument();
});


it('does not restart reauthorization when the account list refreshes after success', async () => {
  const connected = { ...original, status: 'connected', binding_status: 'connected', runtime_status: 'running' } as ChannelAccount;
  mocks.create.mockResolvedValue({ id: 'reauthorize-session', provider: 'feishu', mode: 'qr_code', status: 'connected', allowed_actions: [], account: connected });
  await mount();

  fireEvent.click(screen.getByRole('button', { name: 'notifications.reauthorize' }));

  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
  const accountCallsAfterStart = mocks.accounts.mock.calls.length;
  await waitFor(() => expect(mocks.accounts.mock.calls.length).toBeGreaterThan(accountCallsAfterStart));
  expect(mocks.create).toHaveBeenCalledTimes(1);
});
