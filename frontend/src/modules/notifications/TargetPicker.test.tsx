import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { ChannelAccount } from '@/modules/channelGateway/api';
import TargetPicker from './TargetPicker';
const mocks = vi.hoisted(() => ({ targets: vi.fn(), groups: vi.fn() }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getTargets: mocks.targets, getGroups: mocks.groups }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
const account = { id: 'a', provider: 'feishu', label: 'Bot', status: 'connected', default_recipient_id: 'oc_daily' } as ChannelAccount;
beforeEach(() => {
  vi.clearAllMocks();
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'oc_old', label: '原接收群', available: true }], next_cursor: '' });
  mocks.groups.mockResolvedValue({ items: [{ recipient_id: 'oc_daily', label: '产品日报群', available: true, kind: 'group' }], next_cursor: '' });
});
afterEach(cleanup);
it('copies an explicit default with a readable group name', async () => {
  const save = vi.fn();
  render(<TargetPicker provider="feishu" accounts={[account]} current={{ enabled: true, account_id: 'a' }} onSave={save} onClose={() => {}} />);
  await screen.findByText('产品日报群');
  await waitFor(() => expect(screen.getByRole('button', { name: 'notifications.save' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
  expect(save).toHaveBeenCalledWith({ enabled: true, account_id: 'a', recipient_id: 'oc_daily' });
});
it('preserves a saved recipient when the connection default differs', async () => {
  const save = vi.fn();
  render(<TargetPicker provider="feishu" accounts={[account]} current={{ enabled: true, account_id: 'a', recipient_id: 'oc_old' }} onSave={save} onClose={() => {}} />);
  await screen.findByText('原接收群');
  await waitFor(() => expect(screen.getByRole('button', { name: 'notifications.save' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
  expect(save).toHaveBeenCalledWith({ enabled: true, account_id: 'a', recipient_id: 'oc_old' });
});
it('retains known recipients when group permission is missing', async () => {
  mocks.groups.mockRejectedValue(new Error('permission denied'));
  render(<TargetPicker provider="feishu" accounts={[account]} current={{ enabled: true, account_id: 'a', recipient_id: 'oc_old' }} onSave={() => {}} onClose={() => {}} />);
  await screen.findByText('notifications.groupPermissionHint');
  await waitFor(() => expect(screen.getByRole('button', { name: 'notifications.save' })).toBeEnabled());
});
it('resolves the default even when it is outside the first page', async () => {
  mocks.groups.mockResolvedValue({ items: [], next_cursor: '' });
  mocks.targets.mockImplementation((_account: string, _cursor = '', recipient = '') => Promise.resolve({
    items: recipient ? [{ recipient_id: 'oc_daily', label: '后页默认群', available: true }] : [], next_cursor: '',
  }));
  const save = vi.fn();
  render(<TargetPicker provider="feishu" accounts={[account]} current={{ enabled: true, account_id: 'a' }} onSave={save} onClose={() => {}} />);
  await screen.findByText('后页默认群');
  fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
  expect(save).toHaveBeenCalledWith({ enabled: true, account_id: 'a', recipient_id: 'oc_daily' });
});
