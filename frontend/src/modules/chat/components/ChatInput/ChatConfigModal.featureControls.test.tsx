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
      'chat.conversationConfigTaskCenterDisabled': '任务中心已关闭，对话中的子任务和工作流暂不可用。',
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

describe('ChatConfigPopover task center dependency', () => {
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

  it('shows effective disabled controls and updates without overwriting saved chat choices', async () => {
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

    expect(await screen.findByText('任务中心已关闭，对话中的子任务和工作流暂不可用。')).toBeInTheDocument();
    const subagentSwitch = screen.getByRole('switch', { name: '允许子任务' });
    expect(subagentSwitch).toBeDisabled();
    expect(subagentSwitch).not.toBeChecked();

    const workflowControl = screen.getByLabelText('工作流执行方式');
    expect(
      within(workflowControl).getAllByRole('radio').every((radio) => radio.hasAttribute('disabled')),
    ).toBe(true);
    expect(mocks.onSave).not.toHaveBeenCalled();
    expect(mocks.patchConversationSettings).not.toHaveBeenCalled();

    act(() => {
      window.dispatchEvent(new CustomEvent('lazymind:user-ui-preferences-changed', {
        detail: { task_center_enabled: true, workflows_enabled: false },
      }));
    });

    await waitFor(() => expect(subagentSwitch).toBeEnabled());
    expect(subagentSwitch).toBeChecked();
    expect(screen.getByText('工作流总开关已关闭。')).toBeInTheDocument();
    expect(
      within(workflowControl).getAllByRole('radio').every((radio) => radio.hasAttribute('disabled')),
    ).toBe(true);

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
    expect(mocks.onSave).not.toHaveBeenCalled();
    expect(mocks.patchConversationSettings).not.toHaveBeenCalled();
  });
});
