import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Task } from './api';
import TaskDetail from './TaskDetail';

const translations: Record<string, string> = {
  'common.cancel': '取消',
  'common.close': '关闭',
  'settingsPage.recovery.archiveAction': '归档',
  'settingsPage.recovery.moveToTrash': '移入回收站',
  'taskCenter.conversationUnavailable': '关联对话尚未就绪，暂时无法打开',
  'taskCenter.createdAt': '创建时间',
  'taskCenter.executionSteps': '执行步骤',
  'taskCenter.moreActions': '更多操作',
  'taskCenter.noDescription': '暂无任务说明',
  'taskCenter.noSteps': '暂无执行步骤',
  'taskCenter.noTitle': '（无标题）',
  'taskCenter.openConversation': '打开任务对话',
  'taskCenter.statusCompleted': '已完成',
  'taskCenter.statusRunning': '进行中',
  'taskCenter.statusSucceeded': '已完成',
  'taskCenter.stepFallback': '执行步骤 {{index}}',
  'taskCenter.stepPrepare': '准备任务',
  'taskCenter.taskGoal': '任务目标',
  'taskCenter.trashTask': '移入回收站',
  'taskCenter.trashTaskDescription': '任务和对应会话将保留 30 天',
  'taskCenter.trashTaskTitle': '将“{{name}}”移入回收站？',
  'taskCenter.typeWorkflowRun': '工作流任务',
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

afterEach(() => cleanup());

describe('TaskDetail', () => {
  it('shows localized task metadata and workflow steps without raw identifiers', () => {
    render(<TaskDetail task={task} onClose={vi.fn()} onOpenConversation={vi.fn()} />);

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
