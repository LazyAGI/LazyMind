import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Modal } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import ScheduleNotificationPanel from './ScheduleNotificationPanel';
import { emptyRule, type NotificationConfig, type Preferences, type ScheduleNotifications } from './api';

const mocks = vi.hoisted(() => ({ preferences: vi.fn(), schedule: vi.fn(), put: vi.fn(), accounts: vi.fn(), detail: vi.fn(), targets: vi.fn(), groups: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async original => ({ ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getPreferences: mocks.preferences, getScheduleNotifications: mocks.schedule,
  putScheduleNotifications: mocks.put, getAccountDetail: mocks.detail, getTargets: mocks.targets, getGroups: mocks.groups }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/runtime/mode', async original => ({ ...await original<typeof import('@/runtime/mode')>(), isDesktopRuntime: () => true }));

const realConfirm = Modal.confirm;
let saved: ScheduleNotifications;
let preferences: Preferences;
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
};
const mount = (props: Partial<React.ComponentProps<typeof ScheduleNotificationPanel>> = {}) => render(
  <MemoryRouter><ScheduleNotificationPanel scheduleId="daily" editorOnly editorOpen showHistory={false} {...props} /></MemoryRouter>,
);
const choose = async (name: string, label: string) => {
  const select = screen.getByRole('combobox', { name });
  await waitFor(() => expect(select).toBeEnabled());
  fireEvent.mouseDown(select);
  let option: HTMLElement | undefined;
  await waitFor(() => {
    option = [...document.querySelectorAll<HTMLElement>('.ant-select-item-option')].find(item => item.textContent === label);
    expect(option).toBeDefined();
  });
  fireEvent.click(option!);
};
const edit = async () => {
  const waiting = await screen.findByRole('switch', { name: 'notifications.waiting' });
  await waitFor(() => expect(waiting).toBeEnabled());
  fireEvent.click(waiting);
  await choose('notifications.succeeded · notifications.content', 'notifications.full');
  await choose('notifications.account', 'Robot B');
  await waitFor(() => expect(mocks.targets).toHaveBeenCalledWith('robot-b'));
  await choose('notifications.recipient', 'Recipient B');
  return { waiting, body: document.querySelector<HTMLElement>('.ant-drawer-body')! };
};
const expectedDraft = (): NotificationConfig => ({ ...saved.config!, events: { ...saved.config!.events,
  succeeded: { enabled: true, content: 'full' }, waiting: { enabled: true, content: 'summary' } },
  channels: { ...saved.config!.channels, feishu: { enabled: true, account_id: 'robot-b', recipient_id: 'recipient-b' } } });

beforeEach(() => {
  vi.clearAllMocks();
  const config = { ...emptyRule(), channels: { feishu: { enabled: true, account_id: 'robot-a', recipient_id: 'recipient-a' } } };
  saved = { revision: 3, configured: true, config, availability: { feishu: { state: 'unavailable', reason: 'FEISHU_NOTIFICATION_TARGET_UNAVAILABLE' } } };
  preferences = { revision: 1, enabled: true, defaults: { ...emptyRule(), channels: { feishu: { enabled: true }, desktop: { enabled: true } } } };
  mocks.schedule.mockImplementation(async () => saved);
  mocks.preferences.mockImplementation(async () => preferences);
  mocks.accounts.mockImplementation(async (provider: string) => ({ items: provider === 'feishu' ? [
    { id: 'robot-a', label: 'Robot A', provider, status: 'connected', runtime_status: 'running' },
    { id: 'robot-b', label: 'Robot B', provider, status: 'connected', runtime_status: 'running' },
  ] : [] }));
  mocks.detail.mockResolvedValue({ id: 'robot-a', provider: 'feishu', label: 'Robot A' });
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'recipient-a', label: 'Recipient A', available: true }, { recipient_id: 'recipient-b', label: 'Recipient B', available: true }], next_cursor: '' });
  mocks.groups.mockResolvedValue({ items: [], next_cursor: '' });
  mocks.put.mockResolvedValue({ revision: 4, configured: true, config, availability: {} });
  vi.spyOn(Modal, 'confirm').mockImplementation(options => realConfirm({ ...options, transitionName: '', maskTransitionName: '' }));
});
afterEach(async () => { cleanup(); await act(async () => { Modal.destroyAll(); }); vi.restoreAllMocks(); });

it('refreshes availability in place and saves the event, content, robot and recipient drafts unchanged', async () => {
  mount();
  const { waiting, body } = await edit();
  body.scrollTop = 240;
  const refresh = deferred<ScheduleNotifications>();
  mocks.schedule.mockImplementationOnce(() => refresh.promise);
  const reload = screen.getByRole('button', { name: 'notifications.reload' });
  fireEvent.click(reload);
  expect(waiting).toBeInTheDocument();
  expect(waiting).toBeChecked();
  expect(document.querySelector('.ant-drawer-body')).toBe(body);
  expect(body.scrollTop).toBe(240);
  expect(mocks.put).not.toHaveBeenCalled();
  await act(async () => { refresh.resolve({ ...saved, availability: {} }); });
  await waitFor(() => expect(screen.queryByRole('button', { name: 'notifications.reload' })).not.toBeInTheDocument());
  expect(screen.getByRole('switch', { name: 'notifications.waiting' })).toBe(waiting);
  expect(waiting).toBeChecked();
  expect(Modal.confirm).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 3, expectedDraft()));
});

