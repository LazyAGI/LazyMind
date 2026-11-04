import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { message, Modal, type ModalFuncProps } from 'antd';
import { TerminalConnectionPage } from '@/modules/channelGateway';
import { ChannelConnectionPage } from '@/modules/channelGateway/pages/ChannelConnectionPage';
import { channelAccountLabel, type ChannelAccount, type ConnectionSession } from '@/modules/channelGateway/api';
import RuleEditor from './RuleEditor';

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), detail: vi.fn(), refs: vi.fn(), archive: vi.fn(), rename: vi.fn(), groups: vi.fn(), targets: vi.fn(), create: vi.fn(), cancel: vi.fn(), resume: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/modules/channelGateway/api')>(),
  listChannelAccounts: mocks.accounts, archiveChannelAccount: mocks.archive, renameChannelAccount: mocks.rename, createConnectionSession: mocks.create, cancelConnectionSession: mocks.cancel, resumeChannelAccount: mocks.resume,
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
  vi.clearAllMocks();
  vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
  vi.spyOn(message, 'info').mockImplementation(() => undefined as never);
  vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
  mocks.groups.mockResolvedValue({ items: [], next_cursor: '' }); rows = [{ ...original }];
  mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: rows.filter(row => row.provider === p) }));
  mocks.detail.mockImplementation((id: string) => Promise.resolve({ ...rows.find(row => row.id === id), primary_recipient: null, notification_reference_count: 1 }));
  mocks.refs.mockResolvedValue({ items: [{ id: 'task', kind: 'schedule', name: 'Daily summary', enabled: true }], total: 1, next_cursor: '' });
  mocks.archive.mockImplementation((id: string) => { rows = rows.filter(row => row.id !== id); return Promise.resolve(); });
  mocks.rename.mockImplementation((id: string, label: string) => { rows = rows.map(row => row.id === id ? { ...row, label } : row); return Promise.resolve(rows.find(row => row.id === id)); });
  mocks.targets.mockResolvedValue({ items: [], next_cursor: '' }); mocks.cancel.mockResolvedValue(undefined);
  mocks.resume.mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
async function mount(provider: 'feishu' | 'wecom' = 'feishu') {
  render(<MemoryRouter><TerminalConnectionPage initialProvider={provider} /></MemoryRouter>);
  const label = await screen.findAllByText(channelAccountLabel(rows[0], rows));
  const disclosure = label[0].closest('details')!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
  await waitFor(() => expect(within(disclosure).getByRole('button', { name: rows[0].status === 'connected' ? 'notifications.disconnect' : 'notifications.reconnect' })).toBeEnabled());
}

// Feishu retains robot-name display without authorization identities or a
// remark editor; WeCom has its own remark and bot identity controls below.
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

