import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { usePluginSession } from '@/modules/chat/hooks/usePlugin';
import { usePluginStore } from '@/modules/chat/store/pluginPanel';
import type { PluginSession, SlotRevision, TabDef, PluginUI } from '@/modules/chat/store/pluginPanel';
import { SlotRenderer } from './SlotComponents';
import './PluginPanel.scss';

interface PluginPanelProps {
  conversationId: string;
  pollIntervalMs?: number;
  /** Called when the user clicks Continue or Retry — simulates sending a user message. */
  onSendMessage?: (text: string) => void;
}

/**
 * AutoSlotGrid renders all available slot revisions in a responsive grid,
 * without requiring a pre-defined UI spec.
 */
function AutoSlotGrid({ session }: { session: PluginSession }) {
  if (!session.slots || session.slots.length === 0) {
    return (
      <div className='plugin-panel__empty' role='status' aria-live='polite'>
        <span>Waiting for results…</span>
      </div>
    );
  }

  const bySlot: Record<string, SlotRevision[]> = {};
  for (const s of session.slots) {
    if (!s.selected) continue;
    if (!bySlot[s.slot_id]) bySlot[s.slot_id] = [];
    bySlot[s.slot_id].push(s);
  }

  return (
    <div className='plugin-panel__auto-grid'>
      {Object.entries(bySlot).map(([slotId, revisions]) => (
        <div key={slotId} className='plugin-panel__slot-group'>
          <span className='plugin-panel__slot-label'>{slotId}</span>
          <div className='plugin-panel__slot-items'>
            {revisions.map((rev) => (
              <SlotRenderer
                key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                slot={rev}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

/**
 * TabSlotGrid renders slots according to the plugin UI tab definition.
 */
function TabSlotGrid({ tab, session }: { tab: TabDef; session: PluginSession }) {
  return (
    <div className='plugin-panel__tab-content'>
      {tab.slots.map((slotDef) => {
        const artifactKey = slotDef.artifact_key ?? slotDef.id;
        const revisions = (session.slots ?? []).filter(
          (s) => s.artifact_key === artifactKey && s.selected,
        );
        const isImageList = slotDef.type === 'image' && slotDef.cardinality === 'list';
        return (
          <div key={slotDef.id} className='plugin-panel__named-slot'>
            {slotDef.label && (
              <span className='plugin-panel__slot-label'>{slotDef.label}</span>
            )}
            {revisions.length === 0 ? (
              <div
                className='plugin-panel__slot-placeholder'
                aria-label={`${slotDef.label} pending`}
              >
                <span>—</span>
              </div>
            ) : isImageList ? (
              <div className='plugin-panel__image-list'>
                {revisions.map((rev) => (
                  <SlotRenderer
                    key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                    slot={rev}
                    cardMode
                    expectedType={slotDef.type}
                  />
                ))}
              </div>
            ) : (
              revisions.map((rev) => (
                <SlotRenderer
                  key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                  slot={rev}
                  expectedType={slotDef.type}
                />
              ))
            )}
          </div>
        );
      })}
    </div>
  );
}

const STATUS_KEY: Record<string, string> = {
  active: 'chat.pluginStatusRunning',
  completed: 'chat.pluginStatusDone',
  failed: 'chat.pluginStatusFailed',
  waiting: 'chat.pluginStatusWaiting',
};

export function PluginPanel({
  conversationId,
  pollIntervalMs = 3000,
  onSendMessage,
}: PluginPanelProps) {
  const { t } = useTranslation();
  const { session, loading, refresh } = usePluginSession(conversationId);
  const [activeTab, setActiveTab] = React.useState(0);
  const [collapsed, setCollapsed] = useState(false);
  const fetchPluginUI = usePluginStore((s) => s.fetchPluginUI);
  const pluginUIByPlugin = usePluginStore((s) => s.pluginUIByPlugin);
  const [ui, setUI] = useState<PluginUI>({});

  useEffect(() => {
    if (!session?.plugin_id) return;
    const cached = pluginUIByPlugin[session.plugin_id];
    if (cached) { setUI(cached); return; }
    fetchPluginUI(session.plugin_id).then(setUI);
  }, [session?.plugin_id, fetchPluginUI, pluginUIByPlugin]);

  useEffect(() => {
    if (!session || session.status !== 'active') return;
    const id = setInterval(refresh, pollIntervalMs);
    return () => clearInterval(id);
  }, [session, refresh, pollIntervalMs]);

  if (loading && !session) {
    return (
      <div
        className='plugin-panel plugin-panel--loading'
        role='status'
        aria-label='Loading plugin panel'
      />
    );
  }

  if (!session) return null;

  const tabs: TabDef[] = ui.tabs ?? [];
  const hasTabs = tabs.length > 0;

  const showActions =
    session.status === 'waiting' ||
    session.status === 'active' ||
    session.status === 'completed' ||
    session.status === 'failed';
  // Both buttons are disabled while a SubAgent is running.
  const buttonsDisabled = session.status === 'active';
  // "Continue" is only shown when there is a next step to advance to.
  // completed = last step already done (Driver returned DONE), failed = terminal.
  const showContinue =
    session.status === 'waiting' || session.status === 'active';

  function handleContinue() {
    if (buttonsDisabled) return;
    onSendMessage?.(t('chat.pluginContinue'));
  }

  function handleRetry() {
    if (buttonsDisabled) return;
    onSendMessage?.(t('chat.pluginRetry'));
  }

  return (
    <div
      className={`plugin-panel plugin-panel--${session.status}${collapsed ? ' plugin-panel--collapsed' : ''}`}
      data-session-id={session.session_id}
      aria-label='Plugin Panel'
    >
      {/* Header */}
      <div className='plugin-panel__header'>
        <span className='plugin-panel__title'>{session.plugin_id}</span>
        <span
          className={`plugin-panel__status plugin-panel__status--${session.status}`}
          aria-label={`Status: ${t(STATUS_KEY[session.status] ?? session.status)}`}
        >
          {t(STATUS_KEY[session.status] ?? session.status)}
        </span>
        <button
          type='button'
          className='plugin-panel__collapse-btn'
          onClick={() => setCollapsed((c) => !c)}
          aria-label={collapsed ? 'Expand panel' : 'Collapse panel'}
          title={collapsed ? 'Expand' : 'Collapse'}
        >
          <svg
            width='12'
            height='12'
            viewBox='0 0 12 12'
            fill='none'
            xmlns='http://www.w3.org/2000/svg'
            className={`plugin-panel__collapse-icon${collapsed ? ' plugin-panel__collapse-icon--up' : ''}`}
          >
            <path d='M2 4L6 8L10 4' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
          </svg>
        </button>
      </div>

      {/* Tabs — step navigator style */}
      {!collapsed && hasTabs && (
        <div className='plugin-panel__tabs' role='tablist'>
          {tabs.map((tab, idx) => (
            <React.Fragment key={tab.id}>
              <button
                role='tab'
                aria-selected={idx === activeTab}
                aria-controls={`plugin-tab-panel-${tab.id}`}
                className={`plugin-panel__tab${idx === activeTab ? ' plugin-panel__tab--active' : ''}${idx < activeTab ? ' plugin-panel__tab--done' : ''}`}
                onClick={() => setActiveTab(idx)}
                type='button'
              >
                <span className='plugin-panel__tab-badge'>{idx + 1}</span>
                <span className='plugin-panel__tab-label'>{tab.label}</span>
              </button>
              {idx < tabs.length - 1 && (
                <span className={`plugin-panel__tab-connector${idx < activeTab ? ' plugin-panel__tab-connector--done' : ''}`} aria-hidden='true' />
              )}
            </React.Fragment>
          ))}
        </div>
      )}

      {/* Body */}
      {!collapsed && (
        <div className='plugin-panel__body'>
          {hasTabs ? (
            tabs.map((tab, idx) => (
              <div
                key={tab.id}
                id={`plugin-tab-panel-${tab.id}`}
                role='tabpanel'
                hidden={idx !== activeTab}
              >
                <TabSlotGrid
                  tab={tab}
                  session={session}
                />
              </div>
            ))
          ) : (
            <AutoSlotGrid session={session} />
          )}
        </div>
      )}

      {/* Footer */}
      {!collapsed && showActions && (
        <div className='plugin-panel__footer' role='group' aria-label='Session controls'>
          <button
            type='button'
            className='plugin-panel__action-btn plugin-panel__action-btn--secondary'
            disabled={buttonsDisabled}
            aria-disabled={buttonsDisabled}
            onClick={handleRetry}
            title={buttonsDisabled ? t('chat.pluginBtnDisabledHint') : t('chat.pluginRetry')}
          >
            {t('chat.pluginRetry')}
          </button>
          {showContinue && (
            <button
              type='button'
              className='plugin-panel__action-btn plugin-panel__action-btn--primary'
              disabled={buttonsDisabled}
              aria-disabled={buttonsDisabled}
              onClick={handleContinue}
              title={buttonsDisabled ? t('chat.pluginBtnDisabledHint') : t('chat.pluginContinue')}
            >
              {t('chat.pluginContinue')}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
