import { beforeEach, expect, it, vi } from 'vitest';
import { AgentAppsAuth } from '@/components/auth';
import { axiosInstance } from '@/components/request';
import { writeGitHubStar, writeLocalGitHubStar } from './api';

vi.mock('antd', () => ({ message: { error: vi.fn(), warning: vi.fn() } }));
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key, exists: () => false, language: 'en' } }));
vi.mock('@/runtime/assistantSession', () => ({ clearLocalAssistantSession: vi.fn() }));
vi.mock('@/runtime/localSession', () => ({ isLocalSessionEnabled: () => false, localSessionInitialized: true,
  ensureLocalSession: vi.fn(), restoreLocalSessionAndGetToken: vi.fn() }));

beforeEach(() => localStorage.clear());

it.each([
  ['saved connection', () => writeGitHubStar({ confirmed: true, connection_id: 'saved-account', expected_account_id: '10001' })],
  ['local credential', () => writeLocalGitHubStar({ confirmed: true })],
] as const)('binds the %s confirmation before the request interceptor can observe a replacement login', async (_source, write) => {
  const adapter = vi.fn();
  const originalAdapter = axiosInstance.defaults.adapter;
  axiosInstance.defaults.adapter = adapter;
  try {
    AgentAppsAuth.setUserInfo({ userId: 'owner-A', username: 'A', token: 'fixture-A' });
    const pending = write();
    AgentAppsAuth.setUserInfo({ userId: 'owner-B', username: 'B', token: 'fixture-B' });
    await expect(pending).rejects.toThrow('STALE_AUTH_SESSION');
    expect(adapter).not.toHaveBeenCalled();
  } finally {
    axiosInstance.defaults.adapter = originalAdapter;
  }
});
