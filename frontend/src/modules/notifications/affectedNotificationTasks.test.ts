import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Task } from '@/modules/taskCenter/api';
import type { NotificationConfig, Preferences } from './api';
import { affectedNotificationTasks } from './affectedNotificationTasks';

const mocks = vi.hoisted(() => ({ tasks: vi.fn(), execution: vi.fn() }));
vi.mock('@/modules/taskCenter/api', () => ({ listTasks: mocks.tasks }));
vi.mock('./api', () => ({ channels: ['desktop', 'feishu', 'wecom', 'wechat'], getExecutionNotifications: mocks.execution }));

function config(enabled: string[] = ['desktop', 'feishu']): NotificationConfig {
  return {
    events: { succeeded: { enabled: true, content: 'summary' }, failed: { enabled: true, content: 'summary' }, waiting: { enabled: false, content: 'summary' } },
    channels: Object.fromEntries(['desktop', 'feishu', 'wecom', 'wechat'].map(channel => [channel, { enabled: enabled.includes(channel) }])),
  };
}
function task(id: string, overrides: Partial<Task> = {}): Task {
  return { id, user_id: 'owner', conversation_id: 'conversation', conversation_state: 'active', task_type: 'scheduled', status: 'running', steps: [], created_at: '2026-10-29', updated_at: '2026-10-29', ...overrides };
}
const preferences = (): Preferences => ({ enabled: true, revision: 4, defaults: config() });

beforeEach(() => {
  vi.resetAllMocks();
  mocks.tasks.mockResolvedValue({ items: [], total: 0 });
  mocks.execution.mockResolvedValue({ snapshot: { config: config(), revision: 1 }, items: [] });
});

describe('affectedNotificationTasks', () => {
  it('includes only supported active desktop tasks and preserves their actual status', async () => {
    mocks.tasks.mockResolvedValue({ items: [
      task('workflow', { task_type: 'workflow_run', status: 'waiting' }),
      task('background', { task_type: 'background_chat', status: 'pending' }),
      task('scheduled', { status: 'waiting_inputs' }),
      task('legacy', { task_type: '', schedule_id: 'old-plan' }),
      task('finished', { status: 'succeeded' }),
      task('canceled', { status: 'canceled' }),
      task('unsupported', { task_type: 'import' }),
      task('external-only'),
    ], total: 8 });
    mocks.execution.mockImplementation(async id => ({ snapshot: { config: id === 'external-only' ? config(['feishu']) : id === 'legacy' ? null : config(), revision: 1 }, items: [] }));

    const result = await affectedNotificationTasks(preferences(), config(['feishu']));

    expect(result.map(({ task, channels }) => [task.id, task.status, channels])).toEqual([
      ['workflow', 'waiting', ['desktop']],
      ['background', 'pending', ['desktop']],
      ['scheduled', 'waiting_inputs', ['desktop']],
      ['legacy', 'running', ['desktop']],
    ]);
    expect(mocks.execution.mock.calls.map(([id]) => id)).toEqual(['scheduled', 'legacy', 'external-only']);
  });

  it('uses only currently enabled global channels when closing the master switch', async () => {
    mocks.tasks.mockResolvedValue({ items: [task('desktop'), task('feishu'), task('paused-channel'), task('disabled'), task('legacy')], total: 5 });
    const configurations: Record<string, NotificationConfig | null> = {
      desktop: config(['desktop']), feishu: config(['feishu']), 'paused-channel': config(['wecom']), disabled: config([]), legacy: null,
    };
    mocks.execution.mockImplementation(async id => ({ snapshot: { config: configurations[id], revision: 1 }, items: [] }));

    const result = await affectedNotificationTasks(preferences());

    expect(result.map(({ task, channels }) => [task.id, channels])).toEqual([
      ['desktop', ['desktop']], ['feishu', ['feishu']], ['legacy', ['desktop']],
    ]);
  });

  it('excludes ordinary and legacy desktop runs when only an external channel is closed', async () => {
    mocks.tasks.mockResolvedValue({ items: [
      task('workflow', { task_type: 'workflow_run', schedule_id: 'plan' }),
      task('background', { task_type: 'background_chat' }),
      task('legacy'), task('scheduled'), task('no-type', { task_type: '', schedule_id: 'old-plan' }),
    ], total: 5 });
    mocks.execution.mockImplementation(async id => ({ snapshot: { config: id === 'legacy' ? null : config(), revision: 1 }, items: [] }));

    const result = await affectedNotificationTasks(preferences(), config(['desktop']));

    expect(result.map(({ task, channels }) => [task.id, channels])).toEqual([['scheduled', ['feishu']], ['no-type', ['feishu']]]);
    expect(mocks.execution).not.toHaveBeenCalledWith('workflow');
    expect(mocks.execution).not.toHaveBeenCalledWith('background');
    expect(mocks.tasks).toHaveBeenCalledWith({ page: 1, page_size: 100 });
  });

  it('does not load tasks when notifications are globally paused', async () => {
    expect(await affectedNotificationTasks({ ...preferences(), enabled: false }, config([]))).toEqual([]);
    expect(mocks.tasks).not.toHaveBeenCalled();
  });

  it('does not report existing runs when only future event defaults change', async () => {
    const next = config();
    next.events.failed.enabled = false;
    expect(await affectedNotificationTasks(preferences(), next)).toEqual([]);
    expect(mocks.tasks).not.toHaveBeenCalled();
  });

  it('does not report a paused desktop channel when closing the master switch', async () => {
    mocks.tasks.mockResolvedValue({ items: [task('workflow', { task_type: 'workflow_run' }), task('legacy'), task('desktop'), task('external')], total: 4 });
    mocks.execution.mockImplementation(async id => ({ snapshot: { config: id === 'legacy' ? null : config([id === 'desktop' ? 'desktop' : 'feishu']), revision: 1 }, items: [] }));
    const result = await affectedNotificationTasks({ ...preferences(), defaults: config(['feishu']) });
    expect(result.map(({ task, channels }) => [task.id, channels])).toEqual([['external', ['feishu']]]);
  });

  it('reads all pages and deduplicates tasks shifted between pages', async () => {
    mocks.tasks.mockResolvedValueOnce({ items: [task('first')], total: 101 })
      .mockResolvedValueOnce({ items: [task('first'), task('last')], total: 101 });
    const result = await affectedNotificationTasks(preferences());
    expect(result.map(({ task }) => task.id)).toEqual(['first', 'last']);
    expect(mocks.execution).toHaveBeenCalledTimes(2);
    expect(mocks.tasks).toHaveBeenNthCalledWith(2, { page: 2, page_size: 100 });
  });

  it('fails instead of returning an incomplete impact list after 100 pages', async () => {
    mocks.tasks.mockResolvedValue({ items: [], total: 10001 });
    await expect(affectedNotificationTasks(preferences())).rejects.toThrow('TOO_MANY_RUNS');
    expect(mocks.tasks).toHaveBeenCalledTimes(100);
  });

  it('propagates snapshot failures rather than treating unknown notification rules as unaffected', async () => {
    mocks.tasks.mockResolvedValue({ items: [task('scheduled')], total: 1 });
    mocks.execution.mockRejectedValue(new Error('unavailable'));
    await expect(affectedNotificationTasks(preferences())).rejects.toThrow('unavailable');
  });
});
