import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  ChatEntryDefault,
  ChatEntryDefaults,
  ChatExecutorDescriptor,
} from '@/modules/chat/utils/request';
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
    'layout.newChat': '快速问答',
    'layout.newTask': '新建任务',
    'settingsPage.retry': '重试',
    'settingsPage.tasks.defaultsDescription': '仅影响之后新建的快速问答和任务',
    'settingsPage.tasks.defaultsLoadFailed': '默认配置加载失败',
    'settingsPage.tasks.defaultsLoadFailedDesc': '请检查连接后重试',
    'settingsPage.tasks.defaultsSaveAction': '保存',
    'settingsPage.tasks.defaultsSaveFailed': '默认配置保存失败',
    'settingsPage.tasks.defaultsSaveFailedDesc': '未保存的修改已保留，请重试',
    'settingsPage.tasks.defaultsSaved': '已保存',
    'settingsPage.tasks.defaultsSaving': '正在保存',
    'settingsPage.tasks.defaultsTitle': '默认对话配置',
    'settingsPage.tasks.defaultsUnsaved': '有未保存的修改',
    'settingsPage.tasks.depthHigh': '高',
    'settingsPage.tasks.depthLow': '低',
    'settingsPage.tasks.depthMax': 'Max',
    'settingsPage.tasks.depthMedium': '中',
    'settingsPage.tasks.executorDesc': '选择新对话使用的执行者',
    'settingsPage.tasks.subtaskDefaultDesc': '设置是否默认允许子任务',
    'settingsPage.tasks.subtaskMasterOff': '子任务总开关已关闭',
    'settingsPage.tasks.thinkingDepth': '思考深度',
    'settingsPage.tasks.thinkingDepthDesc': '设置新对话的默认思考深度',
    'settingsPage.tasks.workflowDefaultDesc': '设置新对话的工作流执行方式',
    'settingsPage.tasks.workflowMasterOff': '工作流总开关已关闭',
  };
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string) => translations[key] ?? key,
    }),
  };
});

vi.mock('@/modules/chat/utils/request', async () => {
  const actual = await vi.importActual<typeof import('@/modules/chat/utils/request')>(
    '@/modules/chat/utils/request',
  );
  return {
    ...actual,
    ConversationSettingsApi: () => ({
      getChatSettings: mocks.getChatSettings,
      listChatExecutors: mocks.listChatExecutors,
      patchChatEntryDefault: mocks.patchChatEntryDefault,
    }),
  };
});

const executors: ChatExecutorDescriptor[] = [
  {
    id: 'codex',
    display_name: 'Codex',
    kind: 'external',
    installed: true,
    host_online: true,
    available: true,
  },
];

function entryDefault(
  thinkingDepth: ChatEntryDefault['thinking_depth'],
  overrides: Partial<ChatEntryDefault['conversation_settings']> = {},
): ChatEntryDefault {
  return {
    thinking_depth: thinkingDepth,
    conversation_settings: {
      chat_executor: 'lazymind',
      enable_subagent: true,
      enable_workflow: false,
      workflow_mode: 'dynamic',
      ...overrides,
    },
  };
}

function entryDefaults(): ChatEntryDefaults {
  return {
    quick_question: entryDefault('medium'),
    new_task: entryDefault('high', {
      chat_executor: 'codex',
      enable_subagent: false,
      enable_workflow: true,
      workflow_mode: 'auto',
    }),
  };
}

function mockLoadedDefaults(defaults = entryDefaults()) {
  mocks.getChatSettings.mockResolvedValue({ data: defaults });
  mocks.listChatExecutors.mockResolvedValue({ data: { executors } });
}

function activePanel() {
  const panels = screen.getAllByRole('tabpanel');
  const directFields = panels.find((candidate) =>
    candidate.classList.contains('settings-entry-defaults-fields'),
  );
  if (directFields) return directFields;

  const fields = panels
    .map((candidate) => candidate.querySelector<HTMLElement>('.settings-entry-defaults-fields'))
    .find((candidate): candidate is HTMLElement => candidate != null);
  if (!fields) throw new Error('Task entry defaults panel was not rendered');
  return fields;
}

function expectRadioSelection(name: string, selected = true) {
  const radio = within(activePanel()).getByRole('radio', { name });
  const wrapper = radio.closest('label');
  expect(wrapper).not.toBeNull();
  if (selected) {
    expect(wrapper).toHaveClass('ant-radio-button-wrapper-checked');
  } else {
    expect(wrapper).not.toHaveClass('ant-radio-button-wrapper-checked');
  }
}

