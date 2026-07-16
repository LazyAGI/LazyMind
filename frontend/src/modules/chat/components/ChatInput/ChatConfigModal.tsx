import { useState, useEffect, useRef } from 'react';
import { Popover, Segmented, Switch, Tooltip, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { SettingOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import {
  ChatServiceApi,
  ConversationSettingsApi,
  parseConversationPluginSettings,
  type ConversationPluginSettings,
} from '../../utils/request';
import './ChatConfigModal.scss';

interface ChatConfigPopoverProps {
  /** When provided, settings are saved to the server immediately on change. */
  conversationId?: string;
  /** Initial settings to display. If not provided, fetched from server on first open. */
  initialSettings?: ConversationPluginSettings;
  /** Called with the new settings after a successful save. */
  onSave?: (settings: ConversationPluginSettings) => void;
  /** When true, plugins cannot be disabled because a plugin session is active. */
  hasPluginSession?: boolean;
}

type PluginExecutionMode = 'auto' | 'dynamic' | 'disabled';

export default function ChatConfigPopover({
  conversationId,
  initialSettings,
  onSave,
  hasPluginSession = false,
}: ChatConfigPopoverProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<ConversationPluginSettings | null>(
    initialSettings ?? null,
  );
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
        const convSettings = parseConversationPluginSettings(
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

  async function handleChange(patch: Partial<ConversationPluginSettings>) {
    const next = { ...settings, ...patch };
    setSettings(next);
    try {
      if (conversationId && !conversationId.startsWith('temp_')) {
        await ConversationSettingsApi().patchPluginSettings(conversationId, next);
        message.success(t('chat.conversationConfigSaved'));
      }
      onSave?.(next);
    } catch {
      setSettings(settings);
    }
  }

  const pluginEnabled = settings?.enable_plugin ?? true;
  const executionMode: PluginExecutionMode = pluginEnabled
    ? (settings?.plugin_mode ?? 'dynamic')
    : 'disabled';

  function handleExecutionModeChange(mode: string | number) {
    const nextMode = mode as PluginExecutionMode;
    if (nextMode === 'disabled') {
      void handleChange({ enable_plugin: false });
      return;
    }
    void handleChange({ enable_plugin: true, plugin_mode: nextMode });
  }

  const content = (
    <div className="chat-config-popover-content">
      <div className="chat-config-section chat-config-plugin-section">
        <div className="chat-config-row-label chat-config-section-title">
          <span className="chat-config-label">{t('chat.conversationConfigPluginExecution')}</span>
          <Tooltip title={t('chat.conversationConfigPluginExecutionTooltip')} placement="top">
            <QuestionCircleOutlined className="chat-config-help-icon" />
          </Tooltip>
        </div>
        <Segmented
          block
          className="chat-config-plugin-mode"
          value={executionMode}
          onChange={handleExecutionModeChange}
          options={[
            { label: t('chat.conversationConfigPluginAuto'), value: 'auto' },
            { label: t('chat.conversationConfigPluginApproval'), value: 'dynamic' },
            {
              label: t('chat.conversationConfigPluginDisabled'),
              value: 'disabled',
              disabled: hasPluginSession,
            },
          ]}
        />
        <p className="chat-config-plugin-description">
          {t('chat.conversationConfigPluginExecutionDesc')}
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
