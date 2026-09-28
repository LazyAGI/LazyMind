import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Task } from './api';
import TaskDetail from './TaskDetail';
import { getTask, listScheduleTasks } from './api';
import { axiosInstance } from '@/components/request';

vi.mock('./api', () => ({ getTask: vi.fn(), listScheduleTasks: vi.fn() }));
vi.mock('@/modules/notifications/NotificationHistory', () => ({ default: ({ taskId }: { taskId: string }) => <div data-testid='notification-history'>{taskId}</div> }));
vi.mock('@/modules/notifications/ScheduleNotificationPanel', () => ({ default: ({ scheduleId, taskId, showHistory }: { scheduleId: string; taskId: string; showHistory: boolean }) => <button data-schedule={scheduleId} data-task={taskId} data-history={showHistory}>配置通知</button> }));

const translations: Record<string, string> = {
  'common.cancel': '取消',
  'common.close': '关闭',
  'common.retry': '重试',
  'settingsPage.recovery.archiveAction': '归档',
  'settingsPage.recovery.moveToTrash': '移入回收站',
  'taskCenter.conversationUnavailable': '关联对话尚未就绪，暂时无法打开',
  'taskCenter.createdAt': '创建时间',
  'taskCenter.executionSteps': '执行步骤',
  'taskCenter.moreActions': '更多操作',
  'taskCenter.loadError': '加载失败',
  'taskCenter.noDescription': '暂无任务说明',
  'taskCenter.noSteps': '暂无执行步骤',
  'taskCenter.noTitle': '（无标题）',
  'taskCenter.openConversation': '打开任务对话',
  'taskCenter.statusCompleted': '已完成',
  'taskCenter.statusRunning': '进行中',
  'taskCenter.statusSucceeded': '已完成',
  'taskCenter.statusWaiting': '等待审批',
  'taskCenter.stepFallback': '执行步骤 {{index}}',
  'taskCenter.stepPrepare': '准备任务',
  'taskCenter.taskGoal': '任务目标',
  'taskCenter.trashTask': '移入回收站',
  'taskCenter.trashTaskDescription': '任务和对应会话将保留 30 天',
  'taskCenter.trashTaskTitle': '将“{{name}}”移入回收站？',
  'taskCenter.typeWorkflowRun': '工作流任务',
  'taskCenter.schedulePlan': '定时计划',
  'taskCenter.scheduleTriggerPeriod': '触发周期',
  'taskCenter.scheduleDisabled': '已停用',
  'taskCenter.nextRunAt': '下次执行',
  'taskCenter.lastRun': '最近执行',
  'taskCenter.totalExecutions': '累计运行',
  'taskCenter.executionCount': '{{count}} 次',
  'taskCenter.scheduleEdit': '编辑',
  'taskCenter.scheduleDelete': '删除',
  'taskCenter.scheduleDescription': '任务描述',
  'taskCenter.cronDaily': '每天 {{time}}',
  'taskCenter.executionHistory': '执行记录',
  'taskCenter.currentExecution': '当前查看',
  'taskCenter.viewExecution': '查看执行详情',
  'taskCenter.scheduleUnavailable': '关联定时计划不可用',
};

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => Object.entries(options ?? {}).reduce(
      (copy, [name, value]) => copy.split(`{{${name}}}`).join(String(value)),
      translations[key] ?? String(options?.defaultValue ?? key),
    ),
  }),
}));

vi.mock('@/components/request', () => ({
  axiosInstance: { get: vi.fn() },
  BASE_URL: '',
}));

const task: Task = {
  id: 'task-1',
  user_id: 'user-1',
  conversation_id: 'conversation-1',
  conversation_state: 'active',
  conversation_title: '编写玄幻小说',
  task_type: 'workflow_run',
  title: '生成一篇完整的玄幻小说',
  status: 'running',
  steps: [
    { step_id: 'prepare', status: 'succeeded' },
    { step_id: 'custom_api_call', status: 'running' },
  ],
  created_at: '2026-09-02T02:00:00Z',
  updated_at: '2026-09-02T02:05:00Z',
};

