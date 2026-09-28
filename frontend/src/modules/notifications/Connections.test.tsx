import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { Modal } from 'antd';
import { TerminalConnectionPage } from '@/modules/channelGateway';
const mocks = vi.hoisted(() => ({ accounts: vi.fn(), create: vi.fn(), detail: vi.fn(), refs: vi.fn(), disconnect: vi.fn(), resume: vi.fn(), cancel: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({ ...await importOriginal<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts, createConnectionSession: mocks.create, disconnectChannelAccount: mocks.disconnect, resumeChannelAccount: mocks.resume, cancelConnectionSession: mocks.cancel }));
vi.mock('./api', async importOriginal => ({ ...await importOriginal<typeof import('./api')>(), getAccountDetail: mocks.detail, getReferences: mocks.refs }));
vi.mock('react-i18next', async importOriginal => { const t = (key: string) => key; return { ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t }) }; });
beforeEach(() => { vi.clearAllMocks(); mocks.accounts.mockResolvedValue({ items: [] }); mocks.cancel.mockResolvedValue(undefined); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('channel connection workspace', () => {
  it('starts WeCom connection through the QR flow without exposing credentials', async () => {
    mocks.create.mockResolvedValue({ id: 'session', provider: 'wecom', mode: 'qr_code', status: 'connected', allowed_actions: [] });
    render(<MemoryRouter><TerminalConnectionPage initialProvider="wecom" /></MemoryRouter>);
    fireEvent.click(await screen.findByRole('button', { name: /startScan/ }));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith('wecom', expect.objectContaining({ idempotencyKey: expect.any(String) })));
    expect(screen.queryByLabelText('BotID')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Secret')).not.toBeInTheDocument();
  });
  it('blocks disconnect when reference lookup fails and reconnects with the original account ID', async () => {
    const account = { id: 'original', provider: 'wecom', label: 'Work account', status: 'connected', runtime_status: 'running', updated_at: '2026-09-17' };
    mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: p === 'wecom' ? [account] : [] }));
    mocks.detail.mockResolvedValue({ ...account, primary_recipient: null, notification_reference_count: 1 });
    mocks.refs.mockRejectedValue(new Error('unavailable'));
    const confirm = vi.spyOn(Modal, 'confirm');
    const view = render(<MemoryRouter><TerminalConnectionPage initialProvider="wecom" /></MemoryRouter>);
    await screen.findByText('Work account');
    const disclosure = document.querySelector('details')!;
    disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
    fireEvent.click(await screen.findByRole('button', { name: 'notifications.disconnect' }));
    await screen.findByText('notifications.loadFailed');
    expect(mocks.disconnect).not.toHaveBeenCalled(); expect(confirm).not.toHaveBeenCalled();
    view.unmount();
    account.status = 'disconnected';
    mocks.resume.mockRejectedValue({ response: { data: { error: { code: 'WECOM_REAUTHORIZATION_REQUIRED' } } } });
    mocks.create.mockResolvedValue({ id: 's', provider: 'wecom', mode: 'qr_code', status: 'connected', allowed_actions: [] });
    render(<MemoryRouter><TerminalConnectionPage initialProvider="wecom" /></MemoryRouter>);
    await screen.findByText('Work account');
    const disconnected = document.querySelector('details')!; disconnected.open = true; fireEvent(disconnected, new Event('toggle'));
    fireEvent.click(await screen.findByRole('button', { name: 'notifications.reconnect' }));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith('wecom', expect.objectContaining({ accountId: 'original' })));
  });

  it('starts WeChat reconnect immediately with the disconnected account ID', async () => {
    const account = { id: 'wechat-original', provider: 'wechat', label: 'WeChat account', status: 'disconnected', runtime_status: 'stopped', updated_at: '2026-09-18' };
    mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: p === 'wechat' ? [account] : [] }));
    mocks.detail.mockResolvedValue({ ...account, primary_recipient: null, notification_reference_count: 0 });
    mocks.refs.mockResolvedValue({ items: [], total: 0, next_cursor: '' });
    mocks.resume.mockRejectedValue({ response: { data: { error: { code: 'WECHAT_REAUTHORIZATION_REQUIRED' } } } });
    mocks.create.mockResolvedValue({ id: 'wechat-session', provider: 'wechat', mode: 'qr_code', status: 'waiting_scan', poll_after_ms: 1000, allowed_actions: ['cancel'] });
    render(<MemoryRouter><TerminalConnectionPage initialProvider="wechat" /></MemoryRouter>);
    await screen.findByText('WeChat account');
    const disclosure = document.querySelector('details')!; disclosure.open = true; fireEvent(disclosure, new Event('toggle'));
    fireEvent.click(await screen.findByRole('button', { name: 'notifications.reconnect' }));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith('wechat', expect.objectContaining({ accountId: 'wechat-original' })));
    expect(screen.getByText('notifications.reconnectPlatform')).toBeInTheDocument();
  });

  it('marks a connected WeChat account as pending activation until a conversation exists', async () => {
    const account = {
      id: 'wechat-pending', provider: 'wechat', label: '微信 ClawBot', status: 'connected',
      runtime_status: 'running', updated_at: '2026-09-21',
      capabilities: { notification_ready: false },
    };
    mocks.accounts.mockImplementation((p: string) => Promise.resolve({ items: p === 'wechat' ? [account] : [] }));
    mocks.detail.mockResolvedValue({ ...account, primary_recipient: null, default_recipient: null, notification_reference_count: 0 });

    render(<MemoryRouter><TerminalConnectionPage initialProvider="wechat" /></MemoryRouter>);

    await screen.findByText('微信 ClawBot');
    expect(screen.getAllByText('notifications.pendingActivation')).toHaveLength(2);
    const providerTab = document.querySelector('.notification-provider-tabs button[aria-pressed="true"]');
    expect(providerTab).toHaveTextContent('notifications.pendingActivation');
    expect(document.querySelector('.notification-account-manager > header')).not.toHaveTextContent('notifications.availableCount');
  });
});
