import { beforeEach, expect, it, vi } from 'vitest';
import { getTaskRemovalPreview } from './api';
import { TASKS_REMOVED_EVENT } from './events';
import { batchCreateAutomationGroup, cancelSchedule, cancelTask, createAutomationGroup, createSchedule, deleteAutomationGroup, deleteSchedule, enableSchedule, getTask, getTaskGoal, listAutomationGroups, listSchedules, listScheduleTasks, listTasks, moveSchedule, removeTask, runScheduleNow, updateSchedule } from './api';
const http = vi.hoisted(() => ({ request: vi.fn(), defaults: {} }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => { vi.clearAllMocks(); http.request.mockResolvedValue({ data: {} }); });
it('previews encoded task IDs and publishes impact only after successful removal', async () => {
  const signal = new AbortController().signal;
  await getTaskRemovalPreview('a/b', signal);
  expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'GET', url: '/api/core/task-center/tasks/a%2Fb/removal-preview', signal });
  const removed = vi.fn();
  window.addEventListener(TASKS_REMOVED_EVENT, removed);
  try {
    http.request.mockRejectedValueOnce(new Error('failure'));
    await expect(removeTask('a/b', 'version')).rejects.toThrow('failure');
    expect(removed).not.toHaveBeenCalled();
    const impact = { removed_task_ids: ['a/b', 'related'], trashed_conversation_ids: ['conversation'], cancellation_pending: true };
    http.request.mockResolvedValueOnce({ data: impact });
    expect(await removeTask('a/b', 'version')).toEqual(impact);
    expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'POST', url: '/api/core/task-center/tasks/a%2Fb:remove', data: JSON.stringify({ impact_version: 'version' }) });
    expect(removed).toHaveBeenCalledTimes(1);
    expect(removed.mock.calls[0][0].detail).toEqual(impact);
  } finally { window.removeEventListener(TASKS_REMOVED_EVENT, removed); }
});
it('loads the full input for this execution across history pages, preserving second precision', async () => {
  const task = { conversation_id: 'a/b', title: 'Research...', created_at: '2026-09-30T01:57:11.875Z' };
  const controller = new AbortController();
  http.request.mockResolvedValueOnce({ data: { history: [{ query: 'Research later request', create_time: '2026-09-30T02:00:00Z' }], next_page_token: 'older' } });
  http.request.mockResolvedValueOnce({ data: { history: [
    { query: 'Research the complete original goal\nwith all requirements', create_time: '2026-09-30T01:57:11Z' },
    { query: 'Research previous execution', create_time: '2026-09-29T01:57:11Z' },
  ], next_page_token: 'still-older' } });
  expect(await getTaskGoal(task, controller.signal)).toBe('Research the complete original goal\nwith all requirements');
  expect(http.request).toHaveBeenCalledTimes(2);
  expect(http.request.mock.calls[0][0]).toMatchObject({ url: '/api/core/conversations/a%2Fb:history?page_size=50', signal: controller.signal, silentError: true });
  expect(http.request.mock.lastCall![0].url).toContain('page_token=older');
});
it('does not use an unrelated input or an input from a previous execution as the full goal', async () => {
  http.request.mockResolvedValue({ data: { history: [
    { query: 'Different task', create_time: '2026-09-30T01:57:12Z' },
    { query: 'Research previous execution', create_time: '2026-09-29T01:57:11Z' },
  ] } });
  expect(await getTaskGoal({ conversation_id: 'conv', title: 'Research...', created_at: '2026-09-30T01:57:11Z' }, new AbortController().signal)).toBeUndefined();
});
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
