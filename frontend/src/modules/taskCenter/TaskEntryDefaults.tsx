import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Radio, Skeleton, Switch, Tabs } from 'antd';
import type { RadioChangeEvent } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import { buildExecutorCatalog } from '@/modules/chat/components/ChatInput/ChatConfigModal';
import { THINKING_DEPTH_VALUES, type ThinkingDepth } from '@/modules/chat/store/chatThink';
import {
  ConversationSettingsApi,
  FALLBACK_CHAT_ENTRY_DEFAULTS,
  parseChatEntryDefaults,
  type ChatEntryDefault,
  type ChatEntryDefaults,
  type ChatEntryKind,
  type ChatExecutorDescriptor,
} from '@/modules/chat/utils/request';

interface TaskEntryDefaultsProps {
  subtasksEnabled: boolean;
  workflowsEnabled: boolean;
}

type LoadState = 'loading' | 'ready' | 'error';
type SaveState = 'idle' | 'saving' | 'saved' | 'error';

const ENTRY_KINDS: ChatEntryKind[] = ['quick_question', 'new_task'];

const depthLabelKeys: Record<ThinkingDepth, string> = {
  low: 'settingsPage.tasks.depthLow',
  medium: 'settingsPage.tasks.depthMedium',
  high: 'settingsPage.tasks.depthHigh',
  max: 'settingsPage.tasks.depthMax',
};

function cloneEntryDefault(profile: ChatEntryDefault): ChatEntryDefault {
  return {
    ...profile,
    conversation_settings: { ...profile.conversation_settings },
  };
}

function cloneDefaults(defaults: ChatEntryDefaults): ChatEntryDefaults {
  return {
    quick_question: cloneEntryDefault(defaults.quick_question),
    new_task: cloneEntryDefault(defaults.new_task),
  };
}

function entryDefaultEquals(left: ChatEntryDefault, right: ChatEntryDefault): boolean {
  return left.thinking_depth === right.thinking_depth
    && left.conversation_settings.chat_executor === right.conversation_settings.chat_executor
    && left.conversation_settings.enable_subagent === right.conversation_settings.enable_subagent
    && left.conversation_settings.enable_workflow === right.conversation_settings.enable_workflow
    && left.conversation_settings.workflow_mode === right.conversation_settings.workflow_mode;
}

