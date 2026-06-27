import { useState, useEffect, useRef } from 'react';
import { Popover, Radio, Switch, Tooltip, message } from 'antd';
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
  /** When true, the allow-plugin toggle is locked (plugin session is active). */
  hasPluginSession?: boolean;
}

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
      ensureSettings();
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
      message.error(t('chat.conversationConfigSaveFailed'));
      setSettings(settings);
    }
  }

  const pluginEnabled = settings?.enable_plugin ?? true;

  const content = (
    <div className="chat-config-popover-content">
      {/* Allow Plugin toggle */}
      <div className="chat-config-section">
        <div className="chat-config-row">
          <div className="chat-config-row-label">
            <span className="chat-config-label">{t('chat.conversationConfigAllowPlugin')}</span>
            <Tooltip title={t('chat.conversationConfigAllowPluginTooltip')} placement="top">
              <QuestionCircleOutlined className="chat-config-help-icon" />
            </Tooltip>
          </div>
          <Switch
            checked={pluginEnabled}
            disabled={hasPluginSession}
            onChange={(v) => handleChange({ enable_plugin: v })}
          />
        </div>
      </div>

      {/* Plugin driver mode — only visible when plugin is allowed */}
      {pluginEnabled && (
        <div className="chat-config-section">
          <div className="chat-config-label">{t('chat.conversationConfigPluginMode')}</div>
          <Radio.Group
            value={settings?.plugin_mode ?? 'dynamic'}
            onChange={(e) => handleChange({ plugin_mode: e.target.value })}
            className="chat-config-radio-group"
          >
            <Radio value="dynamic">{t('chat.conversationConfigPluginModeDynamic')}</Radio>
            <Radio value="auto">{t('chat.conversationConfigPluginModeAuto')}</Radio>
          </Radio.Group>
        </div>
      )}

      {/* Allow subtask toggle */}
      <div className="chat-config-section">
        <div className="chat-config-row">
          <div className="chat-config-row-label">
            <span className="chat-config-label">{t('chat.conversationConfigEnableSubagent')}</span>
            <Tooltip title={t('chat.conversationConfigEnableSubagentTooltip')} placement="top">
              <QuestionCircleOutlined className="chat-config-help-icon" />
            </Tooltip>
          </div>
          <Switch
            checked={settings?.enable_subagent ?? true}
            onChange={(v) => handleChange({ enable_subagent: v })}
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
