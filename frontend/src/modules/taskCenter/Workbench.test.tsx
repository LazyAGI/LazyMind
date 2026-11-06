import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Task, WorkbenchResponse } from './api';
import Workbench from './Workbench';

const mocks = vi.hoisted(() => ({
  getWorkbench: vi.fn(),
  navigate: vi.fn(),
  requestGet: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({
    i18n: { language: 'zh-CN' },
    t: (key: string, values?: Record<string, unknown>) => key === 'taskCenter.stepsCompleted' ? `${values?.done}/${values?.total} 个步骤已完成` : ({
      'common.retry': '重试',
      'taskCenter.completedLastSevenDays': '近七日完成',
      'taskCenter.empty': '暂无任务',
      'taskCenter.failedDescription': '失败任务',
      'taskCenter.helpingYou': '正在帮你做',
      'taskCenter.helpingYouDescription': '正在执行的任务',
      'taskCenter.loadError': '加载失败',
      'taskCenter.loadErrorDescription': '任务列表暂时无法加载，请稍后重试',
      'taskCenter.needsAttention': '需要我处理',
      'taskCenter.needsAttentionDescription': '等待你的决定',
      'taskCenter.pausedDescription': '暂停任务',
      'taskCenter.refresh': '刷新',
      'taskCenter.recentResults': '最近结果',
      'taskCenter.recentResultsDescription': '最近完成的任务',
      'taskCenter.searchPlaceholder': '搜索任务名称',
      'taskCenter.statusCanceled': '已取消',
      'taskCenter.statusFailed': '失败',
      'taskCenter.statusPaused': '已暂停',
      'taskCenter.summaryHint': '优先显示需要你决定的任务',
      'taskCenter.triggerAll': '触发来源：全部',
      'taskCenter.typeBackgroundChat': '后台对话',
      'taskCenter.typeScheduled': '定时任务',
      'taskCenter.typeWorkflowRun': '工作流任务',
      'taskCenter.viewAction': '查看',
      'taskCenter.viewAll': '查看全部',
    } as Record<string, string>)[key] ?? key,
  }),
}));

