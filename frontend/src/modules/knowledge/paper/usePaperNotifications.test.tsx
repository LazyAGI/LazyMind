import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { usePaperNotifications } from './usePaperNotifications';

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), user: 'owner' }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { get: api.get, post: api.post } }));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getUserInfo: () => ({ userId: api.user }) },
  AUTH_USER_CHANGE_EVENT: 'test-auth-change', AUTH_LOGOUT_EVENT: 'test-auth-logout',
}));
const notice = (dataset = 'papers') => ({ notification_id: 'notice-1', title: '待确认方向', body: '已有真实论文',
  navigation: { type: 'academic_expansion', dataset_id: dataset, expansion_id: 'expansion-1' } });
beforeEach(() => { vi.clearAllMocks(); api.user = 'owner'; api.post.mockResolvedValue({ data: { read: true } }); });

it('loads durable pending work and only navigates when its action is clicked', async () => {
  api.get.mockResolvedValue({ data: { items: [notice()] } });
  const open = vi.fn();
  window.addEventListener('paper-expansion-open', open);
  const { result } = renderHook(() => usePaperNotifications('papers'));
  await waitFor(() => expect(result.current.notifications).toHaveLength(1));
  act(() => { void result.current.notifications[0].action?.onClick(); });
  expect(open).toHaveBeenCalledOnce();
  expect((open.mock.calls[0][0] as CustomEvent).detail).toEqual({ id: 'expansion-1' });
  expect(api.post).not.toHaveBeenCalled();
  act(() => result.current.resolve('persistent:notice-1'));
  expect(result.current.notifications).toHaveLength(0);
  expect(api.post).toHaveBeenCalledWith('/api/core/academic/notifications/notice-1:read', {}, expect.anything());
  window.removeEventListener('paper-expansion-open', open);
});

it('ignores an old collection response after switching records', async () => {
  let oldResponse!: (value: unknown) => void;
  api.get.mockImplementationOnce(() => new Promise(resolve => { oldResponse = resolve; }))
    .mockResolvedValue({ data: { items: [notice('other')] } });
  const { result, rerender } = renderHook(({ dataset }) => usePaperNotifications(dataset), { initialProps: { dataset: 'papers' } });
  rerender({ dataset: 'other' });
  await waitFor(() => expect(result.current.notifications).toHaveLength(1));
  await act(async () => oldResponse({ data: { items: [{ ...notice(), title: 'stale response' }] } }));
  expect(result.current.notifications[0].title).toBe('待确认方向');
});