beforeEach(() => {
  vi.mocked(getTask).mockReset().mockResolvedValue(task);
  vi.mocked(listScheduleTasks).mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 });
  vi.mocked(axiosInstance.get).mockReset().mockResolvedValue({ data: {} });
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('TaskDetail', () => {
  const scheduled: Task = { ...task, id: 'scheduled-run', task_type: 'scheduled', status: 'succeeded', schedule_id: 'schedule-1', schedule: {
    id: 'schedule-1', name: '每日简报', cron_expr: '0 9 * * *', timezone: 'Asia/Shanghai', enabled: true, run_count: 12,
    last_run_at: '2026-09-24T01:00:00Z', next_run_at: '2026-09-25T01:00:00Z',
  } };
  it('shows the current plan and keeps notification rules tied to the selected execution', async () => {
    const previous = { ...scheduled, id: 'previous-run', conversation_id: 'previous-conversation', conversation_title: '上次简报', created_at: '2026-09-23T01:00:00Z' };
    vi.mocked(getTask).mockImplementation(async id => id === previous.id ? previous : scheduled);
    vi.mocked(listScheduleTasks).mockResolvedValue({ items: [scheduled, previous], total: 2, page: 1, page_size: 5 });
    const openConversation = vi.fn();
    render(<TaskDetail task={scheduled} onClose={vi.fn()} onOpenConversation={openConversation} />);
    expect(await screen.findByText('每天 09:00')).toBeInTheDocument();
    expect(screen.getByText('Asia/Shanghai')).toBeInTheDocument();
    expect(screen.getByText('12 次')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '配置通知' })).toHaveAttribute('data-history', 'false');
    expect(screen.getByRole('button', { name: '配置通知' })).toHaveAttribute('data-task', scheduled.id);
    expect(screen.queryByTestId('notification-history')).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: /查看执行详情/ }));
    await waitFor(() => expect(getTask).toHaveBeenCalledWith(previous.id));
    expect(screen.getByRole('button', { name: '配置通知' })).toHaveAttribute('data-task', previous.id);
    fireEvent.click(screen.getByRole('button', { name: '打开任务对话' }));
    expect(openConversation).toHaveBeenCalledWith(previous.conversation_id);
    expect(listScheduleTasks).toHaveBeenCalledTimes(1);
  });

  it('offers edit and recoverable deletion for a queued scheduled execution', async () => {
    const queued = { ...scheduled, status: 'pending', steps: [] };
    vi.mocked(getTask).mockResolvedValue(queued);
    const onDelete = vi.fn();
    const onOpenConversation = vi.fn();
    render(<TaskDetail task={queued} onClose={vi.fn()} onOpenConversation={onOpenConversation} onDelete={onDelete} />);
    await screen.findByRole('heading', { name: '任务描述' });
    expect(screen.queryByRole('heading', { name: '执行步骤' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '打开任务对话' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '编 辑' }));
    expect(onOpenConversation).toHaveBeenCalledWith(queued.conversation_id);
    fireEvent.click(screen.getByRole('button', { name: /删除/ }));
    expect(onDelete).not.toHaveBeenCalled();
    const confirm = await screen.findByText('任务和对应会话将保留 30 天');
    const dialog = confirm.closest('[role="dialog"]')!;
    fireEvent.click(within(dialog as HTMLElement).getByRole('button', { name: '移入回收站' }));
    await waitFor(() => expect(onDelete).toHaveBeenCalledWith(queued));
  });

  it('does not show a stale next run time for a disabled plan', async () => {
    vi.mocked(getTask).mockResolvedValue({ ...scheduled, schedule: { ...scheduled.schedule!, enabled: false } });
    render(<TaskDetail task={scheduled} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await screen.findAllByText('已停用');
    expect(document.querySelector('.task-schedule-summary')).not.toHaveTextContent('2026/9/25');
  });

  it('preserves execution history when its scheduled plan is unavailable', async () => {
    const missing = { ...scheduled, schedule: undefined };
    vi.mocked(getTask).mockResolvedValue(missing);
    render(<TaskDetail task={missing} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    expect(await screen.findByText('关联定时计划不可用')).toBeInTheDocument();
    expect(screen.getByTestId('notification-history')).toHaveTextContent(missing.id);
    expect(screen.queryByRole('button', { name: '配置通知' })).not.toBeInTheDocument();
    expect(listScheduleTasks).not.toHaveBeenCalled();
  });

  it('refreshes an open completed task after a conversation rename', async () => {
    vi.mocked(getTask).mockResolvedValue({ ...task, status: 'succeeded' });
    render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await screen.findByText('编写玄幻小说');
    vi.mocked(getTask).mockResolvedValue({ ...task, conversation_title: '自定义任务名称', status: 'succeeded' });
    act(() => window.dispatchEvent(new CustomEvent('lazymind:conversation-title-changed', {
      detail: { conversationId: task.conversation_id, displayName: '自定义任务名称', titleRevision: 1 },
    })));
    expect(await screen.findByText('自定义任务名称')).toBeInTheDocument();
    expect(screen.queryByText('编写玄幻小说')).not.toBeInTheDocument();
  });
  it('refreshes a stale selected task and continues polling while the workflow waits', async () => {
    vi.useFakeTimers();
    const initial = { ...task, steps: [] };
    const current = { ...task, workflow_session_id: 'writer', status: 'waiting' };
    vi.mocked(getTask).mockResolvedValueOnce(current).mockResolvedValue({ ...current, status: 'succeeded' });
    render(<TaskDetail task={initial} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await act(async () => { await Promise.resolve(); });
    expect(getTask).toHaveBeenCalledWith(task.id);
    expect(screen.getByText('准备任务')).toBeInTheDocument();
    expect(document.querySelector('.task-detail-status')).toHaveTextContent('等待审批');
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(document.querySelector('.task-detail-status')).toHaveTextContent('已完成');
    const calls = vi.mocked(getTask).mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getTask).toHaveBeenCalledTimes(calls);
  });

  it('ignores a late detail response after switching tasks', async () => {
    let resolveOld!: (value: Task) => void;
    vi.mocked(getTask).mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve; }));
    const next = { ...task, id: 'task-2', conversation_title: '另一个任务', title: '另一个目标' };
    vi.mocked(getTask).mockResolvedValue(next);
    const { rerender } = render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    rerender(<TaskDetail task={next} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await waitFor(() => expect(getTask).toHaveBeenCalledWith(next.id));
    await act(async () => { resolveOld({ ...task, status: 'failed' }); });
    expect(document.querySelector('.task-detail-drawer-title')).toHaveTextContent('另一个任务');
    expect(document.querySelector('.task-detail-status')).toHaveTextContent('进行中');
  });

  it('keeps loaded steps on refresh failure and supports retry', async () => {
    vi.useFakeTimers();
    vi.mocked(getTask).mockResolvedValueOnce(task).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ ...task, status: 'succeeded' });
    render(<TaskDetail task={{ ...task, steps: [] }} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await act(async () => { await Promise.resolve(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.getByRole('alert')).toHaveTextContent('加载失败');
    expect(screen.getByText('准备任务')).toBeInTheDocument();
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /重\s*试/ })); });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(document.querySelector('.task-detail-status')).toHaveTextContent('已完成');
  });

  it('stops polling when the drawer closes', async () => {
    vi.useFakeTimers();
    const { rerender } = render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await act(async () => { await Promise.resolve(); });
    rerender(<TaskDetail task={null} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getTask).toHaveBeenCalledTimes(1);
  });

  it('shows localized task metadata and workflow steps without raw identifiers', async () => {
    render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await act(async () => { await Promise.resolve(); });

    expect(document.querySelector('.task-detail-drawer-title')).toHaveTextContent('工作流任务');
    expect(screen.getByText('准备任务')).toBeInTheDocument();
    expect(screen.getByText('执行步骤 2')).toBeInTheDocument();
    expect(screen.getByText('已完成')).toBeInTheDocument();
    expect(screen.getAllByText('进行中').length).toBeGreaterThan(0);
    expect(screen.queryByText('workflow_run')).not.toBeInTheDocument();
    expect(screen.queryByText('prepare')).not.toBeInTheDocument();
    expect(screen.queryByText('custom_api_call')).not.toBeInTheDocument();

    const footer = document.querySelector<HTMLElement>('.ant-drawer-footer');
    expect(footer).not.toBeNull();
    expect(within(footer!).getAllByRole('button')).toHaveLength(1);
    expect(within(footer!).getByRole('button', { name: '打开任务对话' })).toBeInTheDocument();
    expect(document.querySelector('.task-detail-meta')).not.toBeInTheDocument();
  });

  it('moves archive and trash actions into the header menu', async () => {
    const onArchive = vi.fn();
    const onDelete = vi.fn();
    render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} onArchive={onArchive} onDelete={onDelete} />);

    fireEvent.click(screen.getByRole('button', { name: '更多操作' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: /归档/ }));
    expect(onArchive).toHaveBeenCalledWith(task);

    fireEvent.click(await screen.findByRole('menuitem', { name: /移入回收站/ }));
    await screen.findByText('任务和对应会话将保留 30 天');
    const dialogs = screen.getAllByRole('dialog');
    const dialog = dialogs[dialogs.length - 1];
    fireEvent.click(within(dialog).getByRole('button', { name: '移入回收站' }));
    await waitFor(() => expect(onDelete).toHaveBeenCalledWith(task));
  });
});