export default function TaskEntryDefaults({
  subtasksEnabled,
  workflowsEnabled,
}: TaskEntryDefaultsProps) {
  const { t } = useTranslation();
  const [activeKind, setActiveKind] = useState<ChatEntryKind>('quick_question');
  const [profiles, setProfiles] = useState<ChatEntryDefaults>(() =>
    cloneDefaults(FALLBACK_CHAT_ENTRY_DEFAULTS),
  );
  const [savedProfiles, setSavedProfiles] = useState<ChatEntryDefaults>(() =>
    cloneDefaults(FALLBACK_CHAT_ENTRY_DEFAULTS),
  );
  const [loadState, setLoadState] = useState<LoadState>('loading');
  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [executors, setExecutors] = useState<ChatExecutorDescriptor[]>([]);
  const [executorLoadFailed, setExecutorLoadFailed] = useState(false);
  const savedTimerRef = useRef<number | null>(null);
  const savingRef = useRef(false);
  const saveControllerRef = useRef<AbortController | null>(null);

  const loadProfiles = useCallback(async (signal?: AbortSignal) => {
    setLoadState('loading');
    try {
      const response = await ConversationSettingsApi().getChatSettings({ signal });
      if (signal?.aborted) return;
      const loadedProfiles = cloneDefaults(parseChatEntryDefaults(response.data));
      setProfiles(loadedProfiles);
      setSavedProfiles(cloneDefaults(loadedProfiles));
      setSaveState('idle');
      setLoadState('ready');
    } catch {
      if (!signal?.aborted) setLoadState('error');
    }
  }, []);

  const loadExecutors = useCallback(async (signal?: AbortSignal) => {
    setExecutorLoadFailed(false);
    try {
      const response = await ConversationSettingsApi().listChatExecutors({ signal });
      if (signal?.aborted) return;
      const payload = (response.data as any)?.data ?? response.data;
      const values = Array.isArray(payload?.executors) ? payload.executors : [];
      setExecutors(values.filter(
        (item: ChatExecutorDescriptor) =>
          item && typeof item.id === 'string' && typeof item.display_name === 'string',
      ));
    } catch {
      if (!signal?.aborted) setExecutorLoadFailed(true);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadProfiles(controller.signal);
    void loadExecutors(controller.signal);
    return () => controller.abort();
  }, [loadExecutors, loadProfiles]);

  useEffect(() => () => {
    if (savedTimerRef.current != null) window.clearTimeout(savedTimerRef.current);
    saveControllerRef.current?.abort();
  }, []);

  const hasUnsavedChanges = useMemo(
    () => ENTRY_KINDS.some((kind) => !entryDefaultEquals(profiles[kind], savedProfiles[kind])),
    [profiles, savedProfiles],
  );

  const persistProfiles = async () => {
    if (savingRef.current) return;
    const draft = cloneDefaults(profiles);
    const changedKinds = ENTRY_KINDS.filter(
      (kind) => !entryDefaultEquals(draft[kind], savedProfiles[kind]),
    );
    if (changedKinds.length === 0) return;

    savingRef.current = true;
    if (savedTimerRef.current != null) {
      window.clearTimeout(savedTimerRef.current);
      savedTimerRef.current = null;
    }
    setSaveState('saving');
    const controller = new AbortController();
    saveControllerRef.current = controller;
    try {
      for (const kind of changedKinds) {
        await ConversationSettingsApi().patchChatEntryDefault(kind, draft[kind], {
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        setSavedProfiles((current) => ({
          ...current,
          [kind]: cloneEntryDefault(draft[kind]),
        }));
      }
      setSaveState('saved');
      savedTimerRef.current = window.setTimeout(() => setSaveState('idle'), 1600);
    } catch {
      if (controller.signal.aborted) return;
      setSaveState('error');
    } finally {
      if (saveControllerRef.current === controller) {
        saveControllerRef.current = null;
      }
      savingRef.current = false;
    }
  };

  const updateActiveProfile = (
    update: (profile: ChatEntryDefault) => ChatEntryDefault,
  ) => {
    if (savedTimerRef.current != null) {
      window.clearTimeout(savedTimerRef.current);
      savedTimerRef.current = null;
    }
    setProfiles((current) => ({
      ...current,
      [activeKind]: update(current[activeKind]),
    }));
    setSaveState('idle');
  };

  const activeProfile = profiles[activeKind];
  const settings = activeProfile.conversation_settings;
  const controlsDisabled = loadState !== 'ready' || saveState === 'saving';
  const workflowValue = settings.enable_workflow ? settings.workflow_mode : 'disabled';
  const executorCatalog = useMemo(
    () => buildExecutorCatalog(
      executors,
      settings.chat_executor,
      t('chat.conversationConfigExecutorUnavailable'),
    ),
    [executors, settings.chat_executor, t],
  );

  const fieldRows = loadState === 'loading' ? (
    <div className="settings-entry-defaults-loading">
      <Skeleton active paragraph={{ rows: 4 }} />
    </div>
  ) : (
    <div className="settings-entry-defaults-fields">
      <div className="settings-entry-defaults-row">
        <div><strong>{t('settingsPage.tasks.thinkingDepth')}</strong><p>{t('settingsPage.tasks.thinkingDepthDesc')}</p></div>
        <Radio.Group
          className="settings-entry-defaults-choice"
          optionType="button"
          buttonStyle="solid"
          value={activeProfile.thinking_depth}
          disabled={controlsDisabled}
          aria-label={t('settingsPage.tasks.thinkingDepth')}
          onChange={(event: RadioChangeEvent) => updateActiveProfile((profile) => ({
            ...profile,
            thinking_depth: event.target.value as ThinkingDepth,
          }))}
        >
          {THINKING_DEPTH_VALUES.map((depth) => (
            <Radio.Button key={depth} value={depth}>{t(depthLabelKeys[depth])}</Radio.Button>
          ))}
        </Radio.Group>
      </div>

      <div className="settings-entry-defaults-row">
        <div><strong>{t('chat.conversationConfigExecutor')}</strong><p>{t('settingsPage.tasks.executorDesc')}</p></div>
        <Radio.Group
          className="settings-entry-defaults-choice settings-entry-defaults-executors"
          optionType="button"
          buttonStyle="solid"
          value={settings.chat_executor}
          disabled={controlsDisabled}
          aria-label={t('chat.conversationConfigExecutor')}
          onChange={(event: RadioChangeEvent) => updateActiveProfile((profile) => ({
            ...profile,
            conversation_settings: {
              ...profile.conversation_settings,
              chat_executor: event.target.value,
            },
          }))}
        >
          {executorCatalog.map((executor) => (
            <Radio.Button
              key={executor.id}
              value={executor.id}
              disabled={!executor.available}
              title={executor.available ? undefined : executor.unavailable_reason}
            >
              {executor.display_name}
            </Radio.Button>
          ))}
        </Radio.Group>
      </div>
      {executorLoadFailed ? (
        <Alert
          className="settings-entry-defaults-inline-alert"
          type="warning"
          showIcon
          message={t('settingsPage.tasks.executorLoadFailed')}
          action={<Button size="small" onClick={() => void loadExecutors()}>{t('settingsPage.retry')}</Button>}
        />
      ) : null}

      <div className="settings-entry-defaults-row">
        <div>
          <strong>{t('chat.conversationConfigWorkflowExecution')}</strong>
          <p>{workflowsEnabled ? t('settingsPage.tasks.workflowDefaultDesc') : t('settingsPage.tasks.workflowMasterOff')}</p>
        </div>
        <Radio.Group
          className="settings-entry-defaults-choice"
          optionType="button"
          buttonStyle="solid"
          value={workflowValue}
          disabled={controlsDisabled || !workflowsEnabled}
          aria-label={t('chat.conversationConfigWorkflowExecution')}
          onChange={(event: RadioChangeEvent) => updateActiveProfile((profile) => {
            const value = event.target.value as 'auto' | 'dynamic' | 'disabled';
            return {
              ...profile,
              conversation_settings: {
                ...profile.conversation_settings,
                enable_workflow: value !== 'disabled',
                workflow_mode: value === 'auto' ? 'auto' : value === 'dynamic'
                  ? 'dynamic'
                  : profile.conversation_settings.workflow_mode,
              },
            };
          })}
        >
          <Radio.Button value="auto">{t('chat.conversationConfigWorkflowAuto')}</Radio.Button>
          <Radio.Button value="dynamic">{t('chat.conversationConfigWorkflowApproval')}</Radio.Button>
          <Radio.Button value="disabled">{t('chat.conversationConfigWorkflowDisabled')}</Radio.Button>
        </Radio.Group>
      </div>

      <div className="settings-entry-defaults-row">
        <div>
          <strong>{t('chat.conversationConfigEnableSubagent')}</strong>
          <p>{subtasksEnabled ? t('settingsPage.tasks.subtaskDefaultDesc') : t('settingsPage.tasks.subtaskMasterOff')}</p>
        </div>
        <Switch
          className="settings-ref-switch"
          checked={settings.enable_subagent}
          disabled={controlsDisabled || !subtasksEnabled}
          aria-label={t('chat.conversationConfigEnableSubagent')}
          onChange={(checked: boolean) => updateActiveProfile((profile) => ({
            ...profile,
            conversation_settings: {
              ...profile.conversation_settings,
              enable_subagent: checked,
            },
          }))}
        />
      </div>
    </div>
  );

  return (
    <section
      className="settings-entry-defaults"
      aria-label={t('settingsPage.tasks.defaultsTitle')}
      aria-busy={loadState === 'loading' || saveState === 'saving'}
    >
      {loadState === 'error' ? (
        <Alert
          className="settings-entry-defaults-load-error"
          type="error"
          showIcon
          message={t('settingsPage.tasks.defaultsLoadFailed')}
          description={t('settingsPage.tasks.defaultsLoadFailedDesc')}
          action={<Button size="small" icon={<ReloadOutlined />} onClick={() => void loadProfiles()}>{t('settingsPage.retry')}</Button>}
        />
      ) : null}
      {saveState === 'error' ? (
        <Alert
          className="settings-entry-defaults-load-error"
          type="error"
          showIcon
          message={t('settingsPage.tasks.defaultsSaveFailed')}
          description={t('settingsPage.tasks.defaultsSaveFailedDesc')}
          action={<Button size="small" onClick={() => void persistProfiles()}>{t('settingsPage.retry')}</Button>}
        />
      ) : null}

      <div className="settings-entry-defaults-card">
        <Tabs
          activeKey={activeKind}
          onChange={(key: string) => setActiveKind(key as ChatEntryKind)}
          items={([
            ['quick_question', t('layout.newChat')],
            ['new_task', t('layout.newTask')],
          ] as const).map(([key, label]) => ({
            key,
            label,
            disabled: saveState === 'saving' && key !== activeKind,
            children: fieldRows,
          }))}
        />
        <footer className="settings-entry-defaults-footer">
          <span className={`settings-entry-defaults-save-state is-${saveState}`} role="status" aria-live="polite">
            {saveState === 'saving'
              ? t('settingsPage.tasks.defaultsSaving')
              : saveState === 'saved'
                ? t('settingsPage.tasks.defaultsSaved')
                : hasUnsavedChanges
                  ? t('settingsPage.tasks.defaultsUnsaved')
                  : ''}
          </span>
          <Button
            type="primary"
            loading={saveState === 'saving'}
            disabled={loadState !== 'ready' || !hasUnsavedChanges || saveState === 'saving'}
            onClick={() => void persistProfiles()}
          >
            {t('settingsPage.tasks.defaultsSaveAction')}
          </Button>
        </footer>
      </div>
    </section>
  );
}