it.each(['connected', 'paused'] as const)('offers record deletion while %s', async binding => {
  rows = [{ ...original, binding_status: binding, status: binding === 'connected' ? 'connected' : 'disconnected' }];
  await mount();
  expect(screen.getByRole('button', { name: 'notifications.removeAccount' })).toBeEnabled();
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
    { ...original, label: 'Assistant', status: 'connected', binding_status: 'connected', runtime_status: 'running' },
    { ...original, id: 'second', label: 'Assistant', status: 'connected', binding_status: 'connected', runtime_status: 'running',
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
  let completeSession!: (session: ConnectionSession) => void;
  mocks.create.mockReturnValue(new Promise<ConnectionSession>(resolve => { completeSession = resolve; }));
  await mount();

  fireEvent.click(screen.getByRole('button', { name: 'notifications.reauthorize' }));

  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
  const accountCallsAfterStart = mocks.accounts.mock.calls.length;
  // Complete authorization only after recording the baseline: an immediately
  // resolved session can refresh the accounts before waitFor above returns.
  await act(async () => {
    rows = [connected];
    completeSession({ id: 'reauthorize-session', provider: 'feishu', mode: 'qr_code', status: 'connected',
      revision: 1, message: '', qr: null, challenge: null, poll_after_ms: 0,
      allowed_actions: [], account: connected, error: null });
  });
  await waitFor(() => expect(mocks.accounts.mock.calls.length).toBeGreaterThan(accountCallsAfterStart));
  expect(await screen.findByRole('button', { name: 'notifications.disconnect' })).toBeEnabled();
  expect(mocks.create).toHaveBeenCalledTimes(1);
});

it('restores a connected WeCom robot with its original credentials without automatically creating another QR bot', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Team robot', status: 'connected', binding_status: 'connected', runtime_status: 'running' }];
  mocks.create.mockResolvedValue({ id: 'wecom-repair', provider: 'wecom', mode: 'credentials', status: 'expired',
    revision: 1, message: '', qr: null, challenge: null, poll_after_ms: 0, allowed_actions: [], account: null, error: null });
  await mount('wecom');
  const disclosure = screen.getAllByText('Team robot')[0].closest('details')!;
  expect(within(disclosure).getByRole('button', { name: 'notifications.wecomRepairAuthorization' })).toBeEnabled();
  expect(within(disclosure).getByText('notifications.wecomReauthorizeHint')).toBeVisible();
  expect(within(disclosure).queryByText(original.id, { exact: false })).not.toBeInTheDocument();

  fireEvent.click(within(disclosure).getByRole('button', { name: 'notifications.wecomRepairAuthorization' }));

  expect(await screen.findByRole('heading', { name: 'notifications.wecomRestorePermissionTitle' })).toBeVisible();
  expect(mocks.create).not.toHaveBeenCalled();
  expect(screen.getAllByRole('heading', { name: 'notifications.wecomRepairAuthorization' }).length).toBeGreaterThan(0);
  expect(screen.getByText('notifications.wecomReauthorizeStepHint')).toBeVisible();
  expect(screen.queryByText('channelGateway.wecom.stepScanHint')).not.toBeInTheDocument();
  expect(screen.queryByText('channelGateway.wecom.stepConfirmHint')).not.toBeInTheDocument();
  const restoreRegion = screen.getByRole('region', { name: 'notifications.wecomRestorePermissionTitle' });
  expect(await within(restoreRegion).findByText(
    'notifications.account：Team robot',
    undefined,
    { timeout: 5000 },
  )).toBeVisible();
  expect(screen.queryByRole('button', { name: /channelGateway.wecom.startScan/ })).not.toBeInTheDocument();
  const credentials = screen.getByText('notifications.wecomUpdateCredentials').closest('details')!;
  expect(screen.getByLabelText('notifications.wecomBotId')).not.toBeVisible();
  credentials.open = true; fireEvent(credentials, new Event('toggle'));
  const restore = screen.getByRole('button', { name: 'notifications.wecomRestoreConnection' });
  expect(restore).toBeDisabled();
  fireEvent.change(screen.getByLabelText('notifications.wecomBotId'), { target: { value: 'original-bot' } });
  fireEvent.change(screen.getByLabelText('notifications.wecomBotSecret'), { target: { value: 'original-secret' } });
  fireEvent.click(restore);
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
  expect(mocks.create).toHaveBeenCalledWith('wecom', expect.objectContaining({
    accountId: original.id, credentials: { bot_id: 'original-bot', secret: 'original-secret' },
  }));
  expect(mocks.create.mock.calls[0][1].reauthorize).toBeUndefined();
  expect(mocks.resume).not.toHaveBeenCalled();
});

it('starts restoration of the same WeCom robot when reconnect reports expired messaging permission', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Team robot' }];
  mocks.resume.mockRejectedValue({ response: { data: { error: { code: 'WECOM_CAPABILITY_REAUTH_REQUIRED' } } } });
  await mount('wecom');
  const disclosure = screen.getAllByText('Team robot')[0].closest('details')!;
  fireEvent.click(within(disclosure).getByRole('button', { name: 'notifications.reconnect' }));
  expect(await screen.findByRole('heading', { name: 'notifications.wecomRestorePermissionTitle' })).toBeVisible();
  expect(mocks.resume).toHaveBeenCalledWith(original.id, { silentError: true });
  expect(mocks.create).not.toHaveBeenCalled();
  expect(screen.getByText('notifications.wecomReauthorizeStepHint')).toBeVisible();
  expect(mocks.archive).not.toHaveBeenCalled();
});

