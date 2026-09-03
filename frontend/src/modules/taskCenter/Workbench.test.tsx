import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Task, TaskListResponse } from './api';
import Workbench, { groupWorkbenchTasks } from './Workbench';

const mocks = vi.hoisted(() => ({
  listTasks: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => ({
      'common.retry': '重试',
      'taskCenter.completedLastSevenDays': '近七日完成',
      'taskCenter.empty': '暂无任务',
      'taskCenter.failedDescription': '失败任务',
      'taskCenter.helpingYou': '正在帮你做',
      'taskCenter.loadError': '加载失败',
      'taskCenter.loadErrorDescription': '任务列表暂时无法加载，请稍后重试',
      'taskCenter.needsAttention': '需要我处理',
      'taskCenter.refresh': '刷新',
      'taskCenter.recentResults': '最近结果',
      'taskCenter.searchPlaceholder': '搜索任务名称',
      'taskCenter.statusCanceled': '已取消',
      'taskCenter.statusFailed': '失败',
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
vi.mock('./api', () => ({
  listTasks: (...args: unknown[]) => mocks.listTasks(...args),
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

function response(items: Task[], failed = 0): TaskListResponse {
  return {
    items,
    total: items.length,
    page: 1,
    page_size: 60,
    status_counts: { all: items.length, pending: 0, waiting: 0, waiting_inputs: 0, running: 0, succeeded: 0, failed, canceled: 0 },
  };
}

describe('Task Center workbench sections', () => {
  afterEach(() => cleanup());

  beforeEach(() => {
    mocks.listTasks.mockReset();
    mocks.navigate.mockReset();
  });

  it('groups only task sections that contain matching tasks', () => {
    const waitingInputTask = { ...failedTask, id: 'task-2', status: 'waiting_inputs' };
    const groups = groupWorkbenchTasks([failedTask, waitingInputTask], Date.parse('2026-09-02T03:00:00Z'));

    expect(groups.failed).toEqual([failedTask]);
    expect(groups.waiting).toEqual([waitingInputTask]);
    expect(groups.running).toHaveLength(0);
    expect(groups.canceled).toHaveLength(0);
    expect(groups.recent).toHaveLength(0);
  });

  it('shows one compact empty state instead of empty status sections', async () => {
    mocks.listTasks.mockResolvedValue(response([]));
    render(<Workbench active onViewAllStatus={vi.fn()} />);

    expect(await screen.findByText('暂无任务')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2 })).not.toBeInTheDocument();
  });

  it('shows a retry action after loading fails', async () => {
    mocks.listTasks.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce(response([]));
    render(<Workbench active onViewAllStatus={vi.fn()} />);

    expect(await screen.findByText('任务列表暂时无法加载，请稍后重试')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));

    await waitFor(() => expect(mocks.listTasks).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('暂无任务')).toBeInTheDocument();
  });
});
