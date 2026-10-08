import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { ChannelAccount } from '@/modules/channelGateway/api';
import TargetPicker from './TargetPicker';

const mocks = vi.hoisted(() => ({ targets: vi.fn(), connection: vi.fn() }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getTargets: mocks.targets }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/modules/channelGateway/pages/ChannelConnectionPage', () => ({
  ChannelConnectionPage: (props: { provider: string; accountId: string; autoStart?: boolean; onConnected: (account: ChannelAccount) => void }) => {
    mocks.connection(props);
    return <button onClick={() => props.onConnected({ id: props.accountId, provider: 'wecom', label: 'Original bot', status: 'connected' } as ChannelAccount)}>Complete reauthorization</button>;
  },
}));
beforeEach(() => { vi.clearAllMocks(); });
afterEach(cleanup);

it('repairs the original bot in place and refreshes recipients without changing the saved rule', async () => {
  mocks.targets.mockRejectedValue({ response: { data: { error: { code: 'WECOM_CAPABILITY_REAUTH_REQUIRED' } } } });
  const rule = { enabled: true, account_id: 'original-bot', recipient_id: 'original-group' };
  const onSave = vi.fn();
  const onAccountConnected = vi.fn();
  const onAuthorizationChange = vi.fn();
  render(<TargetPicker inline provider="wecom" accounts={[]} current={rule} onSave={onSave} onClose={vi.fn()} onAccountConnected={onAccountConnected} onAuthorizationChange={onAuthorizationChange} />);
  fireEvent.click(await screen.findByRole('button', { name: 'notifications.wecomRepairAuthorization' }));
  expect(mocks.connection).toHaveBeenCalledWith(expect.objectContaining({ provider: 'wecom', accountId: rule.account_id }));
  expect(mocks.connection.mock.lastCall?.[0].autoStart).toBeUndefined();
  await waitFor(() => expect(onAuthorizationChange).toHaveBeenCalledWith(rule.account_id, true));
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: rule.recipient_id, label: 'Original group', available: true }], next_cursor: '' });
  fireEvent.click(screen.getByRole('button', { name: 'Complete reauthorization' }));
  await waitFor(() => expect(screen.queryByText('notifications.wecomAuthorizationExpired')).toBeNull());
  await waitFor(() => expect(onAuthorizationChange).toHaveBeenLastCalledWith(rule.account_id, false));
  expect(onAccountConnected).toHaveBeenCalledWith(expect.objectContaining({ id: rule.account_id }));
  expect(mocks.targets).toHaveBeenCalledWith(rule.account_id, '', rule.recipient_id);
  expect(onSave).not.toHaveBeenCalled();
  expect(rule).toEqual({ enabled: true, account_id: 'original-bot', recipient_id: 'original-group' });
});

it('rechecks capability after permission restoration without starting a connection or changing recipients', async () => {
  mocks.targets.mockRejectedValue({ response: { data: { error: { code: 'WECOM_CAPABILITY_REAUTH_REQUIRED' } } } });
  const onSave = vi.fn();
  render(<TargetPicker inline provider="wecom" accounts={[]} current={{ enabled: true, account_id: 'original-bot', recipient_id: 'original-group' }} onSave={onSave} onClose={vi.fn()} />);
  const refresh = await screen.findByRole('button', { name: 'notifications.wecomRefreshAuthorization' });
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'original-group', label: 'Original group', available: true }], next_cursor: '' });
  fireEvent.click(refresh);
  await waitFor(() => expect(screen.queryByText('notifications.wecomAuthorizationExpired')).toBeNull());
  expect(mocks.connection).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});

it('allows an explicit switch to a separate bot and clears the old recipient', async () => {
  mocks.targets.mockImplementation((accountId: string) => accountId === 'original-bot'
    ? Promise.reject({ response: { data: { error: { code: 'WECOM_CAPABILITY_REAUTH_REQUIRED' } } } })
    : Promise.resolve({ items: [{ recipient_id: 'new-group', label: 'New group', available: true }], next_cursor: '' }));
  const onSave = vi.fn();
  const accounts = [
    { id: 'original-bot', provider: 'wecom', status: 'connected', label: 'Original bot' },
    { id: 'new-bot', provider: 'wecom', status: 'connected', label: 'New bot' },
  ] as ChannelAccount[];
  render(<TargetPicker inline provider="wecom" accounts={accounts} current={{ enabled: true, account_id: 'original-bot', recipient_id: 'original-group' }} onSave={onSave} onClose={vi.fn()} />);
  await screen.findByText('notifications.wecomSwitchAccountHint');
  expect(onSave).not.toHaveBeenCalled();
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'notifications.account' }));
  fireEvent.click(await screen.findByText('New bot'));
  expect(onSave).toHaveBeenLastCalledWith({ enabled: true, account_id: 'new-bot' });
  await waitFor(() => expect(mocks.targets).toHaveBeenCalledWith('new-bot'));
  expect(mocks.targets).not.toHaveBeenCalledWith('new-bot', '', 'original-group');
});
