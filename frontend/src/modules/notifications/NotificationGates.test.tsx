import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Modal } from 'antd';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import ScheduleNotificationPanel from './ScheduleNotificationPanel';
import NotificationSettings from './NotificationSettings';
import RuleEditor from './RuleEditor';
import { emptyRule, type NotificationConfig, type Preferences } from './api';

const mocks = vi.hoisted(() => ({ prefs: vi.fn(), patch: vi.fn(), schedule: vi.fn(), put: vi.fn(), accounts: vi.fn(), targets: vi.fn(), groups: vi.fn(), tasks: vi.fn(), runs: vi.fn(), execution: vi.fn(), detail: vi.fn(), desktop: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async original => ({ ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('@/modules/taskCenter/api', () => ({ listTasks: mocks.tasks, listScheduleTasks: mocks.runs }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getPreferences: mocks.prefs, patchPreferences: mocks.patch, getScheduleNotifications: mocks.schedule, putScheduleNotifications: mocks.put, getExecutionNotifications: mocks.execution, getTargets: mocks.targets, getGroups: mocks.groups, getAccountDetail: mocks.detail }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/runtime/mode', async original => ({ ...await original<typeof import('@/runtime/mode')>(), isDesktopRuntime: mocks.desktop }));

let preferences: Preferences;
let taskConfig: NotificationConfig;
const realConfirm = Modal.confirm;
const mount = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);
const channelBlock = () => within(document.querySelector<HTMLElement>('.notification-rules.is-task .is-feishu')!);
const taskEditor = () => within(document.querySelector<HTMLElement>('.notification-rules.is-task')!);
const confirmation = () => within(document.querySelector<HTMLElement>('.ant-modal-confirm')!);
const enableButton = () => channelBlock().getByRole('button', { name: 'notifications.openSettingsEnable · notifications.feishu' });
const settingsEditor = () => within(document.querySelector<HTMLElement>('.notification-settings')!);
async function requestChannelEnable() {
  fireEvent.click(enableButton());
  await screen.findByText('notifications.enableChannelContext');
  const toggle = settingsEditor().getByRole('switch', { name: 'notifications.feishu' });
  await waitFor(() => expect(toggle).toBeEnabled());
  fireEvent.click(toggle);
  await screen.findByText('notifications.enableChannelHint');
}
async function openTask() {
  mount(<ScheduleNotificationPanel scheduleId="daily" title="Daily briefing" showHistory={false} summaryCard />);
  const entry = await screen.findByRole('button', { name: 'notifications.configure' });
  await waitFor(() => expect(entry).toBeEnabled());
  fireEvent.click(entry);
  await screen.findByRole('switch', { name: 'notifications.feishu' });
  return entry;
}

beforeEach(() => {
  vi.clearAllMocks();
  taskConfig = emptyRule();
  taskConfig.channels = { desktop: { enabled: true }, feishu: { enabled: true, account_id: 'account-a', recipient_id: 'group-a' } };
  preferences = { revision: 3, enabled: true, defaults: { ...emptyRule(), channels: { desktop: { enabled: true }, feishu: { enabled: false }, wecom: { enabled: false }, wechat: { enabled: false } } } };
  mocks.prefs.mockImplementation(async () => structuredClone(preferences));
  mocks.schedule.mockImplementation(async () => ({ revision: 4, configured: true, config: structuredClone(taskConfig), availability: {} }));
  mocks.put.mockImplementation(async (_id, _revision, config) => ({ revision: 5, configured: true, config, availability: {} }));
  mocks.accounts.mockImplementation(async (provider: string) => ({ items: provider === 'feishu' ? [{ id: 'account-a', provider, label: 'Account A', status: 'connected' }] : [] }));
  mocks.groups.mockResolvedValue({ items: [], next_cursor: '' });
  mocks.tasks.mockResolvedValue({ items: [], total: 0 });
  mocks.runs.mockResolvedValue({ items: [], total: 0 });
  mocks.execution.mockResolvedValue({ snapshot: { config: null, revision: 0 }, items: [] });
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'group-a', label: 'Group A', available: true }], next_cursor: '' });
  mocks.detail.mockResolvedValue({ id: 'account-a', provider: 'feishu', label: 'Account A', status: 'connected', primary_recipient: null, notification_reference_count: 1 });
  mocks.desktop.mockReturnValue(true);
  Object.defineProperty(window, 'Notification', { configurable: true, value: { permission: 'granted', requestPermission: vi.fn() } });
  vi.spyOn(Modal, 'confirm').mockImplementation((options: Parameters<typeof Modal.confirm>[0]) => realConfirm({ ...options, transitionName: '', maskTransitionName: '' }));
});
afterEach(async () => {
  cleanup();
  await act(async () => Modal.destroyAll());
  await waitFor(() => expect(document.querySelector('.ant-modal-root')).toBeNull());
  vi.restoreAllMocks();
});

describe('task notification global channel gates', () => {
  it.each(['channel', 'master'])('saves other event changes after the %s gate pauses an inherited channel without a target', async gate => {
    preferences.defaults.channels.feishu = { enabled: true };
    const changed = vi.fn();
    mount(<ScheduleNotificationPanel compact draftMode onDraftChange={changed} />);
    const entry = await screen.findByRole('button', { name: 'notifications.configure' });
    await waitFor(() => expect(entry).toBeEnabled());
    fireEvent.click(entry);
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'notifications.cancel' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'notifications.save' })).not.toBeInTheDocument());
    if (gate === 'master') preferences.enabled = false;
    else preferences.defaults.channels.feishu = { enabled: false };
    fireEvent.click(entry);
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeDisabled());
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeChecked();
    fireEvent.click(taskEditor().getByRole('switch', { name: 'notifications.waiting' }));
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({
      channels: expect.objectContaining({ feishu: { enabled: true } }),
      events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }),
    }) })));
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it('keeps settings open during a slow update and returns with fresh gates and the task draft', async () => {
    await openTask();
    fireEvent.click(taskEditor().getByRole('switch', { name: 'notifications.waiting' }));
    let finish!: (value: Preferences) => void;
    mocks.patch.mockImplementation(() => new Promise<Preferences>(resolve => { finish = resolve; }));
    await requestChannelEnable();
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    const settingsModal = document.querySelector<HTMLElement>('.notification-settings')!.closest<HTMLElement>('.ant-modal-content')!;
    const back = within(settingsModal).getByRole('button', { name: 'notifications.return' });
    expect(back).toBeDisabled();
    fireEvent.click(back);
    fireEvent.click(within(settingsModal).getByRole('button', { name: 'Close' }));
    fireEvent.keyDown(settingsModal.closest('.ant-modal-wrap')!, { key: 'Escape', code: 'Escape', keyCode: 27 });
    expect(screen.getByText('notifications.enableChannelContext')).toBeInTheDocument();
    const updated = { ...preferences, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } };
    await act(async () => finish(updated));
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    expect(screen.queryByText('notifications.enableChannelContext')).not.toBeInTheDocument();
    expect(taskEditor().getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({
      channels: taskConfig.channels,
      events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }),
    })));
  });

  it('opens connections inside nested settings and returns to the same unsaved task', async () => {
    mocks.accounts.mockResolvedValue({ items: [] });
    render(<MemoryRouter initialEntries={['/task']}><Routes>
      <Route path="/task" element={<ScheduleNotificationPanel scheduleId="daily" title="Daily briefing" showHistory={false} summaryCard />} />
      <Route path="/settings" element={<p>Left task editor</p>} />
    </Routes></MemoryRouter>);
    const entry = await screen.findByRole('button', { name: 'notifications.configure' });
    await waitFor(() => expect(entry).toBeEnabled());
    fireEvent.click(entry);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.waiting' }));
    fireEvent.click(enableButton());
    await screen.findByText('notifications.enableChannelContext');
    const settingsFeishu = within(document.querySelector<HTMLElement>('.notification-settings .is-feishu')!);
    fireEvent.click(await settingsFeishu.findByRole('button', { name: /notifications.connect$/ }));
    expect(await screen.findByRole('navigation', { name: 'notifications.channels' })).toBeInTheDocument();
    expect(screen.queryByText('Left task editor')).not.toBeInTheDocument();
    mocks.accounts.mockImplementation(async (provider: string) => ({ items: provider === 'feishu' ? [{ id: 'account-a', provider, label: 'Account A', status: 'connected' }] : [] }));
    const connectionModal = document.querySelector<HTMLElement>('.notification-connections')!.closest<HTMLElement>('.ant-modal-content')!;
    fireEvent.click(within(connectionModal).getByRole('button', { name: 'notifications.return' }));
    const toggle = await settingsEditor().findByRole('switch', { name: 'notifications.feishu' });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(screen.getByText('notifications.enableChannelContext')).toBeInTheDocument();
    mocks.patch.mockResolvedValue({ ...preferences, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } });
    fireEvent.click(toggle);
    await screen.findByText('notifications.enableChannelHint');
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    expect(await channelBlock().findByRole('combobox', { name: 'notifications.account' })).toBeEnabled();
    expect(await channelBlock().findByText('Account A', { selector: '.ant-select-selection-item' })).toBeInTheDocument();
    expect(taskEditor().getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({
      channels: taskConfig.channels,
      events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }),
    })));
  });

  it('selects a newly connected account and recipient after settings enables the channel', async () => {
    taskConfig.channels.feishu = { enabled: false };
    mocks.accounts.mockResolvedValue({ items: [] });
    await openTask();
    fireEvent.click(taskEditor().getByRole('switch', { name: 'notifications.waiting' }));
    fireEvent.click(enableButton());
    await screen.findByText('notifications.enableChannelContext');
    const settingsFeishu = within(document.querySelector<HTMLElement>('.notification-settings .is-feishu')!);
    fireEvent.click(await settingsFeishu.findByRole('button', { name: /notifications.connect$/ }));
    expect(await screen.findByRole('navigation', { name: 'notifications.channels' })).toBeInTheDocument();
    mocks.accounts.mockImplementation(async (provider: string) => ({ items: provider === 'feishu' ? [{ id: 'account-a', provider, label: 'Account A', status: 'connected' }] : [] }));
    const connectionModal = document.querySelector<HTMLElement>('.notification-connections')!.closest<HTMLElement>('.ant-modal-content')!;
    fireEvent.click(within(connectionModal).getByRole('button', { name: 'notifications.return' }));
    const toggle = await settingsEditor().findByRole('switch', { name: 'notifications.feishu' });
    await waitFor(() => expect(toggle).toBeEnabled());
    mocks.patch.mockResolvedValue({ ...preferences, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } });
    fireEvent.click(toggle);
    await screen.findByText('notifications.enableChannelHint');
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));

    const account = await channelBlock().findByRole('combobox', { name: 'notifications.account' });
    await waitFor(() => expect(account).toBeEnabled());
    fireEvent.mouseDown(account);
    fireEvent.click(await screen.findByText('Account A', { selector: '.ant-select-item-option-content' }));
    await waitFor(() => expect(channelBlock().getByRole('combobox', { name: 'notifications.account' })).toBeEnabled());
    fireEvent.mouseDown(channelBlock().getByRole('combobox', { name: 'notifications.recipient' }));
    fireEvent.click(await screen.findByText('Group A', { selector: '.ant-select-item-option-content' }));
    expect(taskEditor().getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({
      channels: { ...taskConfig.channels, feishu: { enabled: true, account_id: 'account-a', recipient_id: 'group-a' } },
      events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }),
    })));
    expect(mocks.schedule).toHaveBeenCalledTimes(1);
  });

  it.each(['channel', 'master'])('keeps selected channels and targets disabled when the %s gate is off', async gate => {
    if (gate === 'master') { preferences.enabled = false; preferences.defaults.channels.feishu = { enabled: true }; }
    const entry = await openTask();
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeDisabled());
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeChecked();
    expect(channelBlock().getByRole('combobox', { name: 'notifications.account' })).toBeDisabled();
    expect(channelBlock().getByRole('combobox', { name: 'notifications.recipient' })).toBeDisabled();
    expect(channelBlock().getByText('notifications.channelDisabledHint')).toBeInTheDocument();
    expect(channelBlock().queryByRole('button', { name: 'notifications.openSettings' })).not.toBeInTheDocument();
    expect(entry).toHaveTextContent('notifications.channelPaused');
    expect(enableButton()).toBeEnabled();
    fireEvent.click(screen.getByRole('switch', { name: 'notifications.waiting' }));
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({
      channels: taskConfig.channels,
      events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }),
    })));
  });

  it('cancels global enabling without updating preferences or the task selection', async () => {
    taskConfig.channels.feishu!.enabled = false;
    await openTask();
    await requestChannelEnable();
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.cancel' }));
    await waitFor(() => expect(screen.queryByText('notifications.enableChannelHint')).not.toBeInTheDocument());
    expect(mocks.patch).not.toHaveBeenCalled();
    const preferenceReads = mocks.prefs.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'notifications.return' }));
    await waitFor(() => expect(mocks.prefs).toHaveBeenCalledTimes(preferenceReads + 1));
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).not.toBeChecked();
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeDisabled();
  });

  it('confirms using the loaded settings revision, preserves other preferences and returns to this task after success', async () => {
    preferences.enabled = false;
    taskConfig.channels.feishu!.enabled = false;
    await openTask();
    preferences = { ...preferences, revision: 9, defaults: { ...preferences.defaults, events: { ...preferences.defaults.events, waiting: { enabled: true, content: 'full' } }, channels: { ...preferences.defaults.channels, wecom: { enabled: true } } } };
    mocks.patch.mockImplementation(async patch => ({ ...preferences, ...patch, revision: 10 }));
    await requestChannelEnable();
    expect(screen.getByText('notifications.enableMasterHint')).toBeInTheDocument();
    expect(mocks.patch).not.toHaveBeenCalled();
    const readsBeforeConfirm = mocks.prefs.mock.calls.length;
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith({ revision: 9, enabled: true, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } }));
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    expect(screen.queryByText('notifications.enableChannelContext')).not.toBeInTheDocument();
    expect(mocks.prefs).toHaveBeenCalledTimes(readsBeforeConfirm);
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({ channels: { ...taskConfig.channels, feishu: { enabled: true, account_id: 'account-a', recipient_id: 'group-a' } } })));
  });

  it.each(['NOTIFICATION_UNAVAILABLE', 'NOTIFICATION_CONFIG_CONFLICT'])('preserves the draft and refreshes settings after %s enabling failure', async reason => {
    taskConfig.channels.feishu!.enabled = false;
    await openTask();
    fireEvent.click(screen.getByRole('switch', { name: 'notifications.waiting' }));
    mocks.patch.mockImplementation(async () => {
      preferences = { ...preferences, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } };
      throw { response: { data: { data: { detail: { reason } } } } };
    });
    await requestChannelEnable();
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(settingsEditor().getByRole('switch', { name: 'notifications.feishu' })).toBeChecked());
    fireEvent.click(screen.getByRole('button', { name: 'notifications.return' }));
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).not.toBeChecked();
    expect(taskEditor().getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    expect(channelBlock().queryByRole('button', { name: 'notifications.openSettingsEnable · notifications.feishu' })).not.toBeInTheDocument();
    expect(mocks.put).not.toHaveBeenCalled();
  });

  it('requires a new confirmation after concurrent changes turn the master switch off', async () => {
    taskConfig.channels.feishu!.enabled = false;
    await openTask();
    await requestChannelEnable();
    expect(screen.queryByText('notifications.enableMasterHint')).not.toBeInTheDocument();
    preferences = { ...preferences, enabled: false, revision: 4 };
    mocks.patch.mockRejectedValue({ response: { data: { data: { detail: { reason: 'NOTIFICATION_CONFIG_CONFLICT' } } } } });
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith({ revision: 3, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } }));
    await waitFor(() => expect(settingsEditor().getByRole('switch', { name: 'notifications.global' })).toBeEnabled());
    expect(settingsEditor().getByRole('switch', { name: 'notifications.global' })).not.toBeChecked();
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).not.toBeChecked();
    fireEvent.click(settingsEditor().getByRole('switch', { name: 'notifications.global' }));
    expect(await screen.findByText('notifications.enableMasterHint')).toBeInTheDocument();
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.cancel' }));
    expect(mocks.patch).toHaveBeenCalledTimes(1);
    expect(mocks.put).not.toHaveBeenCalled();
  });

  it('confirms the master switch when the requested channel is already enabled globally', async () => {
    preferences.enabled = false;
    preferences.defaults.channels.feishu = { enabled: true };
    taskConfig.channels.feishu!.enabled = false;
    await openTask();
    fireEvent.click(taskEditor().getByRole('switch', { name: 'notifications.waiting' }));
    fireEvent.click(enableButton());
    await screen.findByText('notifications.enableChannelContext');
    mocks.patch.mockResolvedValue({ ...preferences, enabled: true, revision: 4 });
    fireEvent.click(settingsEditor().getByRole('switch', { name: 'notifications.global' }));
    expect(await screen.findByText('notifications.enableMasterHint')).toBeInTheDocument();
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith({ revision: 3, enabled: true }));
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeChecked();
    expect(taskEditor().getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({ channels: { ...taskConfig.channels, feishu: { ...taskConfig.channels.feishu, enabled: true } }, events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }) })));
  });

  it('locks settings after an enabling failure if its current revision cannot be refreshed', async () => {
    taskConfig.channels.feishu!.enabled = false;
    await openTask();
    await requestChannelEnable();
    mocks.patch.mockRejectedValue(new Error('unavailable'));
    mocks.prefs.mockRejectedValue(new Error('unavailable'));
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.enableChannel' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(settingsEditor().getByRole('switch', { name: 'notifications.feishu' })).toBeDisabled());
    expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).not.toBeChecked();
    expect(mocks.put).not.toHaveBeenCalled();
  });

  it('seeds new task channel flags without recipients and permits an independent task disable', async () => {
    preferences.defaults.channels.feishu = { enabled: true, account_id: 'legacy', recipient_id: 'legacy-target' };
    const changed = vi.fn();
    mount(<ScheduleNotificationPanel compact draftMode onDraftChange={changed} />);
    const entry = await screen.findByRole('button', { name: 'notifications.configure' });
    await waitFor(() => expect(entry).toBeEnabled());
    fireEvent.click(entry);
    const toggle = await screen.findByRole('switch', { name: 'notifications.feishu' });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).toBeChecked();
    expect(channelBlock().getByRole('combobox', { name: 'notifications.account' })).toHaveValue('');
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    expect(await screen.findByText('notifications.NOTIFICATION_TARGET_REQUIRED')).toBeInTheDocument();
    expect(changed).not.toHaveBeenCalled();
    fireEvent.click(toggle);
    await screen.findAllByText('notifications.closeNamedTitle');
    fireEvent.click(confirmation().getByRole('button', { name: 'notifications.confirmClose' }));
    await waitFor(() => expect(toggle).not.toBeChecked());
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ channels: expect.objectContaining({ feishu: { enabled: false } }) }) })));
    expect(mocks.patch).not.toHaveBeenCalled();
    expect(mocks.put).not.toHaveBeenCalled();
  });

  it('refreshes channel gates and accounts on ordinary return without discarding unsaved task targets', async () => {
    preferences.defaults.channels.feishu = { enabled: true };
    mocks.targets.mockResolvedValue({ items: [
      { recipient_id: 'group-a', label: 'Group A', available: true },
      { recipient_id: 'group-b', label: 'Group B', available: true },
    ], next_cursor: '' });
    await openTask();
    await waitFor(() => expect(channelBlock().getByRole('combobox', { name: 'notifications.account' })).toBeEnabled());
    fireEvent.mouseDown(channelBlock().getByRole('combobox', { name: 'notifications.recipient' }));
    fireEvent.click(await screen.findByText('Group B', { selector: '.ant-select-item-option-content' }));
    fireEvent.click(taskEditor().getByRole('switch', { name: 'notifications.waiting' }));
    fireEvent.click(taskEditor().getByRole('button', { name: 'notifications.openSettingsEnable · notifications.wecom' }));
    await screen.findByRole('switch', { name: 'notifications.global' });
    preferences = { ...preferences, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, wecom: { enabled: true } } } };
    mocks.accounts.mockImplementation(async (provider: string) => ({ items: provider === 'feishu' ? [{ id: 'account-a', provider, label: 'Reconnected Account A', status: 'connected' }] : [] }));
    fireEvent.click(screen.getByRole('button', { name: 'notifications.return' }));
    expect(await channelBlock().findByText('Reconnected Account A', { selector: '.ant-select-selection-item' })).toBeInTheDocument();
    expect(channelBlock().getByText('Group B', { selector: '.ant-select-selection-item' })).toBeInTheDocument();
    expect(taskEditor().getByRole('switch', { name: 'notifications.wecom' })).not.toBeChecked();
    expect(taskEditor().queryByRole('button', { name: 'notifications.openSettingsEnable · notifications.wecom' })).not.toBeInTheDocument();
    expect(taskEditor().getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expect.objectContaining({
      channels: { ...taskConfig.channels, feishu: { enabled: true, account_id: 'account-a', recipient_id: 'group-b' } },
      events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }),
    })));
    expect(mocks.schedule).toHaveBeenCalledTimes(1);
  });

  it('refreshes channel gates when reopening a noncompact editor', async () => {
    const entry = await openTask();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.cancel' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'notifications.save' })).not.toBeInTheDocument());
    preferences = { ...preferences, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, feishu: { enabled: true } } } };
    fireEvent.click(entry);
    await waitFor(() => expect(channelBlock().getByRole('switch', { name: 'notifications.feishu' })).toBeEnabled());
    expect(mocks.schedule).toHaveBeenCalledTimes(1);
  });
});