vi.mock('react-router-dom', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('@/components/request', () => ({ axiosInstance: { get: mocks.requestGet, defaults: {} }, BASE_URL: '', getLocalizedErrorMessage: () => 'failure' }));
vi.mock('./api', () => ({
  getWorkbench: (...args: unknown[]) => mocks.getWorkbench(...args),
  removeTask: vi.fn(),
}));
vi.mock('./TaskDetail', () => ({
  default: () => null,
  StatusTag: ({ status }: { status: string }) => <span>{status}</span>,
  formatDate: (value?: string) => value ?? '',
}));
vi.mock('@/components/StateGraphModal', () => ({ default: () => null }));

const failedTask: Task = {
  id: 'task-1',
  user_id: 'user-1',
  conversation_id: 'conversation-1',
  conversation_state: 'active',
  task_type: 'workflow_run',
  title: '生成产品说明',
  status: 'failed',
  steps: [],
  created_at: '2026-09-02T02:00:00Z',
  updated_at: '2026-09-02T02:05:00Z',
};

function response(items: Task[], failed = 0): WorkbenchResponse {
  const filtered = {
    attention: items.filter(task => ['waiting', 'waiting_inputs', 'pending'].includes(task.status)),
    running: items.filter(task => task.status === 'running'),
    paused: items.filter(task => task.status === 'paused'),
    failed: items.filter(task => ['failed', 'interrupted'].includes(task.status)),
    recent: items.filter(task => ['succeeded', 'completed'].includes(task.status)),
    canceled: items.filter(task => task.status === 'canceled'),
  };
  return { page_size: 4, groups: Object.fromEntries(Object.entries(filtered).map(([key, tasks]) => [key, {
    items: tasks.slice(0, 4), total: key === 'failed' ? failed || tasks.length : tasks.length, page: 1,
  }])) as WorkbenchResponse['groups'] };
}

describe('Task Center workbench sections', () => {
  it('loads category totals and previews once regardless of historical task count', async () => {
    const older = { ...failedTask, id: 'older-waiting', title: '较早的待审批任务', status: 'waiting' };
    const result = response([older]);
    result.groups.recent.total = 1000;
    mocks.getWorkbench.mockResolvedValue(result);
    render(<Workbench active />);
    expect(await screen.findByText('较早的待审批任务', { selector: 'strong' })).toBeInTheDocument();
    expect(mocks.getWorkbench).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: /需要我处理/ }).querySelector('.metric-count-badge')).toHaveTextContent('1');
    expect(screen.getByRole('button', { name: /近七日完成/ }).querySelector('.metric-count-badge')).toHaveTextContent('1000');
  });

  it('fetches another category page only on demand, retaining global search', async () => {
    const tasks = Array.from({ length: 5 }, (_, i) => ({ ...failedTask, id: `waiting-${i}`, title: `全局 ${i}`, status: 'waiting' }));
    mocks.getWorkbench.mockImplementation(async ({ page, category }) => {
      const result = response(tasks);
      if (page === 2 && category === 'attention') result.groups.attention = { items: tasks.slice(4), total: 5, page: 2 };
      return result;
    });
    render(<Workbench active />);
    fireEvent.change(screen.getByPlaceholderText('搜索任务名称'), { target: { value: '全局' } });
    await screen.findByText('全局 0', { selector: 'strong' });
    expect(screen.queryByText('全局 4', { selector: 'strong' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByTitle('2'));
    expect(await screen.findByText('全局 4', { selector: 'strong' })).toBeInTheDocument();
    expect(mocks.getWorkbench).toHaveBeenLastCalledWith(expect.objectContaining({ keyword: '全局', category: 'attention', page: 2 }));
    expect(screen.queryByText('全局 0', { selector: 'strong' })).not.toBeInTheDocument();
  });

  it('refreshes a completed task title when its conversation is renamed', async () => {
    mocks.getWorkbench.mockResolvedValue(response([failedTask], 1));
    render(<Workbench active />);
    fireEvent.click(screen.getByRole('button', { name: /失败/ }));
    await screen.findByText(failedTask.title!, { selector: "strong" });
    mocks.getWorkbench.mockResolvedValue(response([{ ...failedTask, conversation_title: '新会话名称' }], 1));
    act(() => window.dispatchEvent(new CustomEvent('lazymind:conversation-title-changed', {
      detail: { conversationId: 'conversation-1', displayName: '新会话名称', titleRevision: 1 },
    })));
    expect(await screen.findByText('新会话名称', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.queryByText(failedTask.title!, { selector: "strong" })).not.toBeInTheDocument();
  });
  afterEach(() => cleanup());

  beforeEach(() => {
    mocks.getWorkbench.mockReset();
    mocks.navigate.mockReset();
    mocks.requestGet.mockReset().mockResolvedValue({ data: {} });
  });

  it('renders 1/5 rather than 1/1 on the running card when only one child task exists', async () => {
    const running = { ...failedTask, status: 'running', workflow_session_id: 'session', steps: [{ step_id: 'one', status: 'succeeded' }] };
    mocks.getWorkbench.mockResolvedValue(response([running]));
    mocks.requestGet.mockImplementation(async (url: string) => ({ data: url.endsWith('/projection') ? {
      graph: { static_order: ['one', 'two', 'three', 'four', 'five'] },
      projection: { nodes: { one: { execution: 'succeeded', branch: 'active' }, two: { execution: 'running', branch: 'active' } } },
    } : {} }));
    render(<Workbench active />);
    fireEvent.click(screen.getByRole('button', { name: /正在帮你做/ }));
    expect(await screen.findByText('1/5 个步骤已完成')).toBeInTheDocument();
    expect(screen.queryByText('1/1 个步骤已完成')).not.toBeInTheDocument();
    expect(document.querySelector<HTMLElement>('.workbench-task-card-progress .ant-progress-bg')?.style.width).toBe('20%');
  });


  it('shows one compact empty state instead of empty status sections', async () => {
    mocks.getWorkbench.mockResolvedValue(response([]));
    render(<Workbench active />);

    expect(await screen.findByText('暂无任务')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2 })).not.toBeInTheDocument();
  });

  it('shows a retry action after loading fails', async () => {
    mocks.getWorkbench.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce(response([]));
    render(<Workbench active />);

    expect(await screen.findByText('任务列表暂时无法加载，请稍后重试')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));

    await waitFor(() => expect(mocks.getWorkbench).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('暂无任务')).toBeInTheDocument();
  });

  it('defaults to attention and switches to one selected task category', async () => {
    const waiting = { ...failedTask, id: 'waiting', title: '等待决定', status: 'waiting' };
    const running = { ...failedTask, id: 'running', title: '正在执行', status: 'running' };
    mocks.getWorkbench.mockResolvedValue(response([waiting, running, failedTask], 1));
    render(<Workbench active />);

    expect(await screen.findByText('等待决定', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.queryByText('正在执行', { selector: 'strong' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /需要我处理/ })).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(screen.getByRole('button', { name: /正在帮你做/ }));
    expect(await screen.findByText('正在执行', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.queryByText('等待决定', { selector: 'strong' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /正在帮你做/ })).toHaveAttribute('aria-pressed', 'true');
  });

  it('searches globally across categories instead of keeping the selected category filter', async () => {
    const waiting = { ...failedTask, id: 'waiting', title: '全局等待结果', status: 'waiting' };
    const running = { ...failedTask, id: 'running', title: '全局执行结果', status: 'running' };
    mocks.getWorkbench.mockResolvedValue(response([waiting, running], 0));
    render(<Workbench active />);
    await screen.findByText('全局等待结果', { selector: 'strong' });

    fireEvent.change(screen.getByPlaceholderText('搜索任务名称'), { target: { value: '全局' } });
    expect(await screen.findByText('全局执行结果', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByText('全局等待结果', { selector: 'strong' })).toBeInTheDocument();
    await waitFor(() => expect(mocks.getWorkbench).toHaveBeenLastCalledWith(expect.objectContaining({ keyword: '全局' })));
  });

  it('shows four attention cards by default and renders counts as icon bubbles', async () => {
    const waiting = Array.from({ length: 5 }, (_, index) => ({
      ...failedTask,
      id: `waiting-${index}`,
      title: `等待任务 ${index + 1}`,
      status: 'waiting_inputs',
    }));
    mocks.getWorkbench.mockResolvedValue(response(waiting, 0));
    const { container } = render(<Workbench active />);

    expect(await screen.findByText('等待任务 4', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.queryByText('等待任务 5', { selector: 'strong' })).not.toBeInTheDocument();
    expect(container.querySelectorAll('.task-metric')).toHaveLength(6);
    expect(screen.queryByRole('button', { name: /taskCenter.statusInterrupted/ })).not.toBeInTheDocument();
    expect(container.querySelector('.metric-count-badge')).toHaveTextContent('5');
    expect(container.querySelector('.section-count-badge')).toHaveTextContent('5');
  });


  it('uses the same task card structure for running and recently completed tasks', async () => {
    const running = { ...failedTask, id: 'running-card', title: '执行中的卡片', status: 'running' };
    const completed = { ...failedTask, id: 'recent-card', title: '已完成的卡片', status: 'succeeded', finished_at: new Date().toISOString() };
    mocks.getWorkbench.mockResolvedValue(response([running, completed], 0));
    render(<Workbench active />);
    await screen.findByText('暂无任务');

    fireEvent.click(screen.getByRole('button', { name: /正在帮你做/ }));
    const runningCard = (await screen.findByText('执行中的卡片', { selector: 'strong' })).closest('.attention-task-card');
    expect(runningCard).toHaveClass('workbench-task-card', 'running');

    fireEvent.click(screen.getByRole('button', { name: /近七日完成/ }));
    const recentCard = (await screen.findByText('已完成的卡片', { selector: 'strong' })).closest('.attention-task-card');
    expect(recentCard).toHaveClass('workbench-task-card', 'recent');
    expect(document.querySelector('.recent-task-row')).toBeNull();
  });

  it('hides only overflowing metric labels and restores them after resizing without losing selection or counts', async () => {
    let availableWidth = 70;
    const callbacks = new Set<() => void>();
    const originalRect = HTMLElement.prototype.getBoundingClientRect;
    const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function () {
      return this.classList.contains('metric-icon-wrap') ? new DOMRect(0, 0, 38, 38) : originalRect.call(this);
    });
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function () {
      return this.classList.contains('task-metric') ? availableWidth : 0;
    });
    const textWidth = vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function () {
      return this.classList.contains('metric-label') ? 120 : 0;
    });
    vi.stubGlobal('ResizeObserver', class {
      constructor(private callback: () => void) { callbacks.add(callback); }
      observe() {}
      disconnect() { callbacks.delete(this.callback); }
    });
    try {
      mocks.getWorkbench.mockResolvedValue(response([{ ...failedTask, status: 'waiting' }]));
      render(<Workbench active />);
      const button = await screen.findByRole('button', { name: '需要我处理 1' });
      expect(button).toHaveClass('is-icon-only');
      expect(button).toHaveAttribute('aria-pressed', 'true');
      expect(button.querySelector('.metric-label')).toHaveAttribute('aria-hidden', 'true');
      expect(button.querySelector('.metric-count-badge')).toHaveTextContent('1');
      fireEvent.mouseEnter(button);
      expect(await screen.findByRole('tooltip')).toHaveTextContent('需要我处理');
      fireEvent.click(screen.getByRole('button', { name: /已暂停/ }));
      expect(screen.getByRole('button', { name: /已暂停/ })).toHaveAttribute('aria-pressed', 'true');
      availableWidth = 240;
      act(() => callbacks.forEach(callback => callback()));
      expect(button).not.toHaveClass('is-icon-only');
      expect(button.querySelector('.metric-label')).toHaveAttribute('aria-hidden', 'false');
      availableWidth = 70;
      act(() => callbacks.forEach(callback => callback()));
      expect(button).toHaveClass('is-icon-only');
    } finally {
      cleanup();
      rect.mockRestore(); width.mockRestore(); textWidth.mockRestore();
      vi.unstubAllGlobals();
    }
  });

});
