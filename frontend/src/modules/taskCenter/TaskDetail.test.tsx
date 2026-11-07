import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Task } from './api';
import TaskDetail from './TaskDetail';
import { getTask, getTaskGoal, listScheduleTasks } from './api';
import { axiosInstance } from '@/components/request';
import { DEVELOPER_ACTIVE_EVENT, isDeveloperModeActive } from '@/utils/developerMode';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';

vi.mock('./api', () => ({ getTask: vi.fn(), getTaskGoal: vi.fn(), listScheduleTasks: vi.fn() }));
vi.mock('@/utils/developerMode', () => ({ DEVELOPER_ACTIVE_EVENT: 'lazymind:developer-active-change', isDeveloperModeActive: vi.fn(() => false) }));
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
  'taskCenter.currentStep': '当前步骤',
  'taskCenter.executionSteps': '执行步骤',
  'taskCenter.moreActions': '更多操作',
  'taskCenter.loadError': '加载失败',
  'taskCenter.noDescription': '暂无任务说明',
  'taskCenter.noSteps': '暂无执行步骤',
  'taskCenter.noTitle': '（无标题）',
  'taskCenter.openConversation': '打开任务对话',
  'taskCenter.statusCompleted': '已完成',
  'taskCenter.statusPending': '待执行',
  'taskCenter.statusSkipped': '已跳过',
  'taskCenter.statusFailed': '执行失败',
  'taskCenter.statusInterrupted': '已中断',
  'taskCenter.statusPaused': '已暂停',
  'taskCenter.statusRunning': '进行中',
  'taskCenter.statusSucceeded': '已完成',
  'taskCenter.statusWaiting': '等待审批',
  'taskCenter.statusWaitingInput': '等待输入',
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
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => Object.entries(options ?? {}).reduce(
      (copy, [name, value]) => copy.split(`{{${name}}}`).join(String(value)),
      translations[key] ?? String(options?.defaultValue ?? key),
    ),
    i18n: { language: 'zh-CN' },
  }),
}));

