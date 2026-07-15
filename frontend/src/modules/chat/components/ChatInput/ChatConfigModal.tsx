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
import {
  listUserPluginSettings,
  setUserPluginEnabled,
  type UserPluginSetting,
} from '@/modules/plugin/pluginDraftApi';
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
  const [pluginItems, setPluginItems] = useState<UserPluginSetting[]>([]);
  // Track whether we've already fetched defaults to avoid repeated requests.
  const fetchedRef = useRef(false);
  const pluginsFetchedRef = useRef(false);

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

  async function ensurePluginItems() {
    if (pluginsFetchedRef.current) {
      return;
    }
    pluginsFetchedRef.current = true;
    try {
      setPluginItems(await listUserPluginSettings());
    } catch {
      // Plugin defaults are optional configuration. Keep the rest of the
      // conversation settings usable if this endpoint is unavailable.
      pluginsFetchedRef.current = false;
      setPluginItems([]);
    }
  }

  function handleOpenChange(next: boolean) {
    setOpen(next);
    if (next) {
      void Promise.all([ensureSettings(), ensurePluginItems()]);
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

  async function handlePluginToggle(item: UserPluginSetting, enabled: boolean) {
    setPluginItems((items) =>
      items.map((current) =>
        current.plugin_ref === item.plugin_ref ? { ...current, enabled } : current,
      ),
    );
    try {
      await setUserPluginEnabled(item.plugin_ref, enabled);
      message.success(t('chat.conversationConfigSaved'));
    } catch {
      setPluginItems((items) =>
        items.map((current) =>
          current.plugin_ref === item.plugin_ref
            ? { ...current, enabled: item.enabled }
            : current,
        ),
      );
      message.error(t('chat.conversationConfigSaveFailed'));
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

      {pluginEnabled && pluginItems.length > 0 && (
        <div className="chat-config-section">
          <div className="chat-config-label">{t('chat.conversationConfigDefaultPlugins')}</div>
          {pluginItems.map((item) => (
            <div className="chat-config-row" key={item.plugin_ref}>
              <span className="chat-config-row-label">{item.name || item.plugin_id}</span>
              <Switch
                checked={item.enabled}
                onChange={(value) => void handlePluginToggle(item, value)}
              />
            </div>
          ))}
        </div>
      )}

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
