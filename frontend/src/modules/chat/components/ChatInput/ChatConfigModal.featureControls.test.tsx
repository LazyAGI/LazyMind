import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import ChatConfigPopover from './ChatConfigModal';

const mocks = vi.hoisted(() => ({
  fetchUserUiPreferences: vi.fn(),
  getChatSettings: vi.fn(),
  listChatExecutors: vi.fn(),
  patchConversationSettings: vi.fn(),
  onSave: vi.fn(),
}));

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  return {
    ...actual,
    message: { error: vi.fn(), success: vi.fn() },
  };
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => ({
      'chat.conversationConfig': '对话配置',
      'chat.conversationConfigExecutor': '对话执行者',
      'chat.conversationConfigExecutorTooltip': '执行者说明',
      'chat.conversationConfigExecutorLazyMindDesc': '使用 LazyMind 内置 ChatAgent。',
      'chat.conversationConfigExecutorUnavailable': '执行者不可用',
      'chat.conversationConfigWorkflowExecution': '工作流执行方式',
      'chat.conversationConfigWorkflowExecutionTooltip': '工作流说明',
      'chat.conversationConfigWorkflowExecutionDesc': '工作流执行说明',
      'chat.conversationConfigWorkflowAuto': '自动执行',
      'chat.conversationConfigWorkflowApproval': '按需审批',
      'chat.conversationConfigWorkflowDisabled': '禁用',
      'chat.conversationConfigEnableSubagent': '允许子任务',
      'chat.conversationConfigEnableSubagentTooltip': '子任务说明',
      'chat.conversationConfigFeatureControlsLoading': '正在读取任务中心状态…',
      'chat.conversationConfigFeatureControlsUnavailable': '任务中心状态不可用',
      'chat.conversationConfigTaskCenterDisabled': '任务中心已关闭，子任务暂不可用。',
      'chat.conversationConfigWorkflowMasterDisabled': '工作流总开关已关闭。',
    }[key] ?? key),
  }),
}));

vi.mock('../../utils/request', () => ({
  ChatServiceApi: () => ({
    conversationServiceGetConversationDetail: vi.fn(),
  }),
  ConversationSettingsApi: () => ({
    getChatSettings: mocks.getChatSettings,
    listChatExecutors: mocks.listChatExecutors,
    patchConversationSettings: mocks.patchConversationSettings,
  }),
  parseConversationRuntimeSettings: vi.fn(),
}));

vi.mock('@/modules/user/uiPreferencesApi', () => ({
  USER_UI_PREFERENCES_CHANGED_EVENT: 'lazymind:user-ui-preferences-changed',
  fetchUserUiPreferences: mocks.fetchUserUiPreferences,
}));