it('keeps the page and unsaved draft after a failed refresh and subsequent retry', async () => {
  mount();
  const { waiting, body } = await edit();
  mocks.schedule.mockRejectedValueOnce(new Error('network unavailable'));
  fireEvent.click(screen.getByRole('button', { name: 'notifications.reload' }));
  await screen.findByText('notifications.loadFailed');
  expect(screen.getByRole('switch', { name: 'notifications.waiting' })).toBe(waiting);
  expect(waiting).toBeChecked();
  expect(document.querySelector('.ant-drawer-body')).toBe(body);
  mocks.schedule.mockResolvedValueOnce({ ...saved, availability: {} });
  const retry = screen.getByRole('button', { name: 'notifications.reload' });
  await waitFor(() => expect(retry).not.toHaveClass('ant-btn-loading'));
  fireEvent.click(retry);
  await waitFor(() => expect(screen.queryByText('notifications.loadFailed')).not.toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 3, expectedDraft()));
});

it('refreshes the account and recipient lists without resetting a task draft when defaults change', async () => {
  mount();
  const { waiting } = await edit();
  const before = mocks.targets.mock.calls.length;
  preferences = { ...preferences, revision: 2, defaults: { ...preferences.defaults, events: { ...preferences.defaults.events, waiting: { enabled: false, content: 'full' } } } };
  mocks.schedule.mockResolvedValueOnce({ ...saved, availability: {} });
  fireEvent.click(screen.getByRole('button', { name: 'notifications.reload' }));
  await waitFor(() => expect(mocks.targets.mock.calls.length).toBeGreaterThan(before));
  expect(waiting).toBeChecked();
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 3, expectedDraft()));
});

it('preserves a new schedule draft while retrying a failed preferences read', async () => {
  const changed = vi.fn();
  preferences = { ...preferences, defaults: { ...emptyRule(), channels: { desktop: { enabled: true } } } };
  mount({ scheduleId: undefined, draftMode: true, onDraftChange: changed });
  const waiting = await screen.findByRole('switch', { name: 'notifications.waiting' });
  await waitFor(() => expect(waiting).toBeEnabled());
  fireEvent.click(waiting);
  mocks.preferences.mockRejectedValueOnce(new Error('network unavailable'));
  fireEvent.focus(window);
  await screen.findByText('notifications.loadFailed');
  fireEvent.click(screen.getByRole('button', { name: 'notifications.reload' }));
  await waitFor(() => expect(screen.getByRole('button', { name: /notifications.save$/ })).toBeEnabled());
  expect(waiting).toBeChecked();
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }) }) })));
  expect(mocks.schedule).not.toHaveBeenCalled();
  expect(mocks.put).not.toHaveBeenCalled();
});

it('ignores a previous schedule status response after navigating to another schedule', async () => {
  const view = mount();
  await edit();
  const stale = deferred<ScheduleNotifications>();
  mocks.schedule.mockImplementationOnce(() => stale.promise);
  fireEvent.click(screen.getByRole('button', { name: 'notifications.reload' }));
  mocks.schedule.mockResolvedValueOnce({ ...saved, revision: 7, availability: {} });
  view.rerender(<MemoryRouter><ScheduleNotificationPanel scheduleId="other" editorOnly editorOpen showHistory={false} /></MemoryRouter>);
  await waitFor(() => expect(screen.getByRole('switch', { name: 'notifications.waiting' })).not.toBeChecked());
  await act(async () => { stale.resolve({ ...saved, revision: 99 }); });
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('other', 7, saved.config));
});


it('retains local edits after reading a newer configuration and saves them normally with its revision', async () => {
  mount();
  const { waiting } = await edit();
  mocks.schedule.mockResolvedValueOnce({ ...saved, revision: 4, config: { ...saved.config!, channels: { desktop: { enabled: true } } }, availability: {} });
  fireEvent.click(screen.getByRole('button', { name: 'notifications.reload' }));
  await screen.findByText('notifications.taskConfigReloaded');
  expect(screen.getByRole('switch', { name: 'notifications.waiting' })).toBe(waiting);
  expect(waiting).toBeChecked();
  await waitFor(() => expect(screen.getByRole('button', { name: /notifications.save$/ })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(mocks.put).toHaveBeenCalledWith('daily', 4, expectedDraft()));
  expect(Modal.confirm).not.toHaveBeenCalled();
});

it('requires another status read after a save races with a concurrent update', async () => {
  mount();
  const { waiting } = await edit();
  mocks.put.mockRejectedValueOnce({ response: { data: { data: { detail: { reason: 'NOTIFICATION_CONFIG_CONFLICT' } } } } });
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await screen.findByText('notifications.taskConfigConflict');
  expect(waiting).toBeChecked();
  expect(screen.getByRole('button', { name: /notifications.save$/ })).toBeDisabled();
  mocks.schedule.mockResolvedValueOnce({ ...saved, revision: 5, availability: {} });
  fireEvent.click(screen.getByRole('button', { name: 'notifications.reload' }));
  await waitFor(() => expect(screen.getByRole('button', { name: /notifications.save$/ })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: /notifications.save$/ }));
  await waitFor(() => expect(mocks.put).toHaveBeenLastCalledWith('daily', 5, expectedDraft()));
  expect(mocks.put).toHaveBeenCalledTimes(2);
  expect(Modal.confirm).not.toHaveBeenCalled();
});