it('keeps QR creation available when adding a new WeCom bot', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Team robot', status: 'connected' }];
  mocks.create.mockResolvedValue({ id: 'wecom-new', provider: 'wecom', mode: 'qr_code', status: 'expired',
    revision: 1, message: '', qr: null, challenge: null, poll_after_ms: 0, allowed_actions: [], account: null, error: null });
  await mount('wecom');
  fireEvent.click(screen.getByRole('button', { name: /channelGateway.wecom.startScan/ }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
  expect(mocks.create).toHaveBeenCalledWith('wecom', expect.objectContaining({ silentError: true }));
  expect(mocks.create.mock.calls[0][1].accountId).toBeUndefined();
  expect(mocks.create.mock.calls[0][1].credentials).toBeUndefined();
});

it('refreshes original WeCom connection after permission recovery without asking for credentials or creating a QR bot', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Team robot', status: 'connected', binding_status: 'connected', runtime_status: 'running' }];
  mocks.resume.mockResolvedValue(rows[0]);
  await mount('wecom');
  const disclosure = screen.getAllByText('Team robot')[0].closest('details')!;
  fireEvent.click(within(disclosure).getByRole('button', { name: 'notifications.wecomRepairAuthorization' }));
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.wecomRefreshAuthorization' }));
  await waitFor(() => expect(mocks.resume).toHaveBeenCalledWith(original.id, { silentError: true }));
  expect(mocks.targets).toHaveBeenCalledWith(original.id);
  expect(mocks.create).not.toHaveBeenCalled();
  expect(mocks.archive).not.toHaveBeenCalled();
  expect(screen.queryByRole('heading', { name: 'notifications.wecomRestorePermissionTitle' })).not.toBeInTheDocument();
});

it('renames only the selected WeCom account and shows its original full Bot ID without revealing a secret', async () => {
  rows = [
    { ...original, provider: 'wecom', label: 'Team robot', status: 'connected', binding_status: 'connected', runtime_status: 'running',
      identity: { bot_id: 'aibot_alpha_123456', bot_name: 'Team robot' } },
    { ...original, provider: 'wecom', id: 'ca-second', label: 'Team robot', status: 'connected', binding_status: 'connected', runtime_status: 'running',
      identity: { bot_id: 'aibot_beta_654321', bot_name: 'Team robot' } },
  ];
  await mount('wecom');
  const disclosure = screen.getAllByText(channelAccountLabel(rows[1], rows))[0].closest('details')!;
  disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
  const rename = within(disclosure).getByRole('button', { name: 'notifications.editAccountLabel' });
  await waitFor(() => expect(rename).toBeEnabled());
  expect(within(disclosure).getByText('aibot_beta_654321')).toBeVisible();
  expect(within(disclosure).queryByText('notifications.wecomBotSecret')).not.toBeInTheDocument();
  fireEvent.click(rename);
  const dialog = await screen.findByRole('dialog', { name: 'notifications.editAccountLabel' });
  expect(within(dialog).getByText('notifications.wecomAccountRemarkHint')).toBeInTheDocument();
  const input = within(dialog).getByRole('textbox', { name: 'notifications.accountRemark' });
  expect(input).toHaveAttribute('maxlength', '80');
  fireEvent.change(input, { target: { value: '  测试机器人  ' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'notifications.saveChanges' }));
  await waitFor(() => expect(mocks.rename).toHaveBeenCalledWith('ca-second', '测试机器人'));
  const updatedLabel = channelAccountLabel(rows[1], rows);
  const updatedSummary = (await screen.findAllByText(updatedLabel)).find(element => element.closest('summary'))!.closest('summary')!;
  expect(within(updatedSummary).getByText(updatedLabel)).toBeVisible();
  expect(rows[0].label).toBe('Team robot');
  expect(rows[1].identity?.bot_id).toBe('aibot_beta_654321');
  expect(mocks.create).not.toHaveBeenCalled();
  expect(mocks.archive).not.toHaveBeenCalled();
});

