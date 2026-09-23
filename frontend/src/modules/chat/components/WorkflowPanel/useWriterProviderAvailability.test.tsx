import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useWriterProviderAvailability } from './useWriterProviderAvailability';
const list = vi.hoisted(() => vi.fn());
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: { listConnectionsApiAuthserviceV1CloudConnectionsGet: list } }));
beforeEach(() => vi.clearAllMocks());
it.each([
  ['ACTIVE', true, true, 'ready'], ['ACTIVE', false, false, 'chat-disabled'], ['EXPIRED', true, false, 'authorize'],
  ['ACTIVE', true, false, 'authorize'], ['ACTIVE', true, undefined, 'failed'],
])('uses server availability (%s, %s, %s)', async (status, chat_enabled, can_use_chat, expected) => {
  list.mockResolvedValue({ data: { items: [{ status, provider_options: { chat_enabled }, can_use_chat }] } });
  const { result } = renderHook(() => useWriterProviderAvailability(['feishu', 'obsidian']));
  await waitFor(() => expect(result.current.states.feishu).toBe(expected));
  expect(result.current.states.obsidian).toBe('ready');
  expect(list).toHaveBeenCalledTimes(1);
});
it('rechecks Feishu on return from settings and accepts any enabled active connection', async () => {
  list.mockResolvedValue({ data: { items: [{ status: 'ACTIVE', provider_account_meta: { chatEnabled: false }, can_use_chat: false }] } });
  const { result } = renderHook(() => useWriterProviderAvailability(['feishu']));
  await waitFor(() => expect(result.current.states.feishu).toBe('chat-disabled'));
  list.mockResolvedValue({ data: { items: [
    { status: 'ACTIVE', provider_options: { chat_enabled: false }, can_use_chat: false },
    { status: 'ACTIVE', provider_account_meta: { chat_enabled: true }, can_use_chat: true },
  ] } });
  act(() => window.dispatchEvent(new Event('focus')));
  await waitFor(() => expect(result.current.states.feishu).toBe('ready'));
});
it('distinguishes failed checks and ignores a stale response after a provider change', async () => {
  let finish!: (value: unknown) => void;
  list.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockRejectedValueOnce(new Error('offline'));
  const { result, rerender } = renderHook(({ ids }) => useWriterProviderAvailability(ids), { initialProps: { ids: ['feishu'] } });
  rerender({ ids: ['github'] });
  await waitFor(() => expect(result.current.states.github).toBe('failed'));
  await act(async () => finish({ data: { items: [{ status: 'ACTIVE', provider_options: { chat_enabled: true }, can_use_chat: true }] } }));
  expect(result.current.states.feishu).toBeUndefined();
  list.mockResolvedValue({ data: { items: [{ status: 'ACTIVE', provider_options: { chat_enabled: true }, can_use_chat: true }] } });
  await act(async () => result.current.refresh());
  expect(result.current.states.github).toBe('ready');
});

it('requires authorization when no connections exist', async () => {
  list.mockResolvedValue({ data: { items: [] } });
  const { result } = renderHook(() => useWriterProviderAvailability(['feishu']));
  await waitFor(() => expect(result.current.states.feishu).toBe('authorize'));
});
