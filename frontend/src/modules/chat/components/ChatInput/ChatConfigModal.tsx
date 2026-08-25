import { useState, useEffect, useRef } from 'react';
import { Popover, Segmented, Switch, Tooltip, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { SettingOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import {
  ChatServiceApi,
  ConversationSettingsApi,
  parseConversationRuntimeSettings,
  type ChatExecutor,
  type ChatExecutorDescriptor,
  type ConversationRuntimeSettings,
} from '../../utils/request';
import {
  fetchUserUiPreferences,
  USER_UI_PREFERENCES_CHANGED_EVENT,
} from '@/modules/user/uiPreferencesApi';
import './ChatConfigModal.scss';

interface ChatConfigPopoverProps {
  /** When provided, settings are saved to the server immediately on change. */
  conversationId?: string;
  /** Initial settings to display. If not provided, fetched from server on first open. */
  initialSettings?: ConversationRuntimeSettings;
  /** Called with the new settings after a successful save. */
  onSave?: (settings: ConversationRuntimeSettings) => void;
  /** When true, workflows cannot be disabled because a workflow session is active. */
  hasWorkflowSession?: boolean;
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

const lazyMindExecutor: ChatExecutorDescriptor = {
  id: 'lazymind',
  display_name: 'LazyMind',
  kind: 'internal',
  installed: true,
  host_online: true,
  available: true,
};

export function buildExecutorCatalog(
  executors: ChatExecutorDescriptor[],
  selectedId: ChatExecutor,
  unavailableReason: string,
): ChatExecutorDescriptor[] {
  const catalog = new Map<string, ChatExecutorDescriptor>([
    [lazyMindExecutor.id, lazyMindExecutor],
  ]);
  executors.forEach((executor) => catalog.set(executor.id, executor));
  if (!catalog.has(selectedId)) {
    catalog.set(selectedId, {
      id: selectedId,
      display_name: selectedId,
      kind: 'external',
      installed: false,
      host_online: false,
      available: false,
      unavailable_reason: unavailableReason,
    });
  }
  return Array.from(catalog.values());
}

export default function ChatConfigPopover({
  conversationId,
  initialSettings,
  onSave,
  hasWorkflowSession = false,
}: ChatConfigPopoverProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<ConversationRuntimeSettings | null>(
    initialSettings ?? null,
  );
  const [executors, setExecutors] = useState<ChatExecutorDescriptor[]>([]);
  const [featureControls, setFeatureControls] = useState({
    loaded: false,
    error: false,
    taskCenterEnabled: false,
    workflowsEnabled: false,
  });
  // Track whether we've already fetched defaults to avoid repeated requests.
  const fetchedRef = useRef(false);

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

  useEffect(() => {
    if (!open) return;
    let active = true;
    const refresh = async () => {
      try {
        const response = await ConversationSettingsApi().listChatExecutors();
        const payload = (response.data as any)?.data ?? response.data;
        const values = Array.isArray(payload?.executors) ? payload.executors : [];
        if (active) {
          setExecutors(
            values.filter(
              (item: ChatExecutorDescriptor) =>
                item && typeof item.id === 'string' && typeof item.display_name === 'string',
            ),
          );
        }
      } catch {
        // Keep the last known catalog; Core remains the final validation boundary.
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 3000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [open]);

  function handleOpenChange(next: boolean) {
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
      const target = executors.find((item) => item.id === patch.chat_executor);
      if (target && !target.available) {
        message.error(target.unavailable_reason || t('chat.conversationConfigExecutorUnavailable'));
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
  const displayedExecutor = executorCatalog.find((item) => item.id === chatExecutor)
    ?? lazyMindExecutor;
  const executorOptions = executorCatalog.map((item) => ({
    label: item.display_name,
    value: item.id,
    disabled: !item.available,
  }));
  const executorDescription = displayedExecutor.available === false
    ? displayedExecutor.unavailable_reason || t('chat.conversationConfigExecutorUnavailable')
    : displayedExecutor.kind === 'external'
      ? t('chat.conversationConfigExecutorExternalDesc', {
          name: displayedExecutor.display_name,
        })
      : t('chat.conversationConfigExecutorLazyMindDesc');
  const taskControlsAvailable = featureControls.loaded
    && !featureControls.error
    && featureControls.taskCenterEnabled;
  const workflowControlsAvailable = taskControlsAvailable && featureControls.workflowsEnabled;
  const executionMode = resolveWorkflowExecutionMode(
    settings,
    hasWorkflowSession,
    workflowControlsAvailable,
  );
  const featureControlsMessage = !featureControls.loaded
    ? t('chat.conversationConfigFeatureControlsLoading')
    : featureControls.error
      ? t('chat.conversationConfigFeatureControlsUnavailable')
      : !featureControls.taskCenterEnabled
        ? t('chat.conversationConfigTaskCenterDisabled')
        : !featureControls.workflowsEnabled
          ? t('chat.conversationConfigWorkflowMasterDisabled')
          : null;
  const featureControlsMessageId = 'chat-config-feature-controls-message';

  function handleExecutionModeChange(mode: string | number) {
    const nextMode = mode as WorkflowExecutionMode;
    if (nextMode === 'disabled') {
      void handleChange({ enable_workflow: false });
      return;
    }
    void handleChange({ enable_workflow: true, workflow_mode: nextMode });
  }

  const content = (
    <div className="chat-config-popover-content">
      {featureControlsMessage ? (
        <p
          id={featureControlsMessageId}
          className="chat-config-master-notice"
          role="status"
        >
          {featureControlsMessage}
        </p>
      ) : null}
      <div className="chat-config-section chat-config-executor-section">
        <div className="chat-config-row-label chat-config-section-title">
          <span className="chat-config-label">{t('chat.conversationConfigExecutor')}</span>
          <Tooltip title={t('chat.conversationConfigExecutorTooltip')} placement="top">
            <QuestionCircleOutlined className="chat-config-help-icon" />
          </Tooltip>
        </div>
        <div
          className="chat-config-executor-grid"
          role="radiogroup"
          aria-label={t('chat.conversationConfigExecutor')}
        >
          {executorOptions.map((option) => (
            <button
              key={option.value}
              type="button"
              role="radio"
              aria-checked={chatExecutor === option.value}
              className={chatExecutor === option.value ? 'is-selected' : ''}
              disabled={option.disabled}
              onClick={() => void handleChange({ chat_executor: option.value as ChatExecutor })}
            >
              {option.label}
            </button>
          ))}
        </div>
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
          disabled={!workflowControlsAvailable}
          aria-label={t('chat.conversationConfigWorkflowExecution')}
          aria-describedby={featureControlsMessage ? featureControlsMessageId : undefined}
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
          {t('chat.conversationConfigWorkflowExecutionDesc')}
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
            aria-describedby={featureControlsMessage ? featureControlsMessageId : undefined}
            onChange={(v: boolean) => handleChange({ enable_subagent: v })}
          />
        </div>
      </div>
    </div>
  );

  return (
    <Popover
      content={content}
      open={open}
      onOpenChange={handleOpenChange}
      trigger="click"
      placement="topLeft"
      arrow={false}
      overlayClassName="chat-config-popover-overlay"
      destroyTooltipOnHide
    >
      <div className="input-bottom-actions-left-item">
        <SettingOutlined style={{ marginRight: 4 }} />
        {t('chat.conversationConfig')}
      </div>
    </Popover>
  );
}
