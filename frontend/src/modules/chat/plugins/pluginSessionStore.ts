import { create } from 'zustand';
import type { PluginEvent, PluginSessionState } from './types';
import { useActivePluginContextStore } from './activePluginContextStore';

interface PluginSessionStore {
  sessions: Record<string, PluginSessionState>;
  handleEvent: (event: PluginEvent) => void;
  getSession: (sessionId: string) => PluginSessionState | undefined;
  clearSession: (sessionId: string) => void;
  /** Restore a plugin session from persisted history (no SSE, artifacts already known). */
  restoreSession: (sessionId: string, pluginId: string, artifacts: Record<string, unknown>) => void;
}

export const usePluginSessionStore = create<PluginSessionStore>((set, get) => ({
  sessions: {},

  handleEvent: (rawEvent: PluginEvent) => {
    // Go wraps events in { type: 'plugin_event', data: { ... } }.
    const event: PluginEvent = (rawEvent.data as PluginEvent) ?? rawEvent;
    const sessionId = event.plugin_session_id ?? '';

    switch (event.type) {
      case 'mount': {
        if (!sessionId) return;
        set((state) => ({
          sessions: {
            ...state.sessions,
            [sessionId]: {
              sessionId,
              pluginId: event.plugin_id ?? '',
              currentStep: '',
              artifacts: {},
              stepProgress: null,
              isWaiting: false,
              stepError: null,
            },
          },
        }));
        break;
      }

      case 'artifact': {
        if (!sessionId || !event.artifact_id) return;
        set((state) => {
          const session = state.sessions[sessionId];
          if (!session) return state;
          return {
            sessions: {
              ...state.sessions,
              [sessionId]: {
                ...session,
                artifacts: {
                  ...session.artifacts,
                  [event.artifact_id!]: event.value,
                },
              },
            },
          };
        });
        break;
      }

      case 'step_change': {
        if (!sessionId || !event.step_id) return;
        set((state) => {
          const session = state.sessions[sessionId];
          if (!session) return state;
          return {
            sessions: {
              ...state.sessions,
              [sessionId]: { ...session, currentStep: event.step_id! },
            },
          };
        });
        // Sync to activePluginContextStore.
        const activeCtx = useActivePluginContextStore.getState().context;
        if (activeCtx && activeCtx.plugin_session_id === sessionId) {
          useActivePluginContextStore.getState().setContext({
            ...activeCtx,
            step: event.step_id!,
          });
        }
        break;
      }

      case 'progress': {
        if (!sessionId) return;
        set((state) => {
          const session = state.sessions[sessionId];
          if (!session) return state;
          return {
            sessions: {
              ...state.sessions,
              [sessionId]: {
                ...session,
                stepProgress: {
                  progress: event.progress ?? 0,
                  message: event.message ?? '',
                },
              },
            },
          };
        });
        break;
      }

      case 'step_done': {
        if (!sessionId) return;
        set((state) => {
          const session = state.sessions[sessionId];
          if (!session) return state;
          return {
            sessions: {
              ...state.sessions,
              [sessionId]: { ...session, stepProgress: null },
            },
          };
        });
        break;
      }

      case 'step_waiting': {
        if (!sessionId || !event.step_id) return;
        set((state) => {
          const session = state.sessions[sessionId];
          if (!session) return state;
          return {
            sessions: {
              ...state.sessions,
              [sessionId]: { ...session, isWaiting: true },
            },
          };
        });
        // Update activePluginContextStore so advance message carries correct step.
        const ctx = useActivePluginContextStore.getState().context;
        if (ctx && ctx.plugin_session_id === sessionId) {
          useActivePluginContextStore.getState().setContext({
            ...ctx,
            step: event.step_id!,
          });
        }
        break;
      }

      case 'step_error': {
        if (!sessionId) return;
        set((state) => {
          const session = state.sessions[sessionId];
          if (!session) return state;
          return {
            sessions: {
              ...state.sessions,
              [sessionId]: {
                ...session,
                stepProgress: null,
                isWaiting: false,
                stepError: event.error ?? 'Unknown error',
              },
            },
          };
        });
        break;
      }
    }
  },

  getSession: (sessionId) => get().sessions[sessionId],

  clearSession: (sessionId) => {
    set((state) => {
      const { [sessionId]: _, ...rest } = state.sessions;
      return { sessions: rest };
    });
  },

  restoreSession: (sessionId, pluginId, artifacts) => {
    set((state) => ({
      sessions: {
        ...state.sessions,
        [sessionId]: {
          sessionId,
          pluginId,
          currentStep: '',
          artifacts,
          stepProgress: null,
          isWaiting: false,
          stepError: null,
        },
      },
    }));
  },
}));