it('keeps WeCom permission recovery open when the original bot remains unauthorized after reconnecting', async () => {
  const account = { ...original, provider: 'wecom', label: 'Team robot', status: 'connected', binding_status: 'connected', runtime_status: 'running' } as ChannelAccount;
  rows = [account];
  mocks.resume.mockResolvedValue(account);
  mocks.targets.mockRejectedValue({ response: { data: { error: { code: 'WECOM_CAPABILITY_REAUTH_REQUIRED' } } } });
  const onConnected = vi.fn();
  const errorToast = vi.mocked(message.error);
  render(<MemoryRouter><ChannelConnectionPage provider="wecom" accountId={original.id} onConnected={onConnected} /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'notifications.wecomRefreshAuthorization' }));
  expect(await screen.findByText('notifications.WECOM_CAPABILITY_REAUTH_REQUIRED')).toBeVisible();
  expect(screen.getByRole('heading', { name: 'notifications.wecomRestorePermissionTitle' })).toBeVisible();
  expect(mocks.targets).toHaveBeenCalledWith(original.id);
  expect(onConnected).not.toHaveBeenCalled();
  expect(errorToast).not.toHaveBeenCalled();
  expect(mocks.create).not.toHaveBeenCalled();
});

it('waits for an original WeCom bot messaging permission check before completing recovery', async () => {
  const account = { ...original, provider: 'wecom', label: 'Team robot', status: 'connected', binding_status: 'connected', runtime_status: 'running' } as ChannelAccount;
  rows = [account];
  mocks.resume.mockResolvedValue(account);
  let finishPermissionCheck!: (result: { items: []; next_cursor: string }) => void;
  mocks.targets.mockReturnValue(new Promise(resolve => { finishPermissionCheck = resolve; }));
  const onConnected = vi.fn();
  render(<MemoryRouter><ChannelConnectionPage provider="wecom" accountId={original.id} onConnected={onConnected} /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'notifications.wecomRefreshAuthorization' }));
  await waitFor(() => expect(mocks.targets).toHaveBeenCalledWith(original.id));
  expect(onConnected).not.toHaveBeenCalled();
  await act(async () => { finishPermissionCheck({ items: [], next_cursor: '' }); });
  expect(onConnected).toHaveBeenCalledWith(account);
  expect(mocks.create).not.toHaveBeenCalled();
});


it.each(['wecom', 'wechat'] as const)('offers removal for %s robots through the shared manager', async provider => {
  rows = [{ ...original, provider, label: 'Managed robot', status: 'connected', binding_status: 'connected', runtime_status: 'running' }];
  render(<MemoryRouter><TerminalConnectionPage initialProvider={provider} embedded /></MemoryRouter>);
  const label = await screen.findByText('Managed robot');
  const card = label.closest('details')!;
  card.open = true; fireEvent(card, new Event('toggle'));
  await waitFor(() => expect(within(card).getByRole('button', { name: 'notifications.removeAccount' })).toBeEnabled());
});

it('keeps an expanded robot card and unsaved remark when its status refreshes', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Stable robot', status: 'connected', binding_status: 'connected', runtime_status: 'running' }];
  await mount('wecom');
  const card = screen.getAllByText('Stable robot')[0].closest('details')!;
  fireEvent.click(within(card).getByRole('button', { name: 'notifications.editAccountLabel' }));
  fireEvent.change(screen.getByRole('textbox', { name: 'notifications.accountRemark' }), { target: { value: 'Unsaved remark' } });
  rows = rows.map(row => ({ ...row, updated_at: '2026-10-29', runtime_status: 'stopped' }));
  fireEvent(window, new Event('focus'));
  await waitFor(() => expect(card.querySelector('summary')).toHaveTextContent('notifications.disconnected'));
  expect(screen.getAllByText('Stable robot')[0].closest('details')).toBe(card);
  expect(card.open).toBe(true);
  expect(screen.getByRole('textbox', { name: 'notifications.accountRemark' })).toHaveValue('Unsaved remark');
  expect(document.querySelector('.notification-provider-tabs button[aria-pressed="true"]')).toHaveTextContent('notifications.notConnected');
  expect(document.querySelector('.notification-account-manager > header')).not.toHaveTextContent('notifications.availableCount');
});

