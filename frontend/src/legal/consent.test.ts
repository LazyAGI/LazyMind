import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ fetch: vi.fn(), patch: vi.fn() }));
vi.mock('@/modules/user/uiPreferencesApi', () => ({
  fetchUserUiPreferences: api.fetch,
  patchUserUiPreferences: api.patch,
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.resetModules();
  vi.stubEnv('VITE_USER_AGREEMENT_VERSION', 'v0.5.0');
});
afterEach(() => vi.unstubAllEnvs());

describe('v0.5.0 agreement consent', () => {
  it.each(['V0.3', 'v0.3.0', null])('requires confirmation for the saved version %s', async (version) => {
    const { syncUserAgreementFromServer } = await import('./consent');
    api.fetch.mockResolvedValue({ accepted_user_agreement_version: version });
    expect(await syncUserAgreementFromServer()).toBe(false);
    expect(api.patch).not.toHaveBeenCalled();
  });

  it('recognizes server confirmation for v0.5.0', async () => {
    const { syncUserAgreementFromServer } = await import('./consent');
    api.fetch.mockResolvedValue({ accepted_user_agreement_version: 'v0.5.0' });
    expect(await syncUserAgreementFromServer()).toBe(true);
  });

  it('persists v0.5.0 when the user confirms', async () => {
    const { persistUserAgreementAccepted } = await import('./consent');
    api.patch.mockResolvedValue({});
    await persistUserAgreementAccepted();
    expect(api.patch).toHaveBeenCalledWith({ accepted_user_agreement_version: 'v0.5.0' });
  });

  it('uses the configured version for agreement text, saved confirmation and persistence', async () => {
    vi.stubEnv('VITE_USER_AGREEMENT_VERSION', ' v0.6.0 ');
    const consent = await import('./consent');
    const { getUserAgreementMarkdown } = await import('./agreementContent');
    expect(consent.USER_AGREEMENT_VERSION).toBe('v0.6.0');
    for (const language of ['zh-CN', 'en-US']) {
      const agreement = getUserAgreementMarkdown(language);
      expect(agreement).toContain('v0.6.0');
      expect(agreement).not.toContain('v0.5.0');
      expect(agreement).not.toContain('{{USER_AGREEMENT_VERSION}}');
    }
    api.fetch.mockResolvedValueOnce({ accepted_user_agreement_version: 'v0.5.0' });
    expect(await consent.syncUserAgreementFromServer()).toBe(false);
    api.fetch.mockResolvedValueOnce({ accepted_user_agreement_version: 'v0.6.0' });
    expect(await consent.syncUserAgreementFromServer()).toBe(true);
    await consent.persistUserAgreementAccepted();
    expect(api.patch).toHaveBeenCalledWith({ accepted_user_agreement_version: 'v0.6.0' });
  });

  it.each(['', undefined])('uses the default version when configuration is %s', async (version) => {
    vi.stubEnv('VITE_USER_AGREEMENT_VERSION', version);
    const { USER_AGREEMENT_VERSION } = await import('./consent');
    expect(USER_AGREEMENT_VERSION).toBe('v0.5.0');
  });
});