describe('ChatConfigPopover feature control independence', () => {
  beforeEach(() => {
    mocks.fetchUserUiPreferences.mockReset();
    mocks.fetchUserUiPreferences.mockResolvedValue({
      task_center_enabled: false,
      workflows_enabled: false,
    });
    mocks.getChatSettings.mockReset();
    mocks.getChatSettings.mockResolvedValue({ data: { data: {} } });
    mocks.listChatExecutors.mockReset();
    mocks.listChatExecutors.mockResolvedValue({ data: { data: { executors: [] } } });
    mocks.patchConversationSettings.mockReset();
    mocks.onSave.mockReset();
  });

  it('disables only subtasks when the task center is off and workflows are on', async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({
      task_center_enabled: false,
      workflows_enabled: true,
    });

    render(
      <ChatConfigPopover
        initialSettings={{
          enable_workflow: true,
          workflow_mode: 'auto',
          enable_subagent: true,
        }}
        onSave={mocks.onSave}
      />,
    );

    fireEvent.click(screen.getByText('对话配置'));

    const taskControlsNotice = await screen.findByText('任务中心已关闭，子任务暂不可用。');
    const subagentSwitch = screen.getByRole('switch', { name: '允许子任务' });
    expect(subagentSwitch).toBeDisabled();
    expect(subagentSwitch).not.toBeChecked();
    expect(subagentSwitch).toHaveAttribute('aria-describedby', taskControlsNotice.id);

    const workflowControl = screen.getByLabelText('工作流执行方式');
    expect(workflowControl).not.toHaveAttribute('aria-describedby');
    expect(
      within(workflowControl).getAllByRole('radio').every(
        (radio) => !radio.hasAttribute('disabled'),
      ),
    ).toBe(true);
    expect(within(workflowControl).getByRole('radio', { name: '自动执行' })).toBeChecked();
    expect(screen.queryByText('工作流总开关已关闭。')).not.toBeInTheDocument();
    expect(mocks.onSave).not.toHaveBeenCalled();
    expect(mocks.patchConversationSettings).not.toHaveBeenCalled();
  });

  it('disables only workflows when the workflow master is off and the task center is on', async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({
      task_center_enabled: true,
      workflows_enabled: false,
    });

    render(
      <ChatConfigPopover
        initialSettings={{
          enable_workflow: true,
          workflow_mode: 'auto',
          enable_subagent: true,
        }}
        onSave={mocks.onSave}
      />,
    );

    fireEvent.click(screen.getByText('对话配置'));

    const workflowControlsNotice = await screen.findByText('工作流总开关已关闭。');
    const subagentSwitch = screen.getByRole('switch', { name: '允许子任务' });
    expect(subagentSwitch).toBeEnabled();
    expect(subagentSwitch).toBeChecked();
    expect(subagentSwitch).not.toHaveAttribute('aria-describedby');

    const workflowControl = screen.getByLabelText('工作流执行方式');
    expect(workflowControl).toHaveAttribute('aria-describedby', workflowControlsNotice.id);
    expect(
      within(workflowControl).getAllByRole('radio').every(
        (radio) => radio.hasAttribute('disabled'),
      ),
    ).toBe(true);
    expect(within(workflowControl).getByRole('radio', { name: '禁用' })).toBeChecked();
    expect(screen.queryByText('任务中心已关闭，子任务暂不可用。')).not.toBeInTheDocument();
    expect(mocks.onSave).not.toHaveBeenCalled();
    expect(mocks.patchConversationSettings).not.toHaveBeenCalled();
  });

  it('updates master availability without overwriting saved chat choices', async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({
      task_center_enabled: true,
      workflows_enabled: true,
    });

    render(
      <ChatConfigPopover
        initialSettings={{
          enable_workflow: true,
          workflow_mode: 'auto',
          enable_subagent: true,
        }}
        onSave={mocks.onSave}
      />,
    );

    fireEvent.click(screen.getByText('对话配置'));

    const subagentSwitch = screen.getByRole('switch', { name: '允许子任务' });
    const workflowControl = screen.getByLabelText('工作流执行方式');
    const autoMode = within(workflowControl).getByRole('radio', { name: '自动执行' });
    const disabledMode = within(workflowControl).getByRole('radio', { name: '禁用' });

    await waitFor(() => expect(subagentSwitch).toBeEnabled());
    expect(subagentSwitch).toBeChecked();
    expect(autoMode).toBeChecked();

    act(() => {
      window.dispatchEvent(new CustomEvent('lazymind:user-ui-preferences-changed', {
        detail: { task_center_enabled: false, workflows_enabled: false },
      }));
    });

    await waitFor(() => expect(subagentSwitch).toBeDisabled());
    expect(subagentSwitch).not.toBeChecked();
    expect(disabledMode).toBeChecked();

    act(() => {
      window.dispatchEvent(new CustomEvent('lazymind:user-ui-preferences-changed', {
        detail: { task_center_enabled: true, workflows_enabled: false },
      }));
    });

    await waitFor(() => expect(subagentSwitch).toBeEnabled());
    expect(subagentSwitch).toBeChecked();
    expect(disabledMode).toBeChecked();

    act(() => {
      window.dispatchEvent(new CustomEvent('lazymind:user-ui-preferences-changed', {
        detail: { task_center_enabled: true, workflows_enabled: true },
      }));
    });

    await waitFor(() => {
      expect(
        within(workflowControl).getAllByRole('radio').every((radio) => !radio.hasAttribute('disabled')),
      ).toBe(true);
    });
    expect(subagentSwitch).toBeChecked();
    expect(autoMode).toBeChecked();
    expect(mocks.onSave).not.toHaveBeenCalled();
    expect(mocks.patchConversationSettings).not.toHaveBeenCalled();
  });

  it('keeps subtask and workflow choices independent', async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({
      task_center_enabled: true,
      workflows_enabled: true,
    });
    mocks.patchConversationSettings.mockResolvedValue(undefined);

    render(
      <ChatConfigPopover
        conversationId="conversation-1"
        initialSettings={{
          enable_workflow: true,
          workflow_mode: 'auto',
          enable_subagent: true,
        }}
        onSave={mocks.onSave}
      />,
    );

    fireEvent.click(screen.getByText('对话配置'));

    const subagentSwitch = screen.getByRole('switch', { name: '允许子任务' });
    const workflowControl = screen.getByLabelText('工作流执行方式');
    const autoMode = within(workflowControl).getByRole('radio', { name: '自动执行' });
    const disabledMode = within(workflowControl).getByRole('radio', { name: '禁用' });

    await waitFor(() => expect(subagentSwitch).toBeEnabled());
    fireEvent.click(subagentSwitch);

    await waitFor(() => {
      expect(mocks.patchConversationSettings).toHaveBeenNthCalledWith(1, 'conversation-1', {
        enable_workflow: true,
        workflow_mode: 'auto',
        enable_subagent: false,
      });
    });
    expect(subagentSwitch).not.toBeChecked();
    expect(autoMode).toBeChecked();
    expect(
      within(workflowControl).getAllByRole('radio').every(
        (radio) => !radio.hasAttribute('disabled'),
      ),
    ).toBe(true);

    fireEvent.click(disabledMode);

    await waitFor(() => {
      expect(mocks.patchConversationSettings).toHaveBeenNthCalledWith(2, 'conversation-1', {
        enable_workflow: false,
        workflow_mode: 'auto',
        enable_subagent: false,
      });
    });
    expect(subagentSwitch).not.toBeChecked();
    expect(disabledMode).toBeChecked();

    fireEvent.click(subagentSwitch);

    await waitFor(() => {
      expect(mocks.patchConversationSettings).toHaveBeenNthCalledWith(3, 'conversation-1', {
        enable_workflow: false,
        workflow_mode: 'auto',
        enable_subagent: true,
      });
    });
    expect(subagentSwitch).toBeChecked();
    expect(disabledMode).toBeChecked();
  });
});
