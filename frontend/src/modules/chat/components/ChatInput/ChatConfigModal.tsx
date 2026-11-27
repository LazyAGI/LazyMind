import { useState, useEffect, useRef } from 'react';
import { Popover, Segmented, Switch, Tooltip, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { SettingOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import {
  ChatServiceApi,
  ConversationSettingsApi,
  parseConversationRuntimeSettings,
  type ConversationRuntimeSettings,
} from '../../utils/request';
import {
  fetchUserUiPreferences,
  USER_UI_PREFERENCES_CHANGED_EVENT,
} from '@/modules/user/uiPreferencesApi';
import { AGENTS } from '@/modules/agentIntegration/catalog';
import { buildExecutorCatalog, executorUnavailableReason, useChatExecutors } from '../../hooks/useChatExecutors';
import ExecutorActivationGuide from './ExecutorActivationGuide';
import './ChatConfigModal.scss';
import { useWorkflowStore } from '../../store/workflowPanel';
import { WorkflowSessionApi } from '../../utils/request';
import { applyWorkflowApprovalSettings, workflowApprovalErrorMessage, type SendWorkflowMessage } from '../../utils/workflowApprovalSettings';

export { buildExecutorCatalog } from '../../hooks/useChatExecutors';

interface ChatConfigPopoverProps {
  menuHeight?: number;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** Prevents opening or editing while the parent composer is unavailable. */
  disabled?: boolean;
  /** When provided, settings are saved to the server immediately on change. */
  conversationId?: string;
  /** Initial settings to display. If not provided, fetched from server on first open. */
  initialSettings?: ConversationRuntimeSettings;
  /** Called with the new settings after a successful save. */
  onSave?: (settings: ConversationRuntimeSettings) => void;
  /** When true, workflows cannot be disabled because a workflow session is active. */
  hasWorkflowSession?: boolean;
  onSendWorkflowMessage?: SendWorkflowMessage;
  /** Current execution mode of the active Workflow Session. */
  lockedWorkflowMode?: 'auto' | 'dynamic';
}

type WorkflowExecutionMode = 'auto' | 'dynamic' | 'disabled';

export function resolveWorkflowExecutionMode(
  settings: ConversationRuntimeSettings | null,
  hasWorkflowSession: boolean,
  workflowAvailable = true,
): WorkflowExecutionMode {
  if (!workflowAvailable) {
    return 'disabled';
  }
  if (!hasWorkflowSession && settings?.enable_workflow === false) {
    return 'disabled';
  }
  return settings?.workflow_mode === 'auto' ? 'auto' : 'dynamic';
}

export default function ChatConfigPopover({
  menuHeight,  open: controlledOpen,
  onOpenChange,
  disabled = false,
  conversationId,
  initialSettings,
  onSave,
  hasWorkflowSession = false,
  lockedWorkflowMode,
  onSendWorkflowMessage,
}: ChatConfigPopoverProps) {
  const { t } = useTranslation();
  const workflowSession = useWorkflowStore(state => conversationId ? state.sessionByConversation[conversationId] : null);
  const [savingWorkflowMode, setSavingWorkflowMode] = useState(false);
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = (next: boolean) => { setInternalOpen(next); onOpenChange?.(next); };
  const [settings, setSettings] = useState<ConversationRuntimeSettings | null>(
    initialSettings ?? null,
  );
  const { executors, loading, error, refresh } = useChatExecutors(open);
  const [guideAgent, setGuideAgent] = useState<typeof AGENTS[number]>();
  const closeGuide = () => {
    setGuideAgent(undefined);
    if (!disabled) setOpen(true);
  };
  const [featureControls, setFeatureControls] = useState({
    loaded: false,
    error: false,
    taskCenterEnabled: false,
    workflowsEnabled: false,
  });
  // Track whether we've already fetched defaults to avoid repeated requests.
  const fetchedRef = useRef(false);

  useEffect(() => {
    if (disabled) {
      setOpen(false);
    }
  }, [disabled]);

  // Sync external initialSettings into local state; reset fetch cache on conversation change.
  useEffect(() => {
    fetchedRef.current = Boolean(
      initialSettings && Object.keys(initialSettings).length > 0,
    );
    if (initialSettings && Object.keys(initialSettings).length > 0) {
      setSettings(initialSettings);
    } else if (!conversationId || conversationId.startsWith('temp_')) {
      setSettings(null);
      fetchedRef.current = false;
    }
  }, [conversationId, initialSettings]);

  useEffect(() => {
    let active = true;
    const applyPreferences = (preferences: {
      task_center_enabled: boolean;
      workflows_enabled: boolean;
    }) => {
      if (!active) return;
      setFeatureControls({
        loaded: true,
        error: false,
        taskCenterEnabled: preferences.task_center_enabled,
        workflowsEnabled: preferences.workflows_enabled,
      });
    };
    const refreshPreferences = async () => {
      try {
        applyPreferences(await fetchUserUiPreferences({ silentError: true } as never));
      } catch {
        if (active) {
          setFeatureControls({
            loaded: true,
            error: true,
            taskCenterEnabled: false,
            workflowsEnabled: false,
          });
        }
      }
    };
    const handlePreferencesChanged = (event: Event) => {
      const detail = (event as CustomEvent<{
        task_center_enabled?: boolean;
        workflows_enabled?: boolean;
      }>).detail;
      if (
        typeof detail?.task_center_enabled === 'boolean'
        && typeof detail.workflows_enabled === 'boolean'
      ) {
        applyPreferences({
          task_center_enabled: detail.task_center_enabled,
          workflows_enabled: detail.workflows_enabled,
        });
        return;
      }
      void refreshPreferences();
    };

    void refreshPreferences();
    window.addEventListener(USER_UI_PREFERENCES_CHANGED_EVENT, handlePreferencesChanged);
    return () => {
      active = false;
      window.removeEventListener(USER_UI_PREFERENCES_CHANGED_EVENT, handlePreferencesChanged);
    };
  }, []);

  // Fetch settings from server the first time the popover opens.
  async function ensureSettings() {
    if (fetchedRef.current) {
      return;
    }
    fetchedRef.current = true;
    try {
      if (conversationId && !conversationId.startsWith('temp_')) {
        const detailRes =
          await ChatServiceApi().conversationServiceGetConversationDetail({
            conversation: conversationId,
          });
        const convSettings = parseConversationRuntimeSettings(
          detailRes.data.conversation,
        );
        if (convSettings) {
          setSettings(convSettings);
          return;
        }
      }
      const res = await ConversationSettingsApi().getChatSettings();
      // Go wraps responses as {code, message, data: {...}}; extract the inner data.
      const payload = (res.data as any)?.data ?? res.data;
      setSettings((s) => ({ ...payload, ...s }));
    } catch {
      // Silently fall back to empty; individual fields will render as undefined.
    }
  }

  function handleOpenChange(next: boolean) {
    if (disabled) {
      setOpen(false);
      return;
    }
    setOpen(next);
    if (next) {
      void ensureSettings();
    }
  }

  async function handleChange(patch: Partial<ConversationRuntimeSettings>) {
    if (
      (patch.enable_workflow === true && !workflowControlsAvailable)
      || (patch.enable_subagent === true && !taskControlsAvailable)
    ) {
      return;
    }
    const next = { ...settings, ...patch };
    try {
      const target = executorCatalog.find((item) => item.id === patch.chat_executor);
      if (patch.chat_executor && !target?.available) {
        message.error(target ? executorUnavailableReason(target, t) : t('chat.conversationConfigExecutorUnavailable'));
        return;
      }
      setSettings(next);
      if (conversationId && !conversationId.startsWith('temp_')) {
        await ConversationSettingsApi().patchConversationSettings(conversationId, next);
        message.success(t('chat.conversationConfigSaved'));
      }
      onSave?.(next);
    } catch {
      setSettings(settings);
      if (patch.chat_executor) {
        message.error(t('chat.conversationConfigExecutorUnavailable'));
      }
    }
  }

  const chatExecutor = settings?.chat_executor ?? 'lazymind';
  const executorCatalog = buildExecutorCatalog(
    executors,
    chatExecutor,
    t('chat.conversationConfigExecutorUnavailable'),
  );
  const displayedExecutor = executorCatalog.find(item => item.id === chatExecutor);
  const selectedAgent = AGENTS.find(agent => agent.id === chatExecutor);
  const defaultGuide = selectedAgent ?? AGENTS.find(agent => agent.executorName);
  const executorDescription = displayedExecutor && !displayedExecutor.available
    ? executorUnavailableReason(displayedExecutor, t)
    : displayedExecutor?.kind === 'external'
      ? t('chat.conversationConfigExecutorExternalDesc', { name: displayedExecutor.display_name })
      : t('chat.conversationConfigExecutorLazyMindDesc');
  const featureControlsLoaded = featureControls.loaded && !featureControls.error;
  const taskControlsAvailable = featureControlsLoaded && featureControls.taskCenterEnabled;
  const workflowControlsAvailable = featureControlsLoaded && featureControls.workflowsEnabled;
  const executionMode = hasWorkflowSession && lockedWorkflowMode
    ? (workflowSession?.approval_mode ?? workflowSession?.workflow_mode ?? lockedWorkflowMode)
    : resolveWorkflowExecutionMode(settings, hasWorkflowSession, workflowControlsAvailable);
  const sharedFeatureControlsMessage = !featureControls.loaded
    ? t('chat.conversationConfigFeatureControlsLoading')
    : featureControls.error
      ? t('chat.conversationConfigFeatureControlsUnavailable')
      : null;
  const taskControlsMessage = sharedFeatureControlsMessage
    ?? (!featureControls.taskCenterEnabled
      ? t('chat.conversationConfigTaskCenterDisabled')
      : null);
  const workflowControlsMessage = sharedFeatureControlsMessage
    ?? (!featureControls.workflowsEnabled
      ? t('chat.conversationConfigWorkflowMasterDisabled')
      : null);
  const sharedFeatureControlsMessageId = 'chat-config-feature-controls-message';
  const taskControlsMessageId = sharedFeatureControlsMessage
    ? sharedFeatureControlsMessageId
    : taskControlsMessage
      ? 'chat-config-task-controls-message'
      : undefined;
  const workflowControlsMessageId = sharedFeatureControlsMessage
    ? sharedFeatureControlsMessageId
    : workflowControlsMessage
      ? 'chat-config-workflow-controls-message'
      : undefined;

  async function handleExecutionModeChange(mode: string | number) {
    const nextMode = mode as WorkflowExecutionMode;
    if (hasWorkflowSession) {
      if (!workflowSession || nextMode === 'disabled' || savingWorkflowMode) return;
      setSavingWorkflowMode(true);
      try {
        const result = await WorkflowSessionApi().getSession(workflowSession.session_id, { silentError: true } as never);
        const remember = Boolean(result.data.data.session.saved_approval_mode);
        await applyWorkflowApprovalSettings(workflowSession, nextMode, remember, onSendWorkflowMessage);
        await useWorkflowStore.getState().loadActiveSession(conversationId!);
        message.success(t(remember ? 'chat.workflowApprovalSavedRemembered' : 'chat.workflowApprovalSaved', {
          mode: t(nextMode === 'auto' ? 'chat.workflowApprovalAutomatic' : 'chat.workflowApprovalOnDemand'),
        }));
      } catch (error) {
        message.error(await workflowApprovalErrorMessage(error, () =>
          useWorkflowStore.getState().loadActiveSession(conversationId!, { silentError: true })));
      } finally { setSavingWorkflowMode(false); }
      return;
    }
    if (nextMode === 'disabled') {
      await handleChange({ enable_workflow: false });
      return;
    }
    await handleChange({ enable_workflow: true, workflow_mode: nextMode });
  }

  const content = (
    <div className="chat-config-popover-content">
      {sharedFeatureControlsMessage ? (
        <p
          id={sharedFeatureControlsMessageId}
          className="chat-config-master-notice"
          role="status"
        >
          {sharedFeatureControlsMessage}
        </p>
      ) : null}
      {!sharedFeatureControlsMessage && taskControlsMessage ? (
        <p
          id="chat-config-task-controls-message"
          className="chat-config-master-notice"
          role="status"
        >
          {taskControlsMessage}
        </p>
      ) : null}
      {!sharedFeatureControlsMessage && workflowControlsMessage ? (
        <p
          id="chat-config-workflow-controls-message"
          className="chat-config-master-notice"
          role="status"
        >
          {workflowControlsMessage}
        </p>
      ) : null}
      <div className="chat-config-section chat-config-executor-section">
        <div className="chat-config-row-label chat-config-section-title">
          <span className="chat-config-label">{t('chat.conversationConfigExecutor')}</span>
          <button type="button" className="chat-config-guide-trigger" title={t('chat.executorGuide.title')}
            aria-label={t('chat.executorGuide.title')} onClick={() => setGuideAgent(defaultGuide)}>
            <QuestionCircleOutlined />
          </button>
        </div>
        <div className="chat-config-executor-grid" role="radiogroup" aria-label={t('chat.conversationConfigExecutor')} aria-busy={loading}>
          {executorCatalog.map(item => {
            const agent = AGENTS.find(agent => agent.id === item.id);
            const reason = item.available ? '' : executorUnavailableReason(item, t);
            return (
              <div className="chat-config-executor-option" key={item.id}>
                <button type="button" role="radio" aria-label={item.display_name}
                  aria-description={reason || undefined} aria-checked={chatExecutor === item.id}
                  className={chatExecutor === item.id ? 'is-selected' : ''}
                  disabled={!item.available} onClick={() => void handleChange({ chat_executor: item.id })}>
                  <span className="chat-config-executor-name">{item.display_name}</span>
                  {reason && <span className="chat-config-executor-reason">{reason}</span>}
                </button>
                {agent && reason && <button type="button" className="chat-config-guide-trigger" aria-label={t('chat.executorGuide.helpFor', { name: agent.name })}
                  onClick={() => setGuideAgent(agent)}><QuestionCircleOutlined /></button>}
              </div>
            );
          })}
        </div>
        {error && <div className="chat-config-executor-error" role="status">
          {t('chat.executorGuide.loadFailed')} <button type="button" onClick={refresh}>{t('common.retry')}</button>
        </div>}
        <p className="chat-config-workflow-description">
          {executorDescription}
        </p>
      </div>
      <div className="chat-config-section chat-config-workflow-section">
        <div className="chat-config-row-label chat-config-section-title">
          <span className="chat-config-label">{t('chat.conversationConfigWorkflowExecution')}</span>
          <Tooltip title={t('chat.conversationConfigWorkflowExecutionTooltip')} placement="top">
            <QuestionCircleOutlined className="chat-config-help-icon" />
          </Tooltip>
        </div>
        <Segmented
          block
          className="chat-config-workflow-mode"
          value={executionMode}
          disabled={!workflowControlsAvailable || savingWorkflowMode || (hasWorkflowSession && !workflowSession)}
          aria-label={t('chat.conversationConfigWorkflowExecution')}
          aria-describedby={workflowControlsMessageId}
          onChange={handleExecutionModeChange}
          options={[
            { label: t('chat.conversationConfigWorkflowAuto'), value: 'auto' },
            { label: t('chat.conversationConfigWorkflowApproval'), value: 'dynamic' },
            {
              label: t('chat.conversationConfigWorkflowDisabled'),
              value: 'disabled',
              disabled: hasWorkflowSession,
            },
          ]}
        />
        <p className="chat-config-workflow-description">
          {t({
            auto: 'chat.conversationConfigWorkflowAutoDesc',
            dynamic: 'chat.conversationConfigWorkflowApprovalDesc',
            disabled: 'chat.conversationConfigWorkflowDisabledDesc',
          }[executionMode])}
        </p>
      </div>

      {/* Allow subtask toggle */}
      <div className="chat-config-section chat-config-subagent-section">
        <div className="chat-config-row">
          <div className="chat-config-row-label">
            <span className="chat-config-label">{t('chat.conversationConfigEnableSubagent')}</span>
            <Tooltip title={t('chat.conversationConfigEnableSubagentTooltip')} placement="top">
              <QuestionCircleOutlined className="chat-config-help-icon" />
            </Tooltip>
          </div>
          <Switch
            checked={taskControlsAvailable && (settings?.enable_subagent ?? true)}
            disabled={!taskControlsAvailable}
            aria-label={t('chat.conversationConfigEnableSubagent')}
            aria-describedby={taskControlsMessageId}
            onChange={(v: boolean) => handleChange({ enable_subagent: v })}
          />
        </div>
      </div>
    </div>
  );

  return (<>
    <Popover
      styles={{ body: { height: menuHeight } }}
      content={content}
      open={disabled ? false : open}
      onOpenChange={handleOpenChange}
      trigger="click"
      placement="topLeft"
      arrow={false}
      overlayClassName="chat-composer-menu chat-config-popover-overlay"
      destroyTooltipOnHide
    >
      <button
        type="button"
        className="input-bottom-actions-left-item chat-config-trigger"
        disabled={disabled}
        aria-label={t('chat.conversationConfig')}
        aria-expanded={!disabled && open}
        title={t('chat.conversationConfig')}
      >
        <SettingOutlined aria-hidden />
        <span className="chat-config-trigger-label">{t('chat.conversationConfig')}</span>
      </button>
    </Popover>
    {guideAgent && !disabled && <ExecutorActivationGuide agent={guideAgent} onSelect={setGuideAgent} onClose={closeGuide} />}
  </>);
}
