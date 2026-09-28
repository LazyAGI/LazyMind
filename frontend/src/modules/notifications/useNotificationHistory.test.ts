import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Modal } from 'antd';
import { useNotificationHistory } from './useNotificationHistory';
import type { Attempt } from './api';
const api = vi.hoisted(() => ({ execution: vi.fn(), attempts: vi.fn(), retry: vi.fn() }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getExecutionNotifications: api.execution, getAttempts: api.attempts, retryNotice: api.retry }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
const attempt: Attempt = { notification_id: 'gateway-1', status: 'failed', created_at: '2026-09-22', retry_of: '', retryable: true, attempt_count: 1, payload: { channel: 'feishu', content: 'summary', event: 'succeeded' } };
beforeEach(() => {
  vi.clearAllMocks();
  api.execution.mockResolvedValue({ snapshot: { revision: 1, config: null }, items: [] });
  api.attempts.mockResolvedValue({ items: [attempt], next_cursor: '' });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it('loads both sources and paginates with an opaque cursor', async () => {
  api.attempts.mockResolvedValueOnce({ items: [attempt], next_cursor: '9223372036854775806' }).mockResolvedValueOnce({ items: [{ ...attempt, notification_id: 'next' }], next_cursor: '' });
  const { result } = renderHook(() => useNotificationHistory('task'));
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(api.execution).toHaveBeenCalledWith('task');
  expect(result.current.rows[0].retryDisabled).toBe(true);
  await act(() => result.current.loadMore());
  expect(api.attempts).toHaveBeenLastCalledWith('task', '9223372036854775806');
  expect(result.current.rows).toHaveLength(2);
});
it('retains the idempotency key after a failed retry', async () => {
  api.retry.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({ ...attempt, notification_id: 'retry', retry_of: 'gateway-1' });
  const { result } = renderHook(() => useNotificationHistory('task'));
  await waitFor(() => expect(result.current.loading).toBe(false));
  await act(() => result.current.retry(attempt));
  await act(() => result.current.retry(attempt));
  expect(api.retry.mock.calls[0][1]).toBe(api.retry.mock.calls[1][1]);
  expect(result.current.rows).toHaveLength(2);
});
it('requires confirmation before retrying an unknown delivery', async () => {
  const confirm = vi.spyOn(Modal, 'confirm').mockReturnValue({ destroy: vi.fn(), update: vi.fn() });
  const { result } = renderHook(() => useNotificationHistory('task'));
  await waitFor(() => expect(result.current.loading).toBe(false));
  await act(() => result.current.retry({ ...attempt, status: 'unknown' }));
  expect(api.retry).not.toHaveBeenCalled();
  expect(confirm).toHaveBeenCalledOnce();
  api.retry.mockResolvedValue({ ...attempt, notification_id: 'retry' });
  await act(async () => { await confirm.mock.calls[0][0].onOk?.(); });
  expect(api.retry).toHaveBeenCalledWith('gateway-1', expect.any(String), true);
});
it('resolves legacy source links and suppresses duplicate Core rows', async () => {
  api.execution.mockResolvedValue({ snapshot: { revision: 1, config: null }, items: [{ ...attempt.payload, notification_id: 'core-1', gateway_id: 'gateway-1', created_at: '2026-09-22', status: 'failed' }] });
  api.attempts.mockResolvedValue({ items: [attempt, { ...attempt, notification_id: 'retry', retry_of: 'gateway-1', status: 'sent' }], next_cursor: '' });
  const { result } = renderHook(() => useNotificationHistory('task'));
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.rows).toHaveLength(2);
  expect(result.current.rows[0].retryDisabled).toBe(true);
});
