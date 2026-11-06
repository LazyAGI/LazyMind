import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useUserAgreementConsentGate } from './index';

const mocks = vi.hoisted(() => ({
  sync: vi.fn(),
  isLocal: vi.fn(),
  restore: vi.fn(),
}));

vi.mock('@/legal/consent', () => ({ syncUserAgreementFromServer: mocks.sync }));
vi.mock('@/runtime/localSession', () => ({
  isLocalSessionEnabled: mocks.isLocal,
  restoreLocalSessionAndGetToken: mocks.restore,
}));

beforeEach(() => {
  mocks.sync.mockReset().mockRejectedValueOnce(new Error('Service unavailable'));
  mocks.isLocal.mockReset().mockReturnValue(true);
  mocks.restore.mockReset().mockResolvedValue('renewed-token');
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => vi.restoreAllMocks());

describe('user agreement status retry', () => {
  it('renews the local session before rechecking consent after a service restart', async () => {
    mocks.sync.mockImplementation(async () => {
      if (!mocks.restore.mock.calls.length) throw new Error('Session expired');
      return true;
    });
    const { result } = renderHook(() => useUserAgreementConsentGate(true));
    await waitFor(() => expect(result.current.checkFailed).toBe(true));
    expect(mocks.restore).not.toHaveBeenCalled();

    act(() => result.current.retryCheck());
    await waitFor(() => {
      expect(result.current.loading).toBe(false);
      expect(result.current.checkFailed).toBe(false);
      expect(result.current.needsConsent).toBe(false);
    });
    expect(mocks.restore).toHaveBeenCalledTimes(1);
    expect(mocks.sync).toHaveBeenCalledTimes(2);
  });

  it('keeps retry available if the service cannot renew the local session yet', async () => {
    mocks.restore.mockRejectedValueOnce(new Error('Service unavailable'));
    mocks.sync.mockResolvedValue(false);
    const { result } = renderHook(() => useUserAgreementConsentGate(true));
    await waitFor(() => expect(result.current.checkFailed).toBe(true));

    act(() => result.current.retryCheck());
    await waitFor(() => expect(result.current.checkFailed).toBe(true));
    expect(mocks.sync).toHaveBeenCalledTimes(1);

    act(() => result.current.retryCheck());
    await waitFor(() => expect(result.current.needsConsent).toBe(true));
    expect(mocks.restore).toHaveBeenCalledTimes(2);
  });

  it('rechecks consent directly for a cloud session', async () => {
    mocks.isLocal.mockReturnValue(false);
    mocks.sync.mockResolvedValue(true);
    const { result } = renderHook(() => useUserAgreementConsentGate(true));
    await waitFor(() => expect(result.current.checkFailed).toBe(true));

    act(() => result.current.retryCheck());
    await waitFor(() => expect(result.current.loading || result.current.checkFailed).toBe(false));
    expect(mocks.sync).toHaveBeenCalledTimes(2);
    expect(mocks.restore).not.toHaveBeenCalled();
  });
});
