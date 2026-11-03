import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { Modal } from 'antd';
import type { TFunction } from 'i18next';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { showDesktopNotificationPermission } from './DesktopNotificationPermission';

const state = vi.hoisted(() => ({ native: false, supported: true, openSettings: vi.fn() }));
vi.mock('@/runtime/mode', () => ({ isDesktopRuntime: () => state.native }));
vi.mock('@/runtime/desktopBridge', () => ({ openNotificationSettings: state.openSettings }));
vi.mock('./browser', () => ({
  browserNotificationsSupported: () => state.supported,
  desktopNotificationsAuthorized: () => Notification.permission === 'granted',
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
const t = ((key: string) => key) as TFunction;
const realInfo = Modal.info;
let requestPermission: ReturnType<typeof vi.fn>;

beforeEach(() => {
  state.native = false;
  state.supported = true;
  state.openSettings.mockReset().mockResolvedValue({ ok: true });
  requestPermission = vi.fn().mockResolvedValue('granted');
  vi.stubGlobal('Notification', { permission: 'default', requestPermission });
  vi.spyOn(Modal, 'info').mockImplementation((options: Parameters<typeof Modal.info>[0]) => realInfo({ ...options, transitionName: '', maskTransitionName: '' }));
});
afterEach(async () => {
  act(() => Modal.destroyAll());
  await waitFor(() => expect(screen.queryAllByRole('dialog')).toHaveLength(0));
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it('prompts for native system permissions even when the browser permission is granted', async () => {
  state.native = true;
  Object.assign(Notification, { permission: 'granted' });
  act(() => { showDesktopNotificationPermission(t); });

  expect(await screen.findByRole('dialog', { name: 'notifications.desktopPermissionTitle' })).toBeInTheDocument();
  expect(screen.getByText('notifications.nativeDesktopHint')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'notifications.browserAuthorize' })).toBeNull();
  expect(screen.getByRole('button', { name: 'notifications.openSystemNotificationSettings' })).toBeInTheDocument();
  expect(state.openSettings).not.toHaveBeenCalled();
  expect(requestPermission).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'notifications.desktopPermissionDone' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});

it('opens native settings on click, blocks duplicate requests and keeps the guidance available', async () => {
  state.native = true;
  let complete!: (value: { ok: true }) => void;
  state.openSettings.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
  act(() => { showDesktopNotificationPermission(t); });
  const button = await screen.findByRole('button', { name: 'notifications.openSystemNotificationSettings' });
  expect(state.openSettings).not.toHaveBeenCalled();
  fireEvent.click(button);
  fireEvent.click(button);
  expect(state.openSettings).toHaveBeenCalledTimes(1);
  expect(button).toHaveClass('ant-btn-loading');
  await act(async () => { complete({ ok: true }); });
  expect(button).not.toHaveClass('ant-btn-loading');
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(requestPermission).not.toHaveBeenCalled();
});

it.each(['failed', 'unavailable'])('keeps manual guidance and permits retry when opening settings is %s', async reason => {
  state.native = true;
  state.openSettings.mockResolvedValueOnce({ ok: false, reason }).mockResolvedValueOnce({ ok: true });
  act(() => { showDesktopNotificationPermission(t); });
  const button = await screen.findByRole('button', { name: 'notifications.openSystemNotificationSettings' });
  fireEvent.click(button);
  expect(await screen.findByRole('alert')).toHaveTextContent('notifications.openSystemNotificationSettingsFailed');
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.click(button);
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
  expect(state.openSettings).toHaveBeenCalledTimes(2);
});

it('lets the user explicitly request browser permission from the enable guidance', async () => {
  act(() => { showDesktopNotificationPermission(t); });

  const authorize = await screen.findByRole('button', { name: 'notifications.browserAuthorize' });
  expect(screen.queryByRole('button', { name: 'notifications.openSystemNotificationSettings' })).toBeNull();
  expect(requestPermission).not.toHaveBeenCalled();
  fireEvent.click(authorize);
  expect(await screen.findByText('notifications.browserPermission_granted')).toBeInTheDocument();
  expect(requestPermission).toHaveBeenCalledTimes(1);
});

it('explains how to restore a blocked browser permission without requesting again', async () => {
  Object.assign(Notification, { permission: 'denied' });
  act(() => { showDesktopNotificationPermission(t); });

  expect(await screen.findByText('notifications.browserPermission_denied')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'notifications.browserAuthorize' })).toBeNull();
  expect(requestPermission).not.toHaveBeenCalled();
});

it('does not prompt browsers that already have notification permission', () => {
  Object.assign(Notification, { permission: 'granted' });
  expect(showDesktopNotificationPermission(t)).toBeUndefined();
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(requestPermission).not.toHaveBeenCalled();
});
