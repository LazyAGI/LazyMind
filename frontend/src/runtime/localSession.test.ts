import { afterEach, expect, it, vi } from 'vitest';
import { AgentAppsAuth } from '@/components/auth';
import { ensureLocalSession } from './localSession';
vi.mock('@/runtime/features', () => ({ runtimeFeatures: { localLikeAutoLogin: true } }));
vi.mock('@/i18n', () => ({ default: { t: (s: string) => s } }));
vi.mock('@/runtime/assistantSession', () => ({ clearLocalAssistantSession: vi.fn() }));
afterEach(() => vi.unstubAllGlobals());
it('a late local-admin recovery cannot overwrite a login that replaced its session', async () => {
  localStorage.clear();
  AgentAppsAuth.setUserInfo({ username: 'A', userId: 'A', token: 'A' });
  let finish!: (value: unknown) => void;
  vi.stubGlobal('fetch', vi.fn(() => new Promise(resolve => { finish = resolve; })));
  const result = ensureLocalSession({ force: true }).catch(error => error);
  AgentAppsAuth.setUserInfo({ username: 'B', userId: 'B', token: 'B' });
  finish({ ok: true, json: async () => ({ username: 'A', userId: 'A', token: 'late-A' }) });
  expect(await result).toBeInstanceOf(Error);
  expect(AgentAppsAuth.getAccessToken()).toBe('B');
});
