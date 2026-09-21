import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useWriterProviderAvailability } from './useWriterProviderAvailability';
const list = vi.hoisted(() => vi.fn());
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: { listConnectionsApiAuthserviceV1CloudConnectionsGet: list } }));
beforeEach(() => vi.clearAllMocks());
it.each([
  ['ACTIVE', true, 'ready'], ['ACTIVE', false, 'authorize'], ['EXPIRED', true, 'authorize'], ['ACTIVE', undefined, 'authorize'],
])('requires active authentication and chat permission (%s, %s)', async (status, chat_enabled, expected) => {
  list.mockResolvedValue({ data: { items: [{ status, provider_options: { chat_enabled } }] } });
  const { result } = renderHook(() => useWriterProviderAvailability(['feishu', 'obsidian']));
  await waitFor(() => expect(result.current.states.feishu).toBe(expected));
  expect(result.current.states.obsidian).toBe('ready');
  expect(list).toHaveBeenCalledTimes(1);
});
it('distinguishes failed checks and ignores a stale response after a provider change', async () => {
  let finish!: (value: unknown) => void;
  list.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockRejectedValueOnce(new Error('offline'));
  const { result, rerender } = renderHook(({ ids }) => useWriterProviderAvailability(ids), { initialProps: { ids: ['feishu'] } });
  rerender({ ids: ['github'] });
  await waitFor(() => expect(result.current.states.github).toBe('failed'));
  await act(async () => finish({ data: { items: [{ status: 'ACTIVE', provider_options: { chat_enabled: true } }] } }));
  expect(result.current.states.feishu).toBeUndefined();
  list.mockResolvedValue({ data: { items: [{ status: 'ACTIVE', provider_options: { chat_enabled: true } }] } });
  await act(async () => result.current.refresh());
  expect(result.current.states.github).toBe('ready');
});
