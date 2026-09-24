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
it('constrains long account labels inside the inline notification grid', () => {
  const longAccount = {
    ...account,
    label: '飞书 · ou_74580b17e64b2f25dced653ef92169fe · cli_aa25feca7a785bb6',
  } as ChannelAccount;
  render(<TargetPicker inline provider="feishu" accounts={[longAccount]} current={{ enabled: true, account_id: 'a' }} onSave={() => {}} onClose={() => {}} />);
  const accountSelect = screen.getByRole('combobox', { name: 'notifications.account' });
  expect(accountSelect.closest('label')).toHaveClass('notification-target-field');
  expect(accountSelect.closest('.ant-select')).toHaveClass('notification-target-select');
});
it('copies an explicit default with a readable group name', async () => {
  const save = vi.fn();
  render(<TargetPicker provider="feishu" accounts={[account]} current={{ enabled: true, account_id: 'a' }} onSave={save} onClose={() => {}} />);
  await screen.findByText('产品日报群');
  // Selecting the default starts another validation request; wait for a usable save action.
  await waitFor(() => {
    const button = screen.getByRole('button', { name: 'notifications.save' });
    expect(button).toBeEnabled();
    fireEvent.click(button);
    expect(save).toHaveBeenCalledWith({ enabled: true, account_id: 'a', recipient_id: 'oc_daily' });
  });
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

it('explains how a WeCom group becomes searchable', async () => {
  mocks.targets.mockResolvedValue({ items: [], next_cursor: '' });
  const wecom = { ...account, provider: 'wecom', default_recipient_id: '' } as ChannelAccount;
  render(<TargetPicker provider="wecom" accounts={[wecom]} current={{ enabled: true, account_id: 'a' }} onSave={() => {}} onClose={() => {}} />);
  expect(await screen.findByText('notifications.noWecomTargets')).toBeInTheDocument();
  expect(mocks.groups).not.toHaveBeenCalled();
});

it('shows the account name without exposing an internal id for a direct-message recipient', async () => {
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'encrypted-user-id', label: '张三', kind: 'conversation', available: true }], next_cursor: '' });
  const wecom = { ...account, provider: 'wecom', default_recipient_id: '' } as ChannelAccount;
  render(<TargetPicker provider="wecom" accounts={[wecom]} current={{ enabled: true, account_id: 'a' }} onSave={() => {}} onClose={() => {}} />);
  fireEvent.mouseDown(await screen.findByRole('combobox', { name: 'notifications.recipient' }));
  expect(await screen.findByText('notifications.directConversation · 张三')).toBeInTheDocument();
});

it('keeps the recipient dropdown open and selectable while automatic refresh is pending', async () => {
  let finishRefresh: ((value: { items: never[]; next_cursor: string }) => void) | undefined;
  mocks.targets
    .mockResolvedValueOnce({ items: [{ recipient_id: 'group-1', label: '可选群聊', available: true }], next_cursor: '' })
    .mockImplementationOnce(() => new Promise(resolve => { finishRefresh = resolve; }));
  const wecom = { ...account, provider: 'wecom', default_recipient_id: '' } as ChannelAccount;
  const save = vi.fn();
  render(<TargetPicker inline provider="wecom" accounts={[wecom]} current={{ enabled: true, account_id: 'a' }} onSave={save} onClose={() => {}} />);

  const recipient = await screen.findByRole('combobox', { name: 'notifications.recipient' });
  fireEvent.mouseDown(recipient);
  await waitFor(() => expect(mocks.targets).toHaveBeenCalledTimes(2));

  expect(recipient).toBeEnabled();
  fireEvent.click(await screen.findByText('可选群聊'));
  expect(save).toHaveBeenCalledWith({ enabled: true, account_id: 'a', recipient_id: 'group-1' });
  finishRefresh?.({ items: [], next_cursor: '' });
});

it('replaces deleted and renamed targets on refresh without changing selection', async () => {
  mocks.targets.mockResolvedValueOnce({ items: [
    { recipient_id: 'kept', label: '旧名称', available: true },
    { recipient_id: 'removed', label: '已删除对象', available: true },
  ], next_cursor: 'old-page' }).mockResolvedValue({ items: [
    { recipient_id: 'kept', label: '新名称', available: false },
  ], next_cursor: '' });
  const save = vi.fn();
  const wecom = { ...account, provider: 'wecom', default_recipient_id: '' } as ChannelAccount;
  render(<TargetPicker inline provider="wecom" accounts={[wecom]} current={{ enabled: true, account_id: 'a' }} onSave={save} onClose={() => {}} />);
  await screen.findByRole('button', { name: 'notifications.loadMore' });
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'notifications.recipient' }));
  expect(await screen.findByText('新名称')).toBeInTheDocument();
  expect(screen.queryByText('已删除对象')).not.toBeInTheDocument();
  expect(screen.queryByText('旧名称')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'notifications.loadMore' })).not.toBeInTheDocument();
  expect(save).not.toHaveBeenCalled();
});
