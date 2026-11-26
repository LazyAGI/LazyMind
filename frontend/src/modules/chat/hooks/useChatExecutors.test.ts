import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatExecutorDescriptor } from '../utils/request';
import { buildExecutorCatalog, notifyChatExecutorsChanged, useChatExecutors } from './useChatExecutors';

const listChatExecutors = vi.hoisted(() => vi.fn());
vi.mock('../utils/request', () => ({ ConversationSettingsApi: () => ({ listChatExecutors }) }));
const host = {
  id: 'deepseek-harness', display_name: 'DeepSeek Harness', kind: 'external',
  available: true, host_online: true, installed: true,
} satisfies ChatExecutorDescriptor;
const response = (executors: ChatExecutorDescriptor[]) => ({ data: { data: { executors } } });

describe('chat executor availability', () => {
  beforeEach(() => { listChatExecutors.mockReset(); });

  it('requires a usable outgoing host, preserves saved IDs, and ignores inbound availability for MCP-only clients', () => {
    const raccoon = { ...host, id: 'raccoon', connected: true };
    const catalog = buildExecutorCatalog([raccoon, { ...host, installed: false }], 'old-provider', 'Unavailable');
    expect(catalog.find(executor => executor.id === raccoon.id)).toMatchObject({
      available: false, unavailable_reason_code: 'inbound_only',
    });
    expect(catalog.find(executor => executor.id === host.id)?.available).toBe(false);
    expect(catalog.find(executor => executor.id === 'old-provider')).toMatchObject({ available: false });
    expect(catalog.find(executor => executor.id === 'lazymind')?.available).toBe(true);
  });

  it('discards an obsolete success after a newer refresh fails, and retries on focus', async () => {
    let finishOld!: (v: ReturnType<typeof response>) => void;
    listChatExecutors.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }))
      .mockRejectedValueOnce(new Error('unavailable'))
      .mockResolvedValueOnce(response([host]));
    const { result, unmount } = renderHook(() => useChatExecutors(true));
    const firstSignal = listChatExecutors.mock.calls[0][0].signal;
    act(notifyChatExecutorsChanged);
    await waitFor(() => expect(result.current.error).toBe(true));
    expect(firstSignal.aborted).toBe(true);
    await act(async () => finishOld(response([host])));
    expect(result.current.executors).toEqual([]);
    act(() => { window.dispatchEvent(new Event('focus')); });
    await waitFor(() => expect(result.current.executors).toEqual([host]));
    expect(result.current.error).toBe(false);
    unmount();
  });

  it('rechecks the host on reopen and clears previously selectable items on error', async () => {
    listChatExecutors.mockResolvedValueOnce(response([host])).mockRejectedValueOnce(new Error('offline'));
    const { result, rerender, unmount } = renderHook(({ open }) => useChatExecutors(open), { initialProps: { open: true } });
    await waitFor(() => expect(result.current.executors).toEqual([host]));
    rerender({ open: false });
    rerender({ open: true });
    await waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.executors).toEqual([]);
    unmount();
  });
});