vi.mock('@/components/request', () => ({
  axiosInstance: { get: vi.fn(), defaults: {} },
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
  useWorkflowStore.setState({ workflowUIByWorkflow: {} });
  vi.mocked(isDeveloperModeActive).mockReset().mockReturnValue(false);
  vi.mocked(getTask).mockReset().mockResolvedValue(task);
  vi.mocked(getTaskGoal).mockReset().mockResolvedValue(undefined);
  vi.mocked(listScheduleTasks).mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 });
  vi.mocked(axiosInstance.get).mockReset().mockResolvedValue({ data: {} });
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('TaskDetail', () => {
  it.each(['paused', 'failed', 'canceled'])('uses the existing empty state instead of spinning forever for a %s task', async status => {
    const current = { ...task, status, workflow_session_id: 'planning-session' };
    vi.mocked(getTask).mockResolvedValue(current);
    vi.mocked(axiosInstance.get).mockImplementation(async (url: string) => {
      if (url.endsWith('/projection')) return { data: { projection: { nodes: {} } } };
      if (url.includes('/workflow-sessions/')) return { data: { session: {
        session_id: 'planning-session', workflow_id: 'ppt', pinned_revision_id: 'planning-revision', steps: [], slots: [],
      } } };
      return { data: { ui: { tab_visibility_ready_material: 'execution_plan', tabs: [{ id: 'analysis', label: '分析', slots: [] }] } } };
    });
    render(<TaskDetail task={current} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    expect(await screen.findByText('暂无执行步骤')).toBeInTheDocument();
    expect(document.querySelector('.task-detail-section .ant-spin')).not.toBeInTheDocument();
    expect(document.querySelector('.task-step-list')).not.toBeInTheDocument();
    expect(screen.queryByText('加载失败')).not.toBeInTheDocument();
  });

  function mockPptWorkflow() {
    const pptTask = { ...task, workflow_session_id: 'ppt-session', steps: [] };
    const stages = [
      ['analysis', 'analyze_requirements', '分析', 'requirement_analysis', 'succeeded'],
      ['materials', 'collect_materials', '素材', 'material_summary', 'succeeded'],
      ['outline', 'build_outline', '大纲', 'deck_outline', 'succeeded'],
      ['page_prompts', 'plan_page_prompts', '每页提示词', 'slide_outline', 'pending'],
      ['slides', 'generate_ppt', '幻灯片', 'preview_html', 'running'],
    ];
    const projection = {
      current_step_id: 'generate_ppt',
      projection: { nodes: Object.fromEntries(stages.map(([, stepId, , , status]) => [stepId, { execution: status === 'pending' ? 'none' : status, branch: 'active' }])) },
      attempt_history: Object.fromEntries(stages.filter(([, , , , status]) => status !== 'pending').map(([, stepId, , , status]) => [stepId, [{ attempt: 1, attempt_id: stepId, task_id: stepId, status, validity: 'effective', started_at: task.created_at }]])),
    };
    vi.mocked(getTask).mockResolvedValue(pptTask);
    vi.mocked(axiosInstance.get).mockImplementation(async (url: string) => {
      if (url.endsWith('/projection')) return { data: projection };
      if (url.includes('/workflow-sessions/')) return { data: { session: {
        session_id: 'ppt-session', workflow_id: 'ppt-workflow', pinned_revision_id: 'ppt-revision',
        current_step_id: '', steps: [], slots: [],
      } } };
      return { data: {
        slots: stages.map(([, , , id]) => ({ id, user_visible: id !== 'slide_outline' })),
        ui: { tabs: stages.map(([id, step_id, label, slot]) => ({ id, step_id, label, slots: [{ id: slot }] })) },
      } };
    });
    return { pptTask, projection };
  }

  it.each([false, true])('matches Chat step visibility in developer mode %s without inventing a skipped attempt', async developer => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(developer);
    const { pptTask } = mockPptWorkflow();
    render(<TaskDetail task={pptTask} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    await screen.findByText('分析');
    expect(Array.from(document.querySelectorAll('.task-step strong')).map(node => node.textContent)).toEqual(
      developer ? ['分析', '素材', '大纲', '每页提示词', '幻灯片'] : ['分析', '素材', '大纲', '幻灯片'],
    );
    if (developer) expect(screen.getByText('每页提示词').closest('.task-step')).toHaveTextContent('待执行');
    expect(screen.getByText('幻灯片').closest('.task-step')).toHaveTextContent('当前步骤');
    expect(screen.getByText('分析').closest('.task-step')).toHaveTextContent('已完成');
    expect(screen.queryByText('已跳过')).not.toBeInTheDocument();
  });

  it('updates the open step list when the same developer-mode event used by Chat changes audience', async () => {
    const { pptTask } = mockPptWorkflow();
    render(<TaskDetail task={pptTask} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await screen.findByText('分析');
    expect(screen.queryByText('每页提示词')).not.toBeInTheDocument();

    vi.mocked(isDeveloperModeActive).mockReturnValue(true);
    act(() => window.dispatchEvent(new CustomEvent(DEVELOPER_ACTIVE_EVENT)));
    expect(await screen.findByText('每页提示词')).toBeInTheDocument();

    vi.mocked(isDeveloperModeActive).mockReturnValue(false);
    act(() => window.dispatchEvent(new CustomEvent(DEVELOPER_ACTIVE_EVENT)));
    await waitFor(() => expect(screen.queryByText('每页提示词')).not.toBeInTheDocument());
    expect(screen.getByText('幻灯片')).toBeInTheDocument();
  });

  it('keeps a visible but bypassed prompt-planning step and labels it as skipped', async () => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(true);
    const { pptTask, projection } = mockPptWorkflow();
    projection.projection.nodes.plan_page_prompts.branch = 'bypassed';
    render(<TaskDetail task={pptTask} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    expect((await screen.findByText('每页提示词')).closest('.task-step')).toHaveTextContent('已跳过');
    expect(document.querySelectorAll('.task-step')).toHaveLength(5);
  });

  it('refreshes authoritative step states with the open task instead of retaining the initial snapshot', async () => {
    vi.useFakeTimers();
    vi.mocked(isDeveloperModeActive).mockReturnValue(true);
    const { pptTask, projection } = mockPptWorkflow();
    render(<TaskDetail task={pptTask} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText('每页提示词').closest('.task-step')).toHaveTextContent('待执行');

    projection.projection.nodes.plan_page_prompts.execution = 'succeeded';
    vi.mocked(getTask).mockResolvedValue({ ...pptTask, updated_at: '2026-09-02T02:10:00Z' });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.getByText('每页提示词').closest('.task-step')).toHaveTextContent('已完成');
    expect(document.querySelectorAll('.task-step')).toHaveLength(5);
  });

  it('uses the same workflow tab labels and effective step statuses as the conversation panel', async () => {
    const workflowTask = { ...task, workflow_session_id: 'workflow-session', status: 'paused', steps: [] };
    vi.mocked(getTask).mockResolvedValue(workflowTask);
    vi.mocked(axiosInstance.get)
      .mockResolvedValueOnce({ data: { data: { projection: { nodes: {} }, graph: { static_order: [] } } } })
      .mockResolvedValueOnce({ data: { data: { session: {
        session_id: 'workflow-session',
        conversation_id: task.conversation_id,
        workflow_id: 'find-skill',
        pinned_revision_id: 'revision-1',
        workflow_mode: 'dynamic',
        status: 'stopped',
        current_step_id: 'search_skillhub',
        created_at: task.created_at,
        updated_at: task.updated_at,
        slots: [{ slot: 'search_plan', selected: true }],
        steps: [
          { id: 'one', session_id: 'workflow-session', step_id: 'extract_search_plan', attempt: 1, task_id: 'one', status: 'succeeded', created_at: task.created_at, updated_at: task.updated_at },
          { id: 'two', session_id: 'workflow-session', step_id: 'search_skillhub', attempt: 1, task_id: 'two', status: 'interrupted', created_at: task.created_at, updated_at: task.updated_at },
        ],
      } } } })
      .mockResolvedValueOnce({ data: { ui: { tabs: [
        { id: 'plan', step_id: 'extract_search_plan', label: '检索计划', slots: [{ id: 'search_plan' }] },
        { id: 'search', step_id: 'search_skillhub', label: 'Search SkillHub', slots: [{ id: 'search_plan' }] },
      ] } } });

    render(<TaskDetail task={workflowTask} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    expect(await screen.findByText('检索计划')).toBeInTheDocument();
    expect(screen.getByText('Search SkillHub')).toBeInTheDocument();
    expect(screen.getByText('检索计划').closest('.task-step')).toHaveTextContent('已完成');
    expect(screen.getByText('Search SkillHub').closest('.task-step')).toHaveTextContent('已暂停');
    expect(screen.getByText('Search SkillHub').closest('.task-step')).toHaveClass('is-current', 'is-focus', 'step-tone-paused');
    expect(screen.getByText('Search SkillHub').closest('.task-step')).toHaveTextContent('当前步骤');
    expect(screen.queryByText('执行步骤 1')).not.toBeInTheDocument();
  });

  it('keeps workflow steps mounted when a refreshed task object points to the same workflow session', async () => {
    const paused = { ...task, workflow_session_id: 'stable-session', status: 'paused', steps: [] };
    vi.mocked(getTask).mockResolvedValue(paused);
    vi.mocked(axiosInstance.get).mockImplementation(async (url: string) => url.endsWith('/projection')
      ? { data: { graph: { static_order: ['active-step'], nodes: { 'active-step': { label: '稳定显示的步骤' } } }, projection: { nodes: { 'active-step': { execution: 'interrupted', branch: 'active' } } } } }
      : { data: {} });
    const props = { onClose: vi.fn(), onOpenConversation: vi.fn() };
    const { rerender } = render(<TaskDetail task={paused} {...props} />);

    expect(await screen.findByText('稳定显示的步骤')).toBeInTheDocument();
    expect(axiosInstance.get).toHaveBeenCalledTimes(2);
    rerender(<TaskDetail task={{ ...paused }} {...props} />);
    await act(async () => { await Promise.resolve(); });

    expect(screen.getByText('稳定显示的步骤')).toBeInTheDocument();
    expect(axiosInstance.get).toHaveBeenCalledTimes(2);
  });

  it.each([
    ['waiting', 'waiting', 'attention'],
    ['waiting_inputs', 'waiting_input', 'input'],
    ['running', 'running', 'running'],
    ['paused', 'interrupted', 'paused'],
    ['failed', 'failed', 'failed'],
    ['failed', 'interrupted', 'failed'],
    ['canceled', 'canceled', 'canceled'],
  ])('colors the focused step by the %s task category', async (taskStatus, stepStatus, tone) => {
    const stateTask = { ...task, status: taskStatus, steps: [{ step_id: 'prepare', status: stepStatus }] };
    vi.mocked(getTask).mockResolvedValue(stateTask);
    render(<TaskDetail task={stateTask} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    const step = (await screen.findByText('准备任务')).closest('.task-step');
    expect(step).toHaveClass('is-focus', `step-tone-${tone}`);
    expect(step).toHaveTextContent('当前步骤');
    if (taskStatus === 'failed') expect(step).toHaveTextContent('执行失败');
    if (taskStatus === 'paused') expect(step).toHaveTextContent('已暂停');
    if (stepStatus === 'waiting_input') expect(step).toHaveTextContent('等待输入');
    expect(step).not.toHaveTextContent('已中断');
  });

  it('does not turn a failed branch into a pause when the workflow is paused', async () => {
    const current = { ...task, status: 'paused', steps: [{ step_id: 'prepare', status: 'failed' }] };
    vi.mocked(getTask).mockResolvedValue(current);
    render(<TaskDetail task={current} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    const step = (await screen.findByText('准备任务')).closest('.task-step');
    expect(step).toHaveClass('is-failed');
    expect(step).not.toHaveClass('step-tone-paused');
    expect(step).toHaveTextContent('执行失败');
  });

  it('keeps completed steps green without applying another task-category tone', async () => {
    const completed = { ...task, status: 'succeeded', steps: [{ step_id: 'prepare', status: 'succeeded' }] };
    vi.mocked(getTask).mockResolvedValue(completed);
    render(<TaskDetail task={completed} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    const step = (await screen.findByText('准备任务')).closest('.task-step');
    expect(step).toHaveClass('is-done');
    expect(step).not.toHaveClass('is-focus');
    expect(step).not.toHaveTextContent('当前步骤');
  });

  it('shows a completed background chat as its truthful single root execution step', async () => {
    const completed = { ...task, task_type: 'background_chat', status: 'succeeded', steps: [] };
    vi.mocked(getTask).mockResolvedValue(completed);
    render(<TaskDetail task={completed} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    const step = (await screen.findAllByText(completed.title!)).find(node => node.closest('.task-step'))?.closest('.task-step');
    expect(step).toHaveClass('is-done');
    expect(step).toHaveTextContent('已完成');
    expect(step).not.toHaveTextContent('当前步骤');
    expect(screen.queryByText('暂无执行步骤')).not.toBeInTheDocument();
  });

  it.each([
    ['writer', 'write_document', '成稿', 'write_flat_document', '短文成稿', 'pruned'],
    ['ppt', 'generate_ppt', '生成幻灯片', 'choose_style', '选择风格', 'bypassed'],
  ])('shows the unexecuted %s branch as skipped rather than pending', async (workflow, completedID, completedLabel, skippedID, skippedLabel, branch) => {
    const completed = { ...task, workflow_session_id: workflow, status: 'succeeded', steps: [] };
    vi.mocked(getTask).mockResolvedValue(completed);
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: {
      graph: {
        static_order: ['__start__', completedID, skippedID, '__end__'],
        nodes: { [completedID]: { label: completedLabel }, [skippedID]: { label: skippedLabel } },
      },
      projection: { nodes: {
        [completedID]: { execution: 'succeeded', branch: 'active' },
        [skippedID]: { execution: 'none', branch },
      } },
    } });
    render(<TaskDetail task={completed} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    expect(await screen.findByText('已跳过')).toBeInTheDocument();
    expect(screen.getByText(skippedLabel).closest('.task-step')).toHaveTextContent('已跳过');
    expect(screen.getByText(skippedLabel).closest('.task-step')).toHaveClass('step-tone-skipped');
    expect(screen.getByText(completedLabel).closest('.task-step')).toHaveTextContent('已完成');
    expect(document.querySelector('.task-detail-status')).toHaveTextContent('已完成');
    expect(document.querySelectorAll('.task-step')).toHaveLength(2);
    expect(screen.queryByText('待执行')).not.toBeInTheDocument();
  });

  it.each([
    ['none', 'active', '待执行'],
    ['pending', 'active', '待执行'],
    ['running', 'active', '进行中'],
    ['running', 'pruned', '进行中'],
    ['succeeded', 'bypassed', '已完成'],
    ['failed', 'bypassed', '执行失败'],
    ['interrupted', 'pruned', '执行失败'],
    ['waiting', 'active', '等待审批'],
  ])('preserves execution %s on branch %s as %s', async (execution, branch, expected) => {
    const current = { ...task, workflow_session_id: 'workflow', steps: [] };
    vi.mocked(getTask).mockResolvedValue(current);
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: {
      graph: { static_order: ['step'], nodes: { step: { label: '测试步骤' } } },
      projection: { nodes: { step: { execution, branch } } },
    } });
    render(<TaskDetail task={current} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

    const label = await screen.findByText('测试步骤');
    expect(label.closest('.task-step')).toHaveTextContent(expected);
    expect(screen.queryByText('已跳过')).not.toBeInTheDocument();
  });

  it.each(['mouseEnter', 'focus'] as const)('shows a full goal on %s without changing the preview', async trigger => {
    render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    const preview = screen.getByText(task.title!);
    expect(preview).toHaveAttribute('tabindex', '0');
    fireEvent[trigger](preview);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(task.title!);
    expect(preview).toHaveTextContent(task.title!);
    expect(getTaskGoal).not.toHaveBeenCalled();
  });

  it('loads the complete original input only when a truncated background goal is opened', async () => {
    const truncated = { ...task, task_type: 'background_chat', title: 'Research...' };
    vi.mocked(getTask).mockResolvedValue(truncated);
    vi.mocked(getTaskGoal).mockResolvedValue('Research the full original task with every requirement');
    render(<TaskDetail task={truncated} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    expect(getTaskGoal).not.toHaveBeenCalled();
    const preview = screen.getByText('Research...');
    fireEvent.mouseEnter(preview);
    await waitFor(() => expect(screen.getByRole('tooltip')).toHaveTextContent('Research the full original task with every requirement'));
    expect(preview).toHaveTextContent('Research...');
    expect(getTaskGoal).toHaveBeenCalledTimes(1);
  });

  it('cancels a pending full-goal lookup when switching tasks and ignores its late result', async () => {
    const truncated = { ...task, task_type: 'background_chat', title: 'Research...' };
    let finish!: (value: string) => void;
    vi.mocked(getTaskGoal).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    vi.mocked(getTask).mockResolvedValue(truncated);
    const { rerender } = render(<TaskDetail task={truncated} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    fireEvent.focus(screen.getByText('Research...'));
    await waitFor(() => expect(getTaskGoal).toHaveBeenCalledTimes(1));
    const signal = vi.mocked(getTaskGoal).mock.calls[0][1];
    const next = { ...task, id: 'next-task', title: 'Another goal' };
    vi.mocked(getTask).mockResolvedValue(next);
    rerender(<TaskDetail task={next} onClose={vi.fn()} onOpenConversation={vi.fn()} />);
    expect(signal.aborted).toBe(true);
    await act(async () => { finish('Previous task full text'); });
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Another goal');
    expect(screen.queryByText('Previous task full text')).not.toBeInTheDocument();
  });

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
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: {
      graph: { static_order: ['prepare', 'custom_api_call'], nodes: { prepare: { label: '准备任务' } } },
      projection: { nodes: { prepare: { execution: 'succeeded', branch: 'active' }, custom_api_call: { execution: 'waiting', branch: 'active' } } },
    } });
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
    await waitFor(() => expect(onDelete).toHaveBeenCalledWith(task));
  });
});
