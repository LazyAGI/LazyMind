import { useState, useEffect, useRef } from 'react';
import { Popover, Segmented, Switch, Tooltip, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { SettingOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import {
  ChatServiceApi,
  ConversationSettingsApi,
  parseConversationWorkflowSettings,
  type ConversationWorkflowSettings,
} from '../../utils/request';
import './ChatConfigModal.scss';

interface ChatConfigPopoverProps {
  /** When provided, settings are saved to the server immediately on change. */
  conversationId?: string;
  /** Initial settings to display. If not provided, fetched from server on first open. */
  initialSettings?: ConversationWorkflowSettings;
  /** Called with the new settings after a successful save. */
  onSave?: (settings: ConversationWorkflowSettings) => void;
  /** When true, workflows cannot be disabled because a workflow session is active. */
  hasWorkflowSession?: boolean;
}

type WorkflowExecutionMode = 'auto' | 'dynamic' | 'disabled';

export default function ChatConfigPopover({
  conversationId,
  initialSettings,
  onSave,
  hasWorkflowSession = false,
}: ChatConfigPopoverProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<ConversationWorkflowSettings | null>(
    initialSettings ?? null,
  );
  // Track whether we've already fetched defaults to avoid repeated requests.
  const fetchedRef = useRef(false);
  const enforcedWorkflowConversationRef = useRef<string | null>(null);

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

  // Starting/attaching a workflow makes approval mode authoritative for this
  // conversation. Enforce it once per attached session; users may still switch
  // between auto and approval afterwards, but cannot disable until it is removed.
  useEffect(() => {
    if (!hasWorkflowSession) {
      enforcedWorkflowConversationRef.current = null;
      return;
    }
    const key = conversationId || 'pending-conversation';
    if (enforcedWorkflowConversationRef.current === key) return;
    enforcedWorkflowConversationRef.current = key;
    const next: ConversationWorkflowSettings = {
      ...settings,
      enable_workflow: true,
      workflow_mode: 'dynamic',
    };
    setSettings(next);
    onSave?.(next);
    if (conversationId && !conversationId.startsWith('temp_')) {
      void ConversationSettingsApi().patchWorkflowSettings(conversationId, next).catch(() => {
        enforcedWorkflowConversationRef.current = null;
      });
    }
  }, [conversationId, hasWorkflowSession, onSave, settings]);

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
        const convSettings = parseConversationWorkflowSettings(
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
    setOpen(next);
    if (next) {
      void ensureSettings();
    }
  }

  async function handleChange(patch: Partial<ConversationWorkflowSettings>) {
    const next = { ...settings, ...patch };
    setSettings(next);
    try {
      if (conversationId && !conversationId.startsWith('temp_')) {
        await ConversationSettingsApi().patchWorkflowSettings(conversationId, next);
        message.success(t('chat.conversationConfigSaved'));
      }
      onSave?.(next);
    } catch {
      setSettings(settings);
    }
  }

  const workflowEnabled = settings?.enable_workflow ?? true;
  const executionMode: WorkflowExecutionMode = workflowEnabled
    ? (settings?.workflow_mode ?? 'dynamic')
    : 'disabled';

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
            checked={settings?.enable_subagent ?? true}
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