describe('settings notification scopes', () => {
  it.each(['desktop', 'global'])('includes ordinary and legacy scheduled runs in the %s close review', async channel => {
    mocks.tasks.mockResolvedValue({ items: [
      { id: 'ordinary', title: 'Workflow in progress', status: 'running', task_type: 'workflow' },
      { id: 'legacy', title: 'Legacy scheduled run', status: 'running', task_type: 'scheduled', schedule_id: 'old' },
      { id: 'disabled', title: 'No channels selected', status: 'running', task_type: 'scheduled', schedule_id: 'disabled-plan' },
    ], total: 3 });
    mocks.execution.mockImplementation(async id => ({ snapshot: { config: id === 'disabled' ? emptyRule() : null, revision: 0 }, items: [] }));
    mount(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: `notifications.${channel}` }));
    expect(await screen.findByText('Workflow in progress')).toBeInTheDocument();
    expect(screen.getByText('Legacy scheduled run')).toBeInTheDocument();
    expect(screen.queryByText('No channels selected')).not.toBeInTheDocument();
    expect(mocks.tasks).toHaveBeenCalledWith({ page: 1, page_size: 100 });
    expect(mocks.execution).not.toHaveBeenCalledWith('ordinary');
    expect(mocks.execution).toHaveBeenCalledWith('legacy');
  });

  it('retains desktop scope text alongside browser permission help', async () => {
    mocks.desktop.mockReturnValue(false);
    mount(<RuleEditor value={preferences.defaults} onChange={vi.fn()} />);
    expect(await screen.findByText('notifications.desktopHint')).toBeInTheDocument();
    expect(screen.getByText('notifications.browserUnsupported')).toBeInTheDocument();
  });

  it.each([['desktop', 'closeDesktopExplanation'], ['feishu', 'closeChannelExplanation']])('uses the %s scope when closing a global channel', async (channel, explanation) => {
    preferences.defaults.channels.feishu = { enabled: true };
    mount(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: `notifications.${channel}` }));
    expect(await screen.findByText(`notifications.${explanation}`)).toBeInTheDocument();
    expect(mocks.patch).not.toHaveBeenCalled();
  });
});
