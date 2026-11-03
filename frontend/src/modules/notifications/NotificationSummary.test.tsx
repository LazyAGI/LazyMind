import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import NotificationSummary from './NotificationSummary';
import type { NotificationConfig, Preferences } from './api';

vi.mock('react-i18next', async original => {
  const { default: locale } = await import('@/i18n/locales/notifications-zh-CN');
  const messages: Record<string, string> = locale;
  const t = (key: string, values: Record<string, unknown> = {}) => (messages[key.replace(/^notifications\./, '')] || key)
    .replace(/\{\{(\w+)\}\}/g, (_match, name: string) => String(values[name] ?? ''));
  return { ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t }) };
});

const taskConfig = (): NotificationConfig => ({
  events: { succeeded: { enabled: true, content: 'summary' }, failed: { enabled: true, content: 'summary' }, waiting: { enabled: false, content: 'summary' } },
  channels: { desktop: { enabled: true }, feishu: { enabled: true, account_id: 'saved-account', recipient_id: 'saved-recipient' } },
});
const preferences = (): Preferences => ({ enabled: true, revision: 4, defaults: taskConfig() });

afterEach(cleanup);

describe('notification summary effective channel count', () => {
  it('counts zero effectively enabled channels while the master gate is off without losing the configured total', () => {
    render(<NotificationSummary value={taskConfig()} preferences={{ ...preferences(), enabled: false }} />);
    expect(screen.getByText(/0 \/ 2 个渠道已开启/)).toBeInTheDocument();
    expect(screen.queryByText(/2 \/ 2 个渠道已开启/)).not.toBeInTheDocument();
  });

  it('counts only desktop when the saved Feishu channel is paused in settings', () => {
    const prefs = preferences();
    prefs.defaults.channels.feishu = { enabled: false };
    render(<NotificationSummary value={taskConfig()} preferences={prefs} />);
    expect(screen.getByText(/1 \/ 2 个渠道已开启/)).toBeInTheDocument();
    expect(screen.getByText(/飞书（设置中未开启）/)).toBeInTheDocument();
  });

  it('restores the effective count after settings change without rewriting the task or its recipient', () => {
    const config = taskConfig();
    const savedConfig = structuredClone(config);
    const paused = preferences();
    paused.defaults.channels.feishu = { enabled: false };
    const { rerender } = render(<NotificationSummary value={config} preferences={paused} />);
    expect(screen.getByText(/1 \/ 2 个渠道已开启/)).toBeInTheDocument();

    rerender(<NotificationSummary value={config} preferences={{ ...preferences(), revision: 5 }} />);

    expect(screen.getByText(/2 \/ 2 个渠道已开启/)).toBeInTheDocument();
    expect(screen.queryByText(/设置中未开启/)).not.toBeInTheDocument();
    expect(config).toEqual(savedConfig);
    expect(config.channels.feishu).toEqual({ enabled: true, account_id: 'saved-account', recipient_id: 'saved-recipient' });
  });

  it('does not count a task channel that is disabled even when its global gate is enabled', () => {
    const config = taskConfig();
    config.channels.feishu!.enabled = false;
    render(<NotificationSummary value={config} preferences={preferences()} />);
    expect(screen.getByText(/1 \/ 2 个渠道已开启/)).toBeInTheDocument();
    expect(screen.queryByText(/设置中未开启/)).not.toBeInTheDocument();
  });

  it('keeps paused channel labels in the compact summary so saved selections remain visible', () => {
    const prefs = preferences();
    prefs.defaults.channels.feishu = { enabled: false };
    render(<NotificationSummary compact value={taskConfig()} preferences={prefs} />);
    expect(screen.getByText(/桌面通知、飞书（设置中未开启）/)).toBeInTheDocument();
  });

  it('reports configured channels rather than claiming they are enabled while preferences are unknown', () => {
    render(<NotificationSummary value={taskConfig()} />);
    expect(screen.getByText(/已配置 2 个渠道/)).toBeInTheDocument();
    expect(screen.queryByText(/已开启/)).not.toBeInTheDocument();
  });
});
