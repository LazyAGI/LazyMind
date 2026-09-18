import { beforeEach, expect, it, vi } from 'vitest';
import { getAttempts } from './api';
const http = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => vi.clearAllMocks());

it('initial history request omits the cursor or sends zero, never an empty string', async () => {
  const page = { items: [], next_cursor: '', total: 0 };
  http.get.mockResolvedValue({ data: page });
  expect(await getAttempts('task-one')).toEqual(page);
  const [url, options] = http.get.mock.calls[0];
  expect(url).toBe('/api/channel-gateway/v1/task-notifications');
  expect(options.params.task_id).toBe('task-one');
  expect([undefined, 0, '0']).toContain(options.params.cursor);
});

it('preserves the server cursor without losing 64-bit precision', async () => {
  http.get.mockResolvedValue({ data: { items: [], next_cursor: '', total: 0 } });
  await getAttempts('task-one', '9223372036854775806');
  expect(http.get.mock.calls[0][1].params.cursor).toBe('9223372036854775806');
});
