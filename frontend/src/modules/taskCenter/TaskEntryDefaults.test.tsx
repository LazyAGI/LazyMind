import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ChatEntryDefault, ChatEntryDefaults, ChatExecutorDescriptor } from '@/modules/chat/utils/request';
import TaskEntryDefaults from './TaskEntryDefaults';

const mocks = vi.hoisted(() => ({
  getChatSettings: vi.fn(),
  listChatExecutors: vi.fn(),
  patchChatEntryDefault: vi.fn(),
}));

vi.mock('react-i18next', async () => {
  const actual = await vi.importActual<typeof import('react-i18next')>('react-i18next');
  const translations: Record<string, string> = {
    'chat.conversationConfigEnableSubagent': '允许子任务',
    'chat.conversationConfigExecutor': '对话执行者',
    'chat.conversationConfigExecutorUnavailable': '执行者不可用',
    'chat.conversationConfigWorkflowApproval': '按需审批',
    'chat.conversationConfigWorkflowAuto': '自动执行',
    'chat.conversationConfigWorkflowDisabled': '禁用',
    'chat.conversationConfigWorkflowExecution': '工作流执行方式',
    'settingsPage.retry': '重试',
    'settingsPage.tasks.defaultsTitle': '默认对话配置',
    'settingsPage.tasks.defaultsIntro': '以下两组设置分别作用于不同入口',
    'settingsPage.tasks.defaultsScope': '仅影响新创建的内容',
    'settingsPage.tasks.quickDefaultsTitle': '快速问答默认配置',
    'settingsPage.tasks.quickDefaultsDesc': '快速问答设置',
    'settingsPage.tasks.taskDefaultsTitle': '新建任务默认配置',
    'settingsPage.tasks.taskDefaultsDesc': '新建任务设置',
    'settingsPage.tasks.defaultsRestoreAction': '恢复默认',
    'settingsPage.tasks.defaultsSaveAction': '保存',
    'settingsPage.tasks.defaultsSaving': '正在保存',
    'settingsPage.tasks.defaultsSaved': '已保存',
    'settingsPage.tasks.defaultsUnsaved': '有未保存的修改',
    'settingsPage.tasks.defaultsLoadFailed': '默认配置加载失败',
    'settingsPage.tasks.defaultsLoadFailedDesc': '请重试',
    'settingsPage.tasks.defaultsSaveFailed': '默认配置保存失败',
    'settingsPage.tasks.defaultsSaveFailedDesc': '请重试',
    'settingsPage.tasks.depthLow': '低',
    'settingsPage.tasks.depthMedium': '中',
    'settingsPage.tasks.depthHigh': '高',
    'settingsPage.tasks.depthMax': 'Max',
    'settingsPage.tasks.thinkingDepth': '思考深度',
    'settingsPage.tasks.thinkingDepthDesc': '默认推理深度',
    'settingsPage.tasks.executorDesc': '选择执行者',
    'settingsPage.tasks.executorConnectHint': '外部助理尚未连接',
    'settingsPage.tasks.executorConnectAction': '去连接助理',
    'settingsPage.tasks.executorSelectedUnavailable': '当前助理未连接',
    'settingsPage.tasks.executorLoadFailed': '执行者列表加载失败',
    'settingsPage.tasks.workflowDefaultDesc': '设置工作流',
    'settingsPage.tasks.workflowMasterOff': '工作流总开关已关闭',
    'settingsPage.tasks.subtaskDefaultDesc': '设置子任务',
    'settingsPage.tasks.subtaskMasterOff': '子任务总开关已关闭',
  };
  return { ...actual, useTranslation: () => ({ t: (key: string) => translations[key] ?? key }) };
});

vi.mock('@/modules/chat/utils/request', async () => {
  const actual = await vi.importActual<typeof import('@/modules/chat/utils/request')>('@/modules/chat/utils/request');
  return { ...actual, ConversationSettingsApi: () => ({
    getChatSettings: mocks.getChatSettings,
    listChatExecutors: mocks.listChatExecutors,
    patchChatEntryDefault: mocks.patchChatEntryDefault,
  }) };
});

const entryDefault = (thinkingDepth: ChatEntryDefault['thinking_depth'], overrides = {}): ChatEntryDefault => ({
  thinking_depth: thinkingDepth,
  conversation_settings: {
    chat_executor: 'lazymind', enable_subagent: true, enable_workflow: false, workflow_mode: 'dynamic', ...overrides,
  },
});

const defaults: ChatEntryDefaults = {
  quick_question: entryDefault('medium'),
  new_task: entryDefault('high', { chat_executor: 'codex', enable_subagent: false, enable_workflow: true, workflow_mode: 'auto' }),
};

const executors: ChatExecutorDescriptor[] = [
  { id: 'codex', display_name: 'Codex', kind: 'external', installed: true, host_online: true, available: true },
];

const card = (name: string) => screen.getByRole('heading', { name }).closest('article') as HTMLElement;

describe('TaskEntryDefaults', () => {
  beforeEach(() => {
    mocks.getChatSettings.mockResolvedValue({ data: defaults });
    mocks.listChatExecutors.mockResolvedValue({ data: { executors } });
    mocks.patchChatEntryDefault.mockReset().mockResolvedValue({ data: {} });
  });

  it('shows both independent entry configurations together without nested tabs', async () => {
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findAllByRole('radio', { name: '中' });
    const quick = card('快速问答默认配置');
    const task = card('新建任务默认配置');

    expect(screen.queryByRole('tab', { name: '快速问答' })).not.toBeInTheDocument();
    expect(within(quick).getByRole('radio', { name: '中' })).toBeChecked();
    expect(within(task).getByRole('radio', { name: '高' })).toBeChecked();
    expect(within(quick).getByText('LazyMind')).toBeInTheDocument();
    expect(within(task).getByText('Codex')).toBeInTheDocument();
  });

  it('saves edits from both cards in one action', async () => {
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findAllByRole('radio', { name: '低' });
    fireEvent.click(within(card('快速问答默认配置')).getByRole('radio', { name: '低' }));
    fireEvent.click(within(card('新建任务默认配置')).getByRole('radio', { name: 'Max' }));
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));

    await waitFor(() => expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(2));
    expect(mocks.patchChatEntryDefault).toHaveBeenNthCalledWith(1, 'quick_question', entryDefault('low'), expect.anything());
    expect(mocks.patchChatEntryDefault).toHaveBeenNthCalledWith(2, 'new_task', entryDefault('max', {
      chat_executor: 'codex', enable_subagent: false, enable_workflow: true, workflow_mode: 'auto',
    }), expect.anything());
  });

  it('offers a connection action when an external executor is unavailable', async () => {
    const onConnect = vi.fn();
    mocks.listChatExecutors.mockResolvedValue({ data: { executors: [{ ...executors[0], available: false, host_online: false }] } });
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled onConnectExecutors={onConnect} />);

    fireEvent.mouseDown((await within(card('新建任务默认配置')).findByRole('combobox')).parentElement as HTMLElement);
    fireEvent.click(await screen.findByRole('button', { name: '去连接助理' }));
    expect(onConnect).toHaveBeenCalledOnce();
  });
});
