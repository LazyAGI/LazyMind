import React, { useEffect } from "react";
import { usePluginSession, useSlot } from "@/modules/chat/hooks/usePlugin";
import type { PluginSession, SlotRevision } from "@/modules/chat/store/pluginPanel";
import { SlotRenderer } from "./SlotComponents";
import "./PluginPanel.scss";

interface TabDef {
  id: string;
  label: string;
  slots: SlotDef[];
}

interface SlotDef {
  id: string;
  label: string;
  type: "image" | "text" | "file";
  cardinality?: "single" | "list";
}

interface PluginUI {
  tabs?: TabDef[];
}

function getPluginUI(session: PluginSession): PluginUI {
  // The plugin UI definition is passed down via chat_context from Go.
  // In the current implementation we don't persist full plugin YAML in the DB,
  // so we fall back to a generic auto-render mode based on available slots.
  return {};
}

/**
 * AutoSlotGrid renders all available slot revisions in a responsive grid,
 * without requiring a pre-defined UI spec. Used when no plugin UI tabs are configured.
 */
function AutoSlotGrid({
  session,
  conversationId,
}: {
  session: PluginSession;
  conversationId: string;
}) {
  if (!session.slots || session.slots.length === 0) {
    return (
      <div className="plugin-panel__empty" role="status" aria-live="polite">
        <span>Waiting for results…</span>
      </div>
    );
  }

  // Group by slot_id and show the selected revisions.
  const bySlot: Record<string, SlotRevision[]> = {};
  for (const s of session.slots) {
    if (!s.selected) continue;
    if (!bySlot[s.slot_id]) bySlot[s.slot_id] = [];
    bySlot[s.slot_id].push(s);
  }

  return (
    <div className="plugin-panel__auto-grid">
      {Object.entries(bySlot).map(([slotId, revisions]) => (
        <div key={slotId} className="plugin-panel__slot-group">
          <span className="plugin-panel__slot-label">{slotId}</span>
          <div className="plugin-panel__slot-items">
            {revisions.map((rev) => (
              <SlotRenderer
                key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                conversationId={conversationId}
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
function TabSlotGrid({
  tab,
  session,
  conversationId,
  selectRevision,
}: {
  tab: TabDef;
  session: PluginSession;
  conversationId: string;
  selectRevision: (slotId: string, revision: number) => void;
}) {
  return (
    <div className="plugin-panel__tab-content">
      {tab.slots.map((slotDef) => {
        const revisions = (session.slots ?? []).filter(
          (s) => s.slot_id === slotDef.id && s.selected,
        );
        return (
          <div key={slotDef.id} className="plugin-panel__named-slot">
            {slotDef.label && (
              <span className="plugin-panel__slot-label">{slotDef.label}</span>
            )}
            {revisions.length === 0 ? (
              <div
                className="plugin-panel__slot-placeholder"
                aria-label={`${slotDef.label} pending`}
              >
                <span>—</span>
              </div>
            ) : (
              revisions.map((rev) => (
                <SlotRenderer
                  key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                  conversationId={conversationId}
                  slot={rev}
                />
              ))
            )}
          </div>
        );
      })}
    </div>
  );
}

interface PluginPanelProps {
  conversationId: string;
  /** Poll interval in ms for slot refreshes (default: 3000). */
  pollIntervalMs?: number;
}

/**
 * PluginPanel shows the active plugin session output.
 * - When plugin UI tabs are defined, renders using the tab layout (custom mode).
 * - Otherwise uses the auto-grid layout.
 * Polls for slot updates while session is active.
 */
export function PluginPanel({ conversationId, pollIntervalMs = 3000 }: PluginPanelProps) {
  const { session, loading, refresh, selectRevision, advance, retry } = usePluginSession(conversationId);
  const [activeTab, setActiveTab] = React.useState(0);

  // Poll for slot updates while session is active.
  useEffect(() => {
    if (!session || session.status !== "active") return;
    const id = setInterval(refresh, pollIntervalMs);
    return () => clearInterval(id);
  }, [session, refresh, pollIntervalMs]);

  if (loading && !session) {
    return <div className="plugin-panel plugin-panel--loading" role="status" aria-label="Loading plugin panel" />;
  }

  if (!session) {
    return null;
  }

  const ui = getPluginUI(session);
  const tabs: TabDef[] = ui.tabs ?? [];
  const hasTabs = tabs.length > 0;

  const statusLabel: Record<string, string> = {
    active: "Running",
    completed: "Done",
    failed: "Failed",
    waiting: "Waiting",
  };

  // Show action buttons only in manual-mode states (waiting = step done, active = step running).
  // "completed" and "failed" sessions don't need controls.
  const showActions = session.status === "waiting" || session.status === "active";
  // Buttons are disabled while a SubAgent is actively running.
  const buttonsDisabled = session.status === "active";

  return (
    <div
      className={`plugin-panel plugin-panel--${session.status}`}
      data-session-id={session.session_id}
      aria-label="Plugin Panel"
    >
      <div className="plugin-panel__header">
        <span className="plugin-panel__title">{session.plugin_id}</span>
        <span
          className={`plugin-panel__status plugin-panel__status--${session.status}`}
          aria-label={`Status: ${statusLabel[session.status] ?? session.status}`}
        >
          {statusLabel[session.status] ?? session.status}
        </span>
      </div>

      {hasTabs && (
        <div className="plugin-panel__tabs" role="tablist">
          {tabs.map((tab, idx) => (
            <button
              key={tab.id}
              role="tab"
              aria-selected={idx === activeTab}
              aria-controls={`plugin-tab-panel-${tab.id}`}
              className={`plugin-panel__tab${idx === activeTab ? " plugin-panel__tab--active" : ""}`}
              onClick={() => setActiveTab(idx)}
              type="button"
            >
              {tab.label}
            </button>
          ))}
        </div>
      )}

      <div className="plugin-panel__body">
        {hasTabs ? (
          tabs.map((tab, idx) => (
            <div
              key={tab.id}
              id={`plugin-tab-panel-${tab.id}`}
              role="tabpanel"
              hidden={idx !== activeTab}
            >
              <TabSlotGrid
                tab={tab}
                session={session}
                conversationId={conversationId}
                selectRevision={selectRevision}
              />
            </div>
          ))
        ) : (
          <AutoSlotGrid session={session} conversationId={conversationId} />
        )}
      </div>

      {showActions && (
        <div className="plugin-panel__footer" role="group" aria-label="Session controls">
          <button
            type="button"
            className="plugin-panel__action-btn plugin-panel__action-btn--secondary"
            disabled={buttonsDisabled}
            aria-disabled={buttonsDisabled}
            onClick={retry}
            title={buttonsDisabled ? "Waiting for step to finish…" : "Retry current step"}
          >
            Retry
          </button>
          <button
            type="button"
            className="plugin-panel__action-btn plugin-panel__action-btn--primary"
            disabled={buttonsDisabled}
            aria-disabled={buttonsDisabled}
            onClick={advance}
            title={buttonsDisabled ? "Waiting for step to finish…" : "Continue to next step"}
          >
            Continue
          </button>
        </div>
      )}
    </div>
  );
}
