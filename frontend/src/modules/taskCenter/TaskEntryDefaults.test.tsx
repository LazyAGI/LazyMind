import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
    'settingsPage.tasks.defaultsSaving': '正在保存',
    'settingsPage.tasks.defaultsSaved': '已自动保存',
    'settingsPage.tasks.defaultsAutosave': '修改后自动保存',
    'settingsPage.tasks.workflowPreferenceNoteTitle': '注意事项',
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
    mocks.listChatExecutors.mockResolvedValue({ data: { data: { executors } } });
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
    expect(screen.queryByRole('button', { name: /保\s*存|恢复默认/ })).not.toBeInTheDocument();
    expect(screen.getByRole('complementary', { name: '注意事项' })).toBeInTheDocument();
    expect(mocks.patchChatEntryDefault).not.toHaveBeenCalled();
  });

  it('automatically saves both entry profiles without a save button', async () => {
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findAllByRole('radio', { name: '低' });
    fireEvent.click(within(card('快速问答默认配置')).getByRole('radio', { name: '低' }));
    fireEvent.click(within(card('新建任务默认配置')).getByRole('radio', { name: 'Max' }));

    await waitFor(() => expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(2));
    expect(mocks.patchChatEntryDefault).toHaveBeenNthCalledWith(1, 'quick_question', entryDefault('low'), expect.anything());
    expect(mocks.patchChatEntryDefault).toHaveBeenNthCalledWith(2, 'new_task', entryDefault('max', {
      chat_executor: 'codex', enable_subagent: false, enable_workflow: true, workflow_mode: 'auto',
    }), expect.anything());
    await screen.findByText('已自动保存');
  });

  it('serializes rapid edits and persists the latest full profile after the pending request', async () => {
    let finish!: () => void;
    mocks.patchChatEntryDefault.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; }));
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findAllByRole('radio', { name: '低' });
    const quick = within(card('快速问答默认配置'));
    fireEvent.click(quick.getByRole('radio', { name: '低' }));
    fireEvent.click(quick.getByRole('radio', { name: '自动执行' }));
    fireEvent.click(quick.getByRole('switch', { name: '允许子任务' }));
    expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(1);
    expect(screen.getByText('正在保存')).toBeInTheDocument();
    await act(async () => finish());
    await screen.findByText('已自动保存');
    expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(2);
    expect(mocks.patchChatEntryDefault).toHaveBeenLastCalledWith('quick_question', entryDefault('low', {
      enable_subagent: false, enable_workflow: true, workflow_mode: 'auto',
    }), expect.anything());
  });

  it('keeps failed changes for explicit retry and does not claim they were saved', async () => {
    mocks.patchChatEntryDefault.mockRejectedValueOnce(new Error('offline'));
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findAllByRole('radio', { name: '低' });
    const quick = within(card('快速问答默认配置'));
    fireEvent.click(quick.getByRole('switch', { name: '允许子任务' }));
    await screen.findByRole('button', { name: /重\s*试/ });
    expect(screen.queryByText('已自动保存')).not.toBeInTheDocument();
    expect(quick.getByRole('switch', { name: '允许子任务' })).not.toBeChecked();
    expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    await screen.findByText('已自动保存');
    expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(2);
    expect(mocks.patchChatEntryDefault).toHaveBeenLastCalledWith('quick_question', entryDefault('medium', { enable_subagent: false }), expect.anything());
  });

  it('keeps successful entries saved if a later entry fails', async () => {
    let finish!: () => void;
    mocks.patchChatEntryDefault.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; }))
      .mockRejectedValueOnce(new Error('offline'));
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findAllByRole('radio', { name: '低' });
    fireEvent.click(within(card('快速问答默认配置')).getByRole('radio', { name: '低' }));
    fireEvent.click(within(card('新建任务默认配置')).getByRole('radio', { name: 'Max' }));
    await act(async () => finish());
    fireEvent.click(await screen.findByRole('button', { name: /重\s*试/ }));
    await screen.findByText('已自动保存');
    expect(mocks.patchChatEntryDefault.mock.calls.map(call => call[0])).toEqual(['quick_question', 'new_task', 'new_task']);
  });

  it('does not overwrite configuration if the initial load fails', async () => {
    mocks.getChatSettings.mockRejectedValueOnce(new Error('offline'));
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);
    await screen.findByText('默认配置加载失败');
    expect(screen.getAllByRole('radio').every(radio => (radio as HTMLInputElement).disabled)).toBe(true);
    expect(mocks.patchChatEntryDefault).not.toHaveBeenCalled();
  });

  it('offers a connection action when an external executor is unavailable', async () => {
    const onConnect = vi.fn();
    mocks.listChatExecutors.mockResolvedValue({ data: { data: { executors: [{ ...executors[0], available: false, host_online: false }] } } });
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled onConnectExecutors={onConnect} />);

    fireEvent.mouseDown((await within(card('新建任务默认配置')).findByRole('combobox')).parentElement as HTMLElement);
    fireEvent.click(await screen.findByRole('button', { name: '去连接助理' }));
    expect(onConnect).toHaveBeenCalledOnce();
  });
});
