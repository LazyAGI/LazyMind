import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CHAT_ENTRY_DEFAULTS_CHANGED_EVENT } from '@/modules/chat/constants/chat';
import { useSettingsDraft } from '@/modules/settings/SettingsNavigationGuard';
import { Alert, Button, Radio, Select, Skeleton, Switch } from 'antd';
import type { RadioChangeEvent } from 'antd';
import {
  CheckOutlined,
  ExclamationCircleOutlined,
  FileDoneOutlined,
  LoadingOutlined,
  InfoCircleOutlined,
  MessageOutlined,
  ReloadOutlined,
  SettingOutlined,
} from '@ant-design/icons';
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
  onConnectExecutors?: () => void;
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
  onConnectExecutors,
}: TaskEntryDefaultsProps) {
  const { t } = useTranslation();
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
  const profilesRef = useRef(profiles);
  const savedProfilesRef = useRef(savedProfiles);
  const savingRef = useRef(false);
  const saveControllerRef = useRef<AbortController | null>(null);

  const loadProfiles = useCallback(async (signal?: AbortSignal) => {
    setLoadState('loading');
    try {
      const response = await ConversationSettingsApi().getChatSettings({ signal });
      if (signal?.aborted) return;
      const loadedProfiles = cloneDefaults(parseChatEntryDefaults(response.data));
      profilesRef.current = loadedProfiles;
      savedProfilesRef.current = cloneDefaults(loadedProfiles);
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
      const values = response.data.data.executors;
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
    saveControllerRef.current?.abort();
  }, []);

  const hasUnsavedChanges = useMemo(
    () => ENTRY_KINDS.some((kind) => !entryDefaultEquals(profiles[kind], savedProfiles[kind])),
    [profiles, savedProfiles],
  );

  const persistProfiles = async () => {
    if (savingRef.current) return false;
    const nextChangedKind = () => ENTRY_KINDS.find(
      (kind) => !entryDefaultEquals(profilesRef.current[kind], savedProfilesRef.current[kind]),
    );
    if (!nextChangedKind()) {
      setSaveState('idle');
      return true;
    }

    savingRef.current = true;
    setSaveState('saving');
    const controller = new AbortController();
    saveControllerRef.current = controller;
    try {
      // Serialize saves and read the latest draft after each response. Changes made
      // while a request is pending must not be overwritten or left unsaved.
      let kind: ChatEntryKind | undefined;
      while ((kind = nextChangedKind())) {
        const draft = cloneEntryDefault(profilesRef.current[kind]);
        await ConversationSettingsApi().patchChatEntryDefault(kind, draft, {
          signal: controller.signal,
        });
        if (controller.signal.aborted) return false;
        savedProfilesRef.current = { ...savedProfilesRef.current, [kind]: draft };
        setSavedProfiles(savedProfilesRef.current);
        window.dispatchEvent(new Event(CHAT_ENTRY_DEFAULTS_CHANGED_EVENT));
      }
      setSaveState('saved');
      return true;
    } catch {
      if (controller.signal.aborted) return false;
      setSaveState('error');
      return false;
    } finally {
      if (saveControllerRef.current === controller) {
        saveControllerRef.current = null;
      }
      savingRef.current = false;
    }
  };

  useSettingsDraft({
    dirty: hasUnsavedChanges,
    saving: saveState === 'saving',
    save: persistProfiles,
    discard: () => {
      profilesRef.current = cloneDefaults(savedProfilesRef.current);
      setProfiles(profilesRef.current);
      setSaveState('idle');
    },
  });

  const updateProfile = (
    kind: ChatEntryKind,
    update: (profile: ChatEntryDefault) => ChatEntryDefault,
  ) => {
    profilesRef.current = {
      ...profilesRef.current,
      [kind]: update(profilesRef.current[kind]),
    };
    setProfiles(profilesRef.current);
    void persistProfiles();
  };

  const controlsDisabled = loadState !== 'ready';
  const hasUnavailableExecutors = executors.some((executor) => !executor.available);

  const renderFields = (kind: ChatEntryKind) => {
    const profile = profiles[kind];
    const settings = profile.conversation_settings;
    const workflowValue = settings.enable_workflow ? settings.workflow_mode : 'disabled';
    const executorCatalog = buildExecutorCatalog(
      executors,
      settings.chat_executor,
      t('chat.conversationConfigExecutorUnavailable'),
    );
    const selectedExecutor = executorCatalog.find((executor) => executor.id === settings.chat_executor);

    return loadState === 'loading' ? (
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
          value={profile.thinking_depth}
          disabled={controlsDisabled}
          aria-label={t('settingsPage.tasks.thinkingDepth')}
          onChange={(event: RadioChangeEvent) => updateProfile(kind, (current) => ({
            ...current,
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
        <div className="settings-entry-defaults-executor-control">
        <Select
          className="settings-entry-defaults-select"
          value={settings.chat_executor}
          disabled={controlsDisabled}
          aria-label={t('chat.conversationConfigExecutor')}
          options={executorCatalog.map((executor) => ({
            value: executor.id,
            label: executor.display_name,
            disabled: !executor.available,
            title: executor.unavailable_reason,
          }))}
          popupRender={(menu) => (
            <>
              {menu}
              {hasUnavailableExecutors && onConnectExecutors ? (
                <div className="settings-entry-defaults-connect">
                  <span>{t('settingsPage.tasks.executorConnectHint')}</span>
                  <Button type="link" size="small" onClick={onConnectExecutors}>
                    {t('settingsPage.tasks.executorConnectAction')}
                  </Button>
                </div>
              ) : null}
            </>
          )}
          onChange={(value: string) => updateProfile(kind, (current) => ({
            ...current,
            conversation_settings: {
              ...current.conversation_settings,
              chat_executor: value,
            },
          }))}
        />
        {selectedExecutor && !selectedExecutor.available ? (
          <button type="button" className="settings-entry-defaults-unavailable" onClick={onConnectExecutors}>
            <InfoCircleOutlined /> {t('settingsPage.tasks.executorSelectedUnavailable')}
          </button>
        ) : null}
        </div>
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
        <div>
        <Radio.Group
          className="settings-entry-defaults-choice"
          optionType="button"
          buttonStyle="solid"
          value={workflowValue}
          disabled={controlsDisabled || !workflowsEnabled}
          aria-label={t('chat.conversationConfigWorkflowExecution')}
          onChange={(event: RadioChangeEvent) => updateProfile(kind, (profile) => {
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
        <p className="settings-entry-defaults-mode-description">{t({
          auto: 'chat.conversationConfigWorkflowAutoDesc',
          dynamic: 'chat.conversationConfigWorkflowApprovalDesc',
          disabled: 'chat.conversationConfigWorkflowDisabledDesc',
        }[workflowValue])}</p>
        </div>
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
          onChange={(checked: boolean) => updateProfile(kind, (profile) => ({
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
  };

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

      <div className="settings-entry-defaults-intro">
        <span className="settings-entry-defaults-intro-icon"><SettingOutlined /></span>
        <div><strong>{t('settingsPage.tasks.defaultsTitle')}</strong><p>{t('settingsPage.tasks.defaultsIntro')}</p></div>
        <div className="settings-entry-defaults-intro-note">
          <span><InfoCircleOutlined /> {t('settingsPage.tasks.defaultsScope')}</span>
          {loadState === 'ready' && <span className={`settings-entry-defaults-save-state is-${saveState}`} role="status" aria-live="polite">
            {saveState === 'saving' ? <LoadingOutlined /> : saveState === 'saved' ? <CheckOutlined /> : null}
            {t(saveState === 'saving' ? 'settingsPage.tasks.defaultsSaving'
              : saveState === 'saved' ? 'settingsPage.tasks.defaultsSaved'
                : saveState === 'error' ? 'settingsPage.tasks.defaultsSaveFailed'
                  : 'settingsPage.tasks.defaultsAutosave')}
          </span>}
        </div>
      </div>
      {([
        ['quick_question', 'settingsPage.tasks.quickDefaultsTitle', 'settingsPage.tasks.quickDefaultsDesc', <MessageOutlined />],
        ['new_task', 'settingsPage.tasks.taskDefaultsTitle', 'settingsPage.tasks.taskDefaultsDesc', <FileDoneOutlined />],
      ] as const).map(([kind, titleKey, descKey, icon]) => (
        <article className={`settings-entry-defaults-card is-${kind}`} key={kind}>
          <header className="settings-entry-defaults-card-header">
            <span className="settings-entry-defaults-card-icon">{icon}</span>
            <div><h2>{t(titleKey)}</h2><p>{t(descKey)}</p></div>
          </header>
          {renderFields(kind)}
        </article>
      ))}
      <aside className="settings-entry-defaults-notice" aria-label={t('settingsPage.tasks.workflowPreferenceNoteTitle')}>
        <ExclamationCircleOutlined aria-hidden />
        <div><strong>{t('settingsPage.tasks.workflowPreferenceNoteTitle')}</strong>
          <p>{t('settingsPage.tasks.workflowPreferenceNote')}</p>
        </div>
      </aside>
    </section>
  );
}
