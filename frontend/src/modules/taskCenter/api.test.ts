import { beforeEach, expect, it, vi } from 'vitest';
import { batchCreateAutomationGroup, cancelSchedule, cancelTask, createAutomationGroup, createSchedule, deleteAutomationGroup, deleteSchedule, enableSchedule, getTask, listAutomationGroups, listSchedules, listScheduleTasks, listTasks, moveSchedule, removeTask, runScheduleNow, updateSchedule } from './api';
const http = vi.hoisted(() => ({ request: vi.fn(), defaults: {} }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => { vi.clearAllMocks(); http.request.mockResolvedValue({ data: {} }); });
it('uses generated request serialization for task filters and opaque IDs', async () => {
  await listTasks({ status: 'running', task_type: 'scheduled', keyword: 'a & b', page: 2, page_size: 10 });
  let request = http.request.mock.calls[0][0];
  const url = new URL(request.url, 'http://localhost');
  expect(Object.fromEntries(url.searchParams)).toEqual({ status: 'running', task_type: 'scheduled', keyword: 'a & b', page: '2', page_size: '10' });
  await getTask('a/b');
  request = http.request.mock.lastCall![0];
  expect(request).toMatchObject({ method: 'GET', url: '/api/core/task-center/tasks/a%2Fb', silentError: true });
  await listScheduleTasks('a/b', 3);
  expect(http.request.mock.lastCall![0].url).toBe('/api/core/task-center/schedules/a%2Fb/tasks?page=3&page_size=10');
});
it('serializes schedule/group bodies through generated contracts', async () => {
  const draft = { name: 'test', cron_expr: '0 8 * * *', timezone: 'UTC', prompt_template: 'hello' };
  await createSchedule(draft);
  expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'POST', url: '/api/core/schedules', data: JSON.stringify(draft) });
  await updateSchedule('a/b', { name: 'new' });
  expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'PUT', url: '/api/core/schedules/a%2Fb', data: JSON.stringify({ name: 'new' }) });
  await createAutomationGroup({ name: 'group' });
  expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'POST', url: '/api/core/automation-groups', data: JSON.stringify({ name: 'group' }), headers: { 'Content-Type': 'application/json' } });
  await moveSchedule('a/b');
  expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'POST', url: '/api/core/schedules/a%2Fb:move', data: JSON.stringify({ group_id: null, position: 0 }) });
  const batch = { group: { name: 'group', timezone: 'UTC' }, tasks: [{ ...draft, client_key: '1' }] };
  await batchCreateAutomationGroup(batch);
  expect(http.request.mock.lastCall![0]).toMatchObject({ url: '/api/core/automation-groups:batch-create', data: JSON.stringify(batch) });
});
it('preserves response bodies and includes disabled schedules when requested', async () => {
  const response = { items: [], total: 0 };
  http.request.mockResolvedValue({ data: response });
  expect(await listSchedules(true)).toEqual(response);
  expect(http.request.mock.lastCall![0].url).toBe('/api/core/schedules?include_disabled=true');
  expect(await listAutomationGroups()).toEqual(response);
});
it.each([
  [cancelTask, '/task-center/tasks/a%2Fb:cancel', 'POST'],
  [removeTask, '/task-center/tasks/a%2Fb:remove', 'POST'],
  [cancelSchedule, '/schedules/a%2Fb:cancel', 'POST'],
  [enableSchedule, '/schedules/a%2Fb:enable', 'POST'],
  [runScheduleNow, '/schedules/a%2Fb:run-now', 'POST'],
  [deleteSchedule, '/schedules/a%2Fb', 'DELETE'],
  [deleteAutomationGroup, '/automation-groups/a%2Fb', 'DELETE'],
] as const)('encodes identifiers for %s', async (call, path, method) => {
  await call('a/b');
  expect(http.request.mock.lastCall![0]).toMatchObject({ method, url: '/api/core' + path });
});