describe('TaskEntryDefaults', () => {
  beforeEach(() => {
    mocks.getChatSettings.mockReset();
    mocks.listChatExecutors.mockReset();
    mocks.patchChatEntryDefault.mockReset();
    mocks.patchChatEntryDefault.mockResolvedValue({ data: {} });
    mockLoadedDefaults();
  });

  it('loads two independent profiles and keeps their values isolated while switching tabs', async () => {
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);

    expect(screen.getByRole('region', { name: '默认对话配置' })).toBeInTheDocument();
    const quickTab = await screen.findByRole('tab', { name: '快速问答' });
    const taskTab = screen.getByRole('tab', { name: '新建任务' });
    const saveButton = screen.getByRole('button', { name: /保\s*存/ });
    expect(quickTab).toHaveAttribute('aria-selected', 'true');
    expect(saveButton).toBeDisabled();

    expectRadioSelection('中');
    expectRadioSelection('LazyMind');
    expectRadioSelection('禁用');
    expect(within(activePanel()).getByRole('switch', { name: '允许子任务' })).toBeChecked();

    fireEvent.click(taskTab);

    await waitFor(() => expect(taskTab).toHaveAttribute('aria-selected', 'true'));
    expectRadioSelection('高');
    expectRadioSelection('Codex');
    expectRadioSelection('自动执行');
    expect(within(activePanel()).getByRole('switch', { name: '允许子任务' })).not.toBeChecked();

    fireEvent.click(quickTab);

    await waitFor(() => expect(quickTab).toHaveAttribute('aria-selected', 'true'));
    expectRadioSelection('中');
    expectRadioSelection('禁用');
    expect(mocks.patchChatEntryDefault).not.toHaveBeenCalled();
  });

  it('keeps edits as drafts and saves both changed profiles explicitly', async () => {
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);

    fireEvent.click(await screen.findByRole('radio', { name: '低' }));
    expect(screen.getByText('有未保存的修改')).toBeInTheDocument();
    expect(mocks.patchChatEntryDefault).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('tab', { name: '新建任务' }));
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '新建任务' })).toHaveAttribute(
        'aria-selected',
        'true',
      );
    });
    fireEvent.click(within(activePanel()).getByRole('radio', { name: 'Max' }));
    expect(mocks.patchChatEntryDefault).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));

    await waitFor(() => {
      expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(2);
      expect(mocks.patchChatEntryDefault).toHaveBeenNthCalledWith(
        1,
        'quick_question',
        entryDefault('low'),
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
      expect(mocks.patchChatEntryDefault).toHaveBeenNthCalledWith(
        2,
        'new_task',
        entryDefault('max', {
          chat_executor: 'codex',
          enable_subagent: false,
          enable_workflow: true,
          workflow_mode: 'auto',
        }),
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
    });
    expect(await screen.findByText('已保存')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /保\s*存/ })).toBeDisabled();
  });

  it('keeps a failed draft and retries the unsaved profile update', async () => {
    mocks.patchChatEntryDefault
      .mockRejectedValueOnce(new Error('save failed'))
      .mockResolvedValueOnce({ data: {} });
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);

    fireEvent.click(await screen.findByRole('radio', { name: '低' }));
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));

    expect(await screen.findByText('默认配置保存失败')).toBeInTheDocument();
    expectRadioSelection('低');
    expect(screen.getByRole('button', { name: /保\s*存/ })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));

    await waitFor(() => {
      expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(2);
      expectRadioSelection('低');
    });
    expect(mocks.patchChatEntryDefault).toHaveBeenLastCalledWith(
      'quick_question',
      entryDefault('low'),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(screen.queryByText('默认配置保存失败')).not.toBeInTheDocument();
  });

  it('ignores repeated save actions while a request is pending', async () => {
    let resolveSave: ((value: { data: object }) => void) | undefined;
    mocks.patchChatEntryDefault.mockImplementationOnce(() => new Promise((resolve) => {
      resolveSave = resolve;
    }));
    render(<TaskEntryDefaults subtasksEnabled workflowsEnabled />);

    fireEvent.click(await screen.findByRole('radio', { name: '低' }));
    const saveButton = screen.getByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveButton);
    fireEvent.click(saveButton);

    expect(mocks.patchChatEntryDefault).toHaveBeenCalledTimes(1);
    expect(await screen.findByText('正在保存')).toBeInTheDocument();

    resolveSave?.({ data: {} });
    expect(await screen.findByText('已保存')).toBeInTheDocument();
  });

  it('shows saved workflow and subtask defaults but disables them when master controls are off', async () => {
    const defaults = entryDefaults();
    defaults.quick_question = entryDefault('medium', {
      enable_subagent: true,
      enable_workflow: true,
      workflow_mode: 'auto',
    });
    mockLoadedDefaults(defaults);

    render(<TaskEntryDefaults subtasksEnabled={false} workflowsEnabled={false} />);

    const workflowAuto = await screen.findByRole('radio', { name: '自动执行' });
    const subtaskSwitch = screen.getByRole('switch', { name: '允许子任务' });
    expectRadioSelection('自动执行');
    expect(workflowAuto).toBeDisabled();
    expect(subtaskSwitch).toBeChecked();
    expect(subtaskSwitch).toBeDisabled();
    expect(screen.getByText('工作流总开关已关闭')).toBeInTheDocument();
    expect(screen.getByText('子任务总开关已关闭')).toBeInTheDocument();

    expect(screen.getByRole('radio', { name: '中' })).toBeEnabled();
    expect(screen.getByRole('radio', { name: 'LazyMind' })).toBeEnabled();
    expect(mocks.patchChatEntryDefault).not.toHaveBeenCalled();
  });
});
