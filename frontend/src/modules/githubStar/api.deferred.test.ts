import { beforeEach, describe, expect, it, vi } from 'vitest';

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: 'https://local-runtime.example.test', axiosInstance: http }));
import * as api from './api';

const publicApi = api as unknown as Record<string, unknown>;
const path = 'https://local-runtime.example.test/api/authservice/v1/cloud/github/star/deferred';
const repository = 'LazyAGI/LazyMind';
const intent = {
  intent_id: 'intent-owner-1', status: 'needs_attention', source: 'local', connection_id: null, account_id: null,
  created_at: '2026-11-17T00:00:00Z', expires_at: '2026-11-24T00:00:00Z', attempt_count: 0,
  next_attempt_at: null, rate_limit_not_before: null, reason: 'binding_not_captured', remote_state: 'unknown',
};

function exported<T>(name: string): T {
  expect(typeof publicApi[name], `${name} must be exported as a callable API`).toBe('function');
  return publicApi[name] as T;
}
function apiResponse(data: unknown, wrapped = true) {
  return { data: wrapped ? { code: 200, message: 'ok', data } : data };
}

beforeEach(() => { vi.resetAllMocks(); });

describe('real deferred support API request wrappers', () => {
  it.each([true, false])('GET uses the fixed AuthService path and unwraps the response (enveloped=%s)', async wrapped => {
    const read = exported<() => Promise<unknown>>('readDeferredGitHubStarIntent');
    const data = { repository, can_receive: true, local_supported: true, accounts: [], intent: null };
    http.get.mockResolvedValue(apiResponse(data, wrapped));
    await expect(read()).resolves.toEqual(data);
    expect(http.get).toHaveBeenCalledTimes(1);
    expect(http.get).toHaveBeenCalledWith(path, expect.objectContaining({ silentError: true }));
    expect(http.post).not.toHaveBeenCalled(); expect(http.delete).not.toHaveBeenCalled(); expect(http.put).not.toHaveBeenCalled();
  });

  it.each([
    { confirmed: true as const, source: 'local' as const },
    { confirmed: true as const, source: 'connection' as const, connection_id: 'conn-alice', expected_account_id: '10001' },
  ])('POST sends only the explicitly confirmed source and preserves acceptance separately from remote success: $source', async body => {
    const accept = exported<(value: typeof body) => Promise<unknown>>('acceptDeferredGitHubStarIntent');
    const acceptedIntent = { ...intent, source: body.source,
      connection_id: 'connection_id' in body ? body.connection_id : null,
      account_id: 'expected_account_id' in body ? body.expected_account_id : null };
    const data = { repository, accepted: true, intent: acceptedIntent };
    http.post.mockResolvedValue(apiResponse(data));
    await expect(accept(body)).resolves.toEqual(data);
    expect(http.post).toHaveBeenCalledTimes(1);
    expect(http.post).toHaveBeenCalledWith(path, body, expect.objectContaining({ silentError: true }));
    expect(http.get).not.toHaveBeenCalled(); expect(http.put).not.toHaveBeenCalled(); expect(http.delete).not.toHaveBeenCalled();
  });

  it('DELETE cancels by intent_id query and returns retained cancelled history without invoking Star PUT', async () => {
    const cancel = exported<(intentId: string) => Promise<unknown>>('cancelDeferredGitHubStarIntent');
    const data = { repository, intent: { ...intent, status: 'cancelled' } };
    http.delete.mockResolvedValue(apiResponse(data));
    await expect(cancel(intent.intent_id)).resolves.toEqual(data);
    expect(http.delete).toHaveBeenCalledTimes(1);
    expect(http.delete).toHaveBeenCalledWith(path, expect.objectContaining({ silentError: true, params: { intent_id: intent.intent_id } }));
    expect(http.get).not.toHaveBeenCalled(); expect(http.post).not.toHaveBeenCalled(); expect(http.put).not.toHaveBeenCalled();
  });

  it.each([
    ['readDeferredGitHubStarIntent', 'get', []],
    ['acceptDeferredGitHubStarIntent', 'post', [{ confirmed: true, source: 'local' }]],
    ['cancelDeferredGitHubStarIntent', 'delete', ['intent-owner-1']],
  ] as const)('%s preserves request failures instead of fabricating an accepted or successful result', async (name, method, args) => {
    const call = exported<(...values: unknown[]) => Promise<unknown>>(name);
    const error = { response: { status: 503, data: { reason: 'unavailable' } } };
    http[method].mockRejectedValue(error);
    await expect(call(...args)).rejects.toBe(error);
    expect(http[method]).toHaveBeenCalledTimes(1);
    expect(http.put).not.toHaveBeenCalled();
  });

  it('exports the agreed notification event and storage key without performing a request', () => {
    expect(publicApi.GITHUB_STAR_SUPPORT_ACCEPTED_EVENT).toBe('lazymind:github-star-support-accepted');
    expect(publicApi.GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY).toBe('lazymind:github-star-support-accepted');
    expect(http.get).not.toHaveBeenCalled(); expect(http.post).not.toHaveBeenCalled();
    expect(http.delete).not.toHaveBeenCalled(); expect(http.put).not.toHaveBeenCalled();
  });
});
