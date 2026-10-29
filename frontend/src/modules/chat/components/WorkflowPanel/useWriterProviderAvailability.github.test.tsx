import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useWriterProviderAvailability } from './useWriterProviderAvailability';
const list = vi.hoisted(() => vi.fn());
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: { listConnectionsApiAuthserviceV1CloudConnectionsGet: list } }));
beforeEach(() => vi.resetAllMocks());
afterEach(cleanup);

it('distinguishes a connected GitHub account with chat disabled from missing authorization', async () => {
  list.mockResolvedValue({ data: { items: [{ status: 'ACTIVE', can_use_chat: false, provider_options: { chat_enabled: false } }] } });
  const { result } = renderHook(() => useWriterProviderAvailability(['github']));
  await waitFor(() => expect(result.current.states.github).toBe('chat-disabled'));
});

it('refreshes GitHub eligibility after the user enables chat in settings', async () => {
  list.mockResolvedValue({ data: { items: [{ status: 'ACTIVE', can_use_chat: false, provider_options: { chat_enabled: false } }] } });
  const { result } = renderHook(() => useWriterProviderAvailability(['github']));
  await waitFor(() => expect(result.current.states.github).toBe('chat-disabled'));
  list.mockResolvedValue({ data: { items: [{ status: 'ACTIVE', can_use_chat: true, provider_options: { chat_enabled: true } }] } });
  act(() => window.dispatchEvent(new Event('focus')));
  await waitFor(() => expect(result.current.states.github).toBe('ready'));
});

it('does not permit a pending or expired record even if its chat switch is on', async () => {
  list.mockResolvedValue({ data: { items: ['PENDING', 'EXPIRED'].map(status => ({ status, can_use_chat: false, provider_options: { chat_enabled: true } })) } });
  const { result } = renderHook(() => useWriterProviderAvailability(['github']));
  await waitFor(() => expect(result.current.states.github).toBe('authorize'));
});
