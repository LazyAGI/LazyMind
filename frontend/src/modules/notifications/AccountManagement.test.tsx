import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { Modal } from 'antd';
import { TerminalConnectionPage } from '@/modules/channelGateway';
import type { ChannelAccount } from '@/modules/channelGateway/api';
import RuleEditor from './RuleEditor';

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), detail: vi.fn(), refs: vi.fn(), archive: vi.fn(), rename: vi.fn(), groups: vi.fn(), targets: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/modules/channelGateway/api')>(),
  listChannelAccounts: mocks.accounts, archiveChannelAccount: mocks.archive, renameChannelAccount: mocks.rename,
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
  mocks.targets.mockResolvedValue({ items: [], next_cursor: '' });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
async function mount() {
  render(<MemoryRouter><TerminalConnectionPage initialProvider="feishu" /></MemoryRouter>);
  await screen.findByText(original.label);
  const disclosure = document.querySelector('details')!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
  await waitFor(() => expect(within(disclosure).getByRole('button', { name: 'notifications.editAccountLabel' })).toBeEnabled());
}

it('shows author, app and user identifiers after unbinding', async () => {
  await mount();
  expect(screen.getByText('notifications.unbound')).toBeVisible();
  expect(screen.getByText('Alice')).toBeVisible();
  expect(screen.getByText('cli_original')).toBeVisible();
  expect(screen.getByText('ou_original')).toBeVisible();
});

it('edits a remark without changing the selected identity', async () => {
  await mount();
  fireEvent.click(screen.getByRole('button', { name: 'notifications.editAccountLabel' }));
  fireEvent.change(await screen.findByRole('textbox', { name: 'notifications.accountRemark' }), { target: { value: 'Daily briefing' } });
  fireEvent.click(screen.getByRole('button', { name: 'common.save' }));
  await waitFor(() => expect(mocks.rename).toHaveBeenCalledWith(original.id, 'Daily briefing'));
  await screen.findByText('Daily briefing');
  expect(rows[0].identity?.app_id).toBe('cli_original');
  expect(mocks.archive).not.toHaveBeenCalled();
});

it('removes only the selected unbound record after showing impact confirmation', async () => {
  let confirmation: { onOk?: () => unknown; content?: unknown } | undefined;
  vi.spyOn(Modal, 'confirm').mockImplementation(config => { confirmation = config; return { destroy: vi.fn(), update: vi.fn() }; });
  rows.push({ ...original, id: 'second', label: 'Personal assistant' });
  await mount();
  const first = screen.getByText(original.label).closest('details')!;
  fireEvent.click(within(first).getByRole('button', { name: 'notifications.removeAccount' }));
  await waitFor(() => expect(confirmation).toBeDefined());
  expect(mocks.refs).toHaveBeenCalledWith(original.id);
  expect(mocks.archive).not.toHaveBeenCalled();
  await act(async () => { await confirmation?.onOk?.(); });
  await waitFor(() => expect(screen.queryByText(original.label)).not.toBeInTheDocument());
  expect(screen.getByText('Personal assistant')).toBeVisible();
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

it('shows distinct same-name robots in task notification account selection', async () => {
  rows = [
    { ...original, label: 'Assistant', status: 'connected', binding_status: 'connected' },
    { ...original, id: 'second', label: 'Assistant', status: 'connected', binding_status: 'connected',
      identity: { app_id: 'cli_second', authorized_name: 'Bob', authorized_id: 'ou_second' } },
  ];
  const config = { events: { succeeded: { enabled: true, content: 'summary' as const },
    failed: { enabled: false, content: 'summary' as const }, waiting: { enabled: false, content: 'summary' as const } },
  channels: { feishu: { enabled: true, account_id: original.id } } };
  render(<MemoryRouter><RuleEditor value={config} onChange={vi.fn()} variant="task" /></MemoryRouter>);
  const select = await screen.findByRole('combobox', { name: 'notifications.account' });
  fireEvent.mouseDown(select);
  await screen.findByText('Assistant · Bob · cli_second');
  expect(screen.getAllByText('Assistant · Alice · cli_original').length).toBeGreaterThan(0);
});
