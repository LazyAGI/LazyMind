import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import TerminalConnectionQuickPanel from './TerminalConnectionQuickPanel';

const mocks = vi.hoisted(() => ({
  listChannelAccounts: vi.fn(),
  startScan: vi.fn(() => Promise.resolve()),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      const copy: Record<string, string> = {
        'channelGateway.terminal.connectedDetailsTitle': '已连接终端账号',
        'channelGateway.terminal.connectedDetailsHint': '点击账号可在下方重新展示对应渠道的二维码。',
        'channelGateway.terminal.refreshAccounts': '刷新列表',
        'channelGateway.terminal.showQr': '展示二维码',
        'channelGateway.terminal.wechatTitle': '微信',
        'channelGateway.terminal.feishuTitle': '飞书',
        'channelGateway.feishu.accountsEmpty': '暂无已连接的飞书账号',
        'channelGateway.wechat.accountStatusMap.connected': '已连接',
        'channelGateway.wechat.runtimeStatusMap.running': '运行中',
      };
      if (key === 'channelGateway.terminal.connectedCount') {
        return `${values?.count} 个已连接`;
      }
      if (key === 'channelGateway.terminal.showAccountQr') {
        return `在下方重新展示${values?.account}的${values?.provider}二维码`;
      }
      return copy[key] || key;
    },
  }),
}));

vi.mock('../api', () => ({
  listChannelAccounts: mocks.listChannelAccounts,
}));

vi.mock('../hooks/useChannelConnection', () => ({
  useChannelConnection: () => ({
    t: (key: string) => key,
    session: null,
    sessionStarting: false,
    actionLoading: false,
    challengeValue: '',
    setChallengeValue: vi.fn(),
    startScan: mocks.startScan,
    refreshQr: vi.fn(),
    submitChallenge: vi.fn(),
  }),
}));

describe('TerminalConnectionQuickPanel', () => {
  beforeEach(() => {
    mocks.listChannelAccounts.mockReset();
    mocks.startScan.mockClear();
    mocks.listChannelAccounts.mockImplementation((provider: string) => Promise.resolve({
      items: provider === 'wechat'
        ? [
          {
            id: 'wechat-connected',
            provider: 'wechat',
            label: '测试微信',
            status: 'connected',
            runtime_status: 'running',
            connected_at: '2026-08-14T08:00:00Z',
            last_poll_at: null,
            last_message_at: null,
            last_error: null,
            updated_at: '2026-08-14T08:00:00Z',
          },
          {
            id: 'wechat-disconnected',
            provider: 'wechat',
            label: '旧微信',
            status: 'disconnected',
            runtime_status: 'stopped',
            connected_at: null,
            last_poll_at: null,
            last_message_at: null,
            last_error: null,
            updated_at: '2026-08-13T08:00:00Z',
          },
        ]
        : [],
    }));
  });

  it('shows account details below the provider tabs and keeps them exclusive with the QR flow', async () => {
    render(<TerminalConnectionQuickPanel onManage={vi.fn()} />);

    const connectedButton = await screen.findByRole('button', { name: '1 个已连接' });
    expect(connectedButton).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(mocks.startScan).toHaveBeenCalledTimes(1));

    fireEvent.click(connectedButton);

    expect(connectedButton).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('测试微信')).toBeInTheDocument();
    expect(screen.queryByText('旧微信')).not.toBeInTheDocument();
    expect(mocks.startScan).toHaveBeenCalledTimes(1);
    const providerTabs = screen.getByRole('tablist');
    const accountDetails = screen.getByRole('region', { name: '已连接终端账号' });
    expect(
      providerTabs.compareDocumentPosition(accountDetails)
      & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    fireEvent.click(screen.getByRole('tab', { name: '飞书' }));
    expect(screen.getByRole('region', { name: '已连接终端账号' })).toBeInTheDocument();
    expect(screen.queryByText('测试微信')).not.toBeInTheDocument();
    expect(screen.getByText('暂无已连接的飞书账号')).toBeInTheDocument();
    expect(mocks.startScan).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('tab', { name: '微信' }));
    expect(screen.getByText('测试微信')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', {
      name: '在下方重新展示测试微信的微信二维码',
    }));
    expect(screen.queryByRole('region', { name: '已连接终端账号' })).not.toBeInTheDocument();
    expect(connectedButton).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(mocks.startScan).toHaveBeenCalledTimes(2));
  });
});
