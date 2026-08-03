import { useCallback, useEffect, useRef, useState } from 'react';
import { message } from 'antd';
import { useTranslation } from 'react-i18next';
import { v4 as uuidv4 } from 'uuid';

import {
  cancelConnectionSession,
  createConnectionSession,
  disconnectChannelAccount,
  getConnectionSession,
  listChannelAccounts,
  refreshConnectionSession,
  submitConnectionChallenge,
  type ChannelAccount,
  type ChannelProvider,
  type ConnectionSession,
} from '../api';

const TERMINAL_STATUSES = new Set([
  'connected',
  'expired',
  'canceled',
  'failed',
]);

function getErrorMessage(error: unknown, fallback: string): string {
  if (
    error &&
    typeof error === 'object' &&
    'response' in error &&
    error.response &&
    typeof error.response === 'object' &&
    'data' in error.response
  ) {
    const data = (error.response as { data?: unknown }).data;
    if (data && typeof data === 'object') {
      const detail = (data as { detail?: unknown; message?: unknown }).detail;
      const msg = (data as { message?: unknown }).message;
      if (typeof msg === 'string' && msg.trim()) {
        return msg;
      }
      if (typeof detail === 'string' && detail.trim()) {
        return detail;
      }
      if (Array.isArray(detail) && detail[0] && typeof detail[0] === 'object') {
        const first = detail[0] as { msg?: unknown };
        if (typeof first.msg === 'string' && first.msg.trim()) {
          return first.msg;
        }
      }
    }
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}

export function useChannelConnection(provider: ChannelProvider) {
  const { t } = useTranslation();
  const translationKey = `channelGateway.${provider}`;
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(true);
  const [session, setSession] = useState<ConnectionSession | null>(null);
  const [sessionStarting, setSessionStarting] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);
  const [disconnectingAccountId, setDisconnectingAccountId] = useState<string | null>(null);
  const [challengeValue, setChallengeValue] = useState('');
  const pollTimerRef = useRef<number | null>(null);
  const sessionIdRef = useRef<string | null>(null);
  const mountedRef = useRef(true);

  const clearPollTimer = useCallback(() => {
    if (pollTimerRef.current != null) {
      window.clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const loadAccounts = useCallback(async () => {
    setAccountsLoading(true);
    try {
      const result = await listChannelAccounts(provider);
      if (mountedRef.current) {
        setAccounts(result.items || []);
      }
    } catch (error) {
      if (mountedRef.current) {
        message.error(
          getErrorMessage(error, t(`${translationKey}.loadAccountsFailed`)),
        );
      }
    } finally {
      if (mountedRef.current) {
        setAccountsLoading(false);
      }
    }
  }, [provider, t, translationKey]);

  const applySession = useCallback(
    (next: ConnectionSession | null) => {
      if (!mountedRef.current) {
        return;
      }
      setSession(next);
      sessionIdRef.current = next?.id ?? null;
      if (!next || next.status !== 'verification_required') {
        setChallengeValue('');
      }
    },
    [],
  );

  const schedulePoll = useCallback(
    (sessionId: string, delayMs: number) => {
      clearPollTimer();
      pollTimerRef.current = window.setTimeout(async () => {
        if (!mountedRef.current || sessionIdRef.current !== sessionId) {
          return;
        }
        try {
          const next = await getConnectionSession(sessionId);
          if (!mountedRef.current || sessionIdRef.current !== sessionId) {
            return;
          }
          applySession(next);
          if (next.status === 'connected') {
            message.success(t(`${translationKey}.connectSuccess`));
            await loadAccounts();
            return;
          }
          if (!TERMINAL_STATUSES.has(next.status)) {
            schedulePoll(sessionId, Math.max(500, next.poll_after_ms || 1000));
          }
        } catch (error) {
          if (!mountedRef.current || sessionIdRef.current !== sessionId) {
            return;
          }
          message.error(
            getErrorMessage(error, t(`${translationKey}.pollFailed`)),
          );
          schedulePoll(sessionId, 2000);
        }
      }, delayMs);
    },
    [applySession, clearPollTimer, loadAccounts, t, translationKey],
  );

  const startScan = useCallback(async () => {
    if (sessionStarting) {
      return;
    }
    setSessionStarting(true);
    clearPollTimer();
    try {
      if (sessionIdRef.current) {
        try {
          await cancelConnectionSession(sessionIdRef.current);
        } catch {
          // ignore cancel failures when starting a new session
        }
      }
      const next = await createConnectionSession(provider, {
        idempotencyKey: uuidv4(),
      });
      applySession(next);
      if (next.status === 'connected') {
        message.success(t(`${translationKey}.connectSuccess`));
        await loadAccounts();
        return;
      }
      if (!TERMINAL_STATUSES.has(next.status)) {
        schedulePoll(next.id, Math.max(500, next.poll_after_ms || 1000));
      }
    } catch (error) {
      message.error(
        getErrorMessage(error, t(`${translationKey}.startFailed`)),
      );
    } finally {
      if (mountedRef.current) {
        setSessionStarting(false);
      }
    }
  }, [
    applySession,
    clearPollTimer,
    loadAccounts,
    provider,
    schedulePoll,
    sessionStarting,
    t,
    translationKey,
  ]);

  const cancelScan = useCallback(async () => {
    const sessionId = sessionIdRef.current;
    if (!sessionId || actionLoading) {
      return;
    }
    setActionLoading(true);
    clearPollTimer();
    try {
      await cancelConnectionSession(sessionId);
      applySession(null);
      message.success(t(`${translationKey}.cancelSuccess`));
    } catch (error) {
      message.error(
        getErrorMessage(error, t(`${translationKey}.cancelFailed`)),
      );
    } finally {
      if (mountedRef.current) {
        setActionLoading(false);
      }
    }
  }, [actionLoading, applySession, clearPollTimer, t, translationKey]);

  const disconnectAccount = useCallback(async (accountId: string) => {
    if (disconnectingAccountId) {
      return;
    }
    setDisconnectingAccountId(accountId);
    try {
      await disconnectChannelAccount(accountId);
      message.success(t(`${translationKey}.disconnectSuccess`));
      await loadAccounts();
    } catch (error) {
      message.error(
        getErrorMessage(error, t(`${translationKey}.disconnectFailed`)),
      );
    } finally {
      if (mountedRef.current) {
        setDisconnectingAccountId(null);
      }
    }
  }, [disconnectingAccountId, loadAccounts, t, translationKey]);

  const refreshQr = useCallback(async () => {
    const sessionId = sessionIdRef.current;
    if (!sessionId || actionLoading) {
      return;
    }
    setActionLoading(true);
    clearPollTimer();
    try {
      const next = await refreshConnectionSession(sessionId);
      applySession(next);
      if (!TERMINAL_STATUSES.has(next.status)) {
        schedulePoll(next.id, Math.max(500, next.poll_after_ms || 1000));
      }
    } catch (error) {
      message.error(
        getErrorMessage(error, t(`${translationKey}.refreshFailed`)),
      );
    } finally {
      if (mountedRef.current) {
        setActionLoading(false);
      }
    }
  }, [actionLoading, applySession, clearPollTimer, schedulePoll, t, translationKey]);

  const submitChallenge = useCallback(async () => {
    const sessionId = sessionIdRef.current;
    const value = challengeValue.trim();
    if (!sessionId || !value || actionLoading) {
      return;
    }
    if (!/^\d+$/.test(value)) {
      message.warning(t(`${translationKey}.challengeDigitsOnly`));
      return;
    }
    setActionLoading(true);
    clearPollTimer();
    try {
      const next = await submitConnectionChallenge(sessionId, value);
      applySession(next);
      if (next.status === 'connected') {
        message.success(t(`${translationKey}.connectSuccess`));
        await loadAccounts();
        return;
      }
      if (!TERMINAL_STATUSES.has(next.status)) {
        schedulePoll(next.id, Math.max(500, next.poll_after_ms || 1000));
      }
    } catch (error) {
      message.error(
        getErrorMessage(error, t(`${translationKey}.challengeFailed`)),
      );
      if (sessionIdRef.current) {
        schedulePoll(sessionIdRef.current, 1000);
      }
    } finally {
      if (mountedRef.current) {
        setActionLoading(false);
      }
    }
  }, [
    actionLoading,
    applySession,
    challengeValue,
    clearPollTimer,
    loadAccounts,
    schedulePoll,
    t,
    translationKey,
  ]);

  const closeSessionPanel = useCallback(() => {
    clearPollTimer();
    applySession(null);
  }, [applySession, clearPollTimer]);

  useEffect(() => {
    mountedRef.current = true;
    void loadAccounts();
    return () => {
      mountedRef.current = false;
      clearPollTimer();
      const sessionId = sessionIdRef.current;
      if (sessionId) {
        void cancelConnectionSession(sessionId).catch(() => undefined);
      }
    };
  }, [clearPollTimer, loadAccounts]);

  return {
    t,
    accounts,
    accountsLoading,
    session,
    sessionStarting,
    actionLoading,
    disconnectingAccountId,
    challengeValue,
    setChallengeValue,
    loadAccounts,
    startScan,
    cancelScan,
    disconnectAccount,
    refreshQr,
    submitChallenge,
    closeSessionPanel,
  };
}
