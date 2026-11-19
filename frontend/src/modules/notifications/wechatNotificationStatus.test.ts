import { describe, expect, it } from 'vitest';
import type { ChannelAccount } from '@/modules/channelGateway/api';
import { wechatAccountStatus, wechatEditorStatus, wechatTargetStatus, wechatStatusLabel, wechatStatusHint } from './wechatNotificationStatus';

const account = (patch: Partial<ChannelAccount> = {}): ChannelAccount => ({
  id: 'selected', provider: 'wechat', label: 'Bot', status: 'connected', runtime_status: 'running', capabilities: {},
  ...patch,
} as ChannelAccount);

describe('WeChat notification status precedence', () => {
  it.each(['degraded', 'failed'] as const)('does not call a %s runtime an activation problem', runtime_status => {
    expect(wechatAccountStatus(account({ runtime_status, capabilities: { notification_ready: false } }), false)).toBe('unavailable');
  });
  it.each(['connected', 'disconnected'] as const)('keeps login expiry separate from activation when the account is %s', status => {
    expect(wechatAccountStatus(account({ status, capabilities: { connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED', notification_ready: false } }), false)).toBe('reauthorization');
  });
  it('uses the exact recipient check instead of another recipient failure', () => {
    const bot = account({ capabilities: { connection_check_status: 'unavailable', notification_ready: false } });
    expect(wechatTargetStatus(bot, { recipient_id: 'a', label: 'A', available: true, connection_check_status: 'connected' })).toBe('connected');
    expect(wechatTargetStatus(bot, { recipient_id: 'b', label: 'B', available: false, connection_check_status: 'unavailable' })).toBe('unavailable');
  });
  it('retains the connecting stage while waiting for a runtime', () => {
    expect(wechatAccountStatus(account({ runtime_status: 'starting' }))).toBe('connecting');
  });
});

describe('WeChat editor account and recipient scope', () => {
  it('shows no account as disconnected, not as a missing recipient', () => {
    expect(wechatEditorStatus({ accounts: [], status: 'unconfigured' })).toBe('disconnected');
    expect(wechatEditorStatus({ accounts: [account({ status: 'disconnected' })] })).toBe('disconnected');
  });
  it('shows an account awaiting activation before recipient guidance', () => {
    const bot = account({ capabilities: { notification_ready: false } });
    expect(wechatEditorStatus({ accounts: [bot], status: 'unconfigured', accountId: bot.id })).toBe('pending');
  });
  it('does not use another account to determine an incomplete task selection', () => {
    const selected = account({ status: 'disconnected' });
    expect(wechatEditorStatus({ accounts: [selected, account({ id: 'other' })], accountId: selected.id })).toBe('disconnected');
    expect(wechatEditorStatus({ accounts: [account({ id: 'other' })], accountId: selected.id })).toBe('disconnected');
  });
  it('does not choose an account or recipient implicitly', () => {
    const accounts = [account()];
    expect(wechatEditorStatus({ accounts })).toBe('unconfigured');
    expect(wechatEditorStatus({ accounts, accountId: 'selected' })).toBe('unconfigured');
  });
  it.each([undefined, 'unconfigured'] as const)('retains a configured recipient without a live check: %s', status => {
    expect(wechatEditorStatus({ accounts: [account()], accountId: 'selected', recipientId: 'a', status })).toBe('connected');
    expect(wechatEditorStatus({ accounts: [account({ status: 'disconnected' })], accountId: 'selected', recipientId: 'a', status })).toBe('disconnected');
  });
  it('uses live results only for confirmed recipients', () => {
    const accounts = [account()];
    expect(wechatEditorStatus({ accounts, accountId: 'selected', status: 'pending' })).toBe('unconfigured');
    expect(wechatEditorStatus({ accounts, accountId: 'selected', recipientId: 'a', status: 'pending' })).toBe('pending');
  });
});


it('merges primary labels without discarding login or temporary connection guidance', () => {
  expect(wechatStatusLabel('reauthorization')).toBe(wechatStatusLabel('disconnected'));
  expect(wechatStatusHint('reauthorization')).toBe('wechatReconnectHint');
  expect(wechatStatusHint('unavailable')).toBe('wechatConnectionUnavailableHint');
});