it('shows WeCom as awaiting authorization, excludes it from available counts, and recovers after refresh', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Authorization robot', status: 'connected', binding_status: 'connected', runtime_status: 'running', capabilities: { authorization_required: true } }];
  await mount('wecom');
  const card = screen.getAllByText('Authorization robot')[0].closest('details')!;
  expect(card.querySelector('summary')).toHaveTextContent('notifications.wecomAuthorizationExpired');
  expect(card.querySelector('summary')).not.toHaveTextContent('notifications.connected');
  expect(within(card).getByRole('button', { name: 'notifications.wecomRepairAuthorization' })).toBeEnabled();
  expect(document.querySelector('.notification-provider-tabs button[aria-pressed="true"]')).toHaveTextContent('notifications.wecomAuthorizationExpired');
  expect(document.querySelector('.notification-account-manager > header')).not.toHaveTextContent('notifications.availableCount');
  rows = rows.map(row => ({ ...row, capabilities: { authorization_required: false } }));
  fireEvent(window, new Event('focus'));
  await waitFor(() => expect(card.querySelector('summary')).toHaveTextContent('notifications.connected'));
  expect(card.open).toBe(true);
});

it('keeps the authorization panel mounted when a background refresh fails', async () => {
  rows = [{ ...original, provider: 'wecom', label: 'Repair robot' }];
  await mount('wecom');
  fireEvent.click(screen.getByRole('button', { name: 'notifications.wecomRepairAuthorization' }));
  const repair = await screen.findByRole('region', { name: 'notifications.wecomRestorePermissionTitle' });
  const credentials = within(repair).getByText('notifications.wecomUpdateCredentials').closest('details')!;
  credentials.open = true; fireEvent(credentials, new Event('toggle'));
  fireEvent.change(screen.getByLabelText('notifications.wecomBotId'), { target: { value: 'draft-bot' } });
  mocks.accounts.mockRejectedValue(new Error('temporarily unavailable'));
  fireEvent(window, new Event('focus'));
  await screen.findByText('notifications.loadFailed');
  expect(screen.getByRole('region', { name: 'notifications.wecomRestorePermissionTitle' })).toBe(repair);
  expect(screen.getByLabelText('notifications.wecomBotId')).toHaveValue('draft-bot');
});

it('keeps a scanned Feishu robot connected and explains a menu permission failure separately', async () => {
  rows = [{ ...original, label: 'Scanned robot', status: 'connected', binding_status: 'connected', runtime_status: 'degraded',
    capabilities: { connection_ready: true, bot_menu_ready: false, bot_menu_error: 'permission_required' } }];
  await mount();
  const card = screen.getAllByText('Scanned robot')[0].closest('details')!;
  expect(card.querySelector('summary')).toHaveTextContent('notifications.connected');
  expect(card.querySelector('summary')).not.toHaveTextContent('notifications.disconnected');
  expect(within(card).getByText('notifications.feishuConnectionReady')).toBeVisible();
  expect(within(card).getByRole('alert')).toHaveTextContent('notifications.feishuMenuPermissionHint');
  expect(within(card).getByRole('button', { name: 'notifications.disconnect' })).toBeEnabled();
  expect(document.querySelector('.notification-provider-tabs button[aria-pressed="true"]')).toHaveTextContent('notifications.connected');
  expect(mocks.create).not.toHaveBeenCalled();
});
