import { beforeEach, expect, it, vi } from 'vitest';
import { getAttempts } from './api';
const http = vi.hoisted(() => ({ request: vi.fn(), defaults: {} }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => vi.clearAllMocks());

it('initial history request omits the cursor or sends zero, never an empty string', async () => {
  const page = { items: [], next_cursor: '', total: 0 };
  http.request.mockResolvedValue({ data: page });
  expect(await getAttempts('task-one')).toEqual(page);
  const url = new URL(http.request.mock.calls[0][0].url, 'http://localhost');
  expect(url.pathname).toBe('/api/channel-gateway/v1/task-notifications');
  expect(url.searchParams.get('task_id')).toBe('task-one');
  expect([null, '0']).toContain(url.searchParams.get('cursor'));
});

it('preserves the server cursor without losing 64-bit precision', async () => {
  http.request.mockResolvedValue({ data: { items: [], next_cursor: '', total: 0 } });
  await getAttempts('task-one', '9223372036854775806');
  const url = new URL(http.request.mock.calls[0][0].url, 'http://localhost');
  expect(url.searchParams.get('cursor')).toBe('9223372036854775806');
});
