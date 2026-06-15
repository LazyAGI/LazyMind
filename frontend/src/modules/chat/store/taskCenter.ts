import { create } from "zustand";
import { AgentAppsAuth } from "@/components/auth";
import { Method, SSE } from "@/modules/chat/utils/sse";
import { TaskServiceApi, taskStreamUrl } from "@/modules/chat/utils/request";
import UIUtils from "@/modules/chat/utils/ui";

export type TaskStatus =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "interrupted"
  | "canceled";

export interface TaskArtifact {
  artifact_key: string;
  content_type: string;
  seq: number;
  value: any;
}

export interface TaskLogEntry {
  type: "text" | "think";
  content: string;
}

export interface SubAgentTask {
  task_id: string;
  conversation_id?: string;
  title: string;
  agent_type: string;
  mode: string;
  status: TaskStatus;
  progress_pct: number;
  current_phase?: string;
  estimated_sec?: number;
  summary?: string;
  output_artifact_keys?: string[];
  artifacts: TaskArtifact[];
  execution_log: TaskLogEntry[];
}

const TERMINAL: TaskStatus[] = [
  "succeeded",
  "failed",
  "interrupted",
  "canceled",
];

function artifactKey(a: TaskArtifact): string {
  return `${a.artifact_key}#${a.seq}`;
}

interface TaskCenterStore {
  // tasks keyed by conversation_id, each an ordered list.
  tasksByConversation: Record<string, SubAgentTask[]>;
  activeConversationId: string;
  // live SSE connections keyed by task_id.
  _streams: Record<string, SSE>;

  setActiveConversation: (conversationId: string) => void;
  getTasks: (conversationId: string) => SubAgentTask[];
  upsertTask: (conversationId: string, task: Partial<SubAgentTask> & { task_id: string }) => void;
  applyTaskEvent: (conversationId: string, taskId: string, event: any) => void;
  subscribeTask: (conversationId: string, taskId: string) => void;
  unsubscribeTask: (taskId: string) => void;
  loadConversationTasks: (conversationId: string) => Promise<void>;
  reset: (conversationId: string) => void;
}

export const useTaskCenterStore = create<TaskCenterStore>()((set, get) => ({
  tasksByConversation: {},
  activeConversationId: "",
  _streams: {},

  setActiveConversation: (conversationId) => {
    set({ activeConversationId: conversationId });
  },

  getTasks: (conversationId) => {
    return get().tasksByConversation[conversationId] ?? [];
  },

  upsertTask: (conversationId, task) => {
    set((state) => {
      const list = state.tasksByConversation[conversationId] ?? [];
      const idx = list.findIndex((t) => t.task_id === task.task_id);
      let next: SubAgentTask[];
      if (idx >= 0) {
        next = list.slice();
        next[idx] = { ...next[idx], ...task };
      } else {
        next = [
          ...list,
          {
            task_id: task.task_id,
            title: task.title ?? "",
            agent_type: task.agent_type ?? "",
            mode: task.mode ?? "auto",
            status: (task.status as TaskStatus) ?? "pending",
            progress_pct: task.progress_pct ?? 0,
            current_phase: task.current_phase,
            estimated_sec: task.estimated_sec,
            summary: task.summary,
            output_artifact_keys: task.output_artifact_keys,
            artifacts: task.artifacts ?? [],
            execution_log: task.execution_log ?? [],
            conversation_id: conversationId,
          },
        ];
      }
      return {
        tasksByConversation: {
          ...state.tasksByConversation,
          [conversationId]: next,
        },
      };
    });
  },

  applyTaskEvent: (conversationId, taskId, event) => {
    set((state) => {
      const list = state.tasksByConversation[conversationId] ?? [];
      const idx = list.findIndex((t) => t.task_id === taskId);
      if (idx < 0) {
        return state;
      }
      const task = { ...list[idx] };
      switch (event.type) {
        case "task_start":
          task.status = "running";
          break;
        case "progress":
          task.status = "running";
          task.progress_pct = event.progress ?? task.progress_pct;
          task.current_phase = event.current_phase ?? task.current_phase;
          task.estimated_sec = event.estimated_sec ?? task.estimated_sec;
          break;
        case "artifact": {
          const newArtifact: TaskArtifact = {
            artifact_key: event.artifact_key,
            content_type: event.content_type,
            seq: event.seq ?? 1,
            value: event.value,
          };
          const existing = task.artifacts ?? [];
          if (!existing.some((a) => artifactKey(a) === artifactKey(newArtifact))) {
            task.artifacts = [...existing, newArtifact];
          }
          break;
        }
        case "done":
          task.status = (event.status as TaskStatus) ?? "succeeded";
          task.progress_pct = 100;
          task.summary = event.summary ?? task.summary;
          break;
        case "error":
          task.status = (event.status as TaskStatus) ?? "failed";
          task.summary = event.message ?? task.summary;
          break;
        case "text": {
          const textContent = event.text ?? "";
          if (textContent) {
            task.execution_log = [
              ...(task.execution_log ?? []),
              { type: "text", content: textContent },
            ];
          }
          break;
        }
        case "think": {
          const thinkContent = event.think ?? "";
          if (thinkContent) {
            task.execution_log = [
              ...(task.execution_log ?? []),
              { type: "think", content: thinkContent },
            ];
          }
          break;
        }
        default:
          return state;
      }
      const next = list.slice();
      next[idx] = task;
      return {
        tasksByConversation: {
          ...state.tasksByConversation,
          [conversationId]: next,
        },
      };
    });
  },

  subscribeTask: (conversationId, taskId) => {
    const existing = get()._streams[taskId];
    if (existing) {
      return;
    }
    const sse = new SSE(taskStreamUrl(taskId), {
      method: Method.GET,
      headers: {
        Accept: "text/event-stream",
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 3600000,
      callbacks: {
        message: (e: CustomEvent) => {
          const raw = (e as any).data;
          if (!raw || raw === "[DONE]") {
            return;
          }
          const event = UIUtils.jsonParser(raw);
          if (!event || !event.type) {
            return;
          }
          get().applyTaskEvent(conversationId, taskId, event);
          if (event.type === "done" || event.type === "error") {
            get().unsubscribeTask(taskId);
          }
        },
        error: () => {
          get().unsubscribeTask(taskId);
        },
      },
    });
    set((state) => ({ _streams: { ...state._streams, [taskId]: sse } }));
  },

  unsubscribeTask: (taskId) => {
    const sse = get()._streams[taskId];
    if (sse) {
      try {
        sse.close();
      } catch {
        // ignore
      }
    }
    set((state) => {
      const next = { ...state._streams };
      delete next[taskId];
      return { _streams: next };
    });
  },

  loadConversationTasks: async (conversationId) => {
    if (!conversationId) {
      return;
    }
    try {
      const res = await TaskServiceApi().listConversationTasks(conversationId);
      const tasks = res?.data?.data?.tasks ?? res?.data?.tasks ?? [];
      tasks.forEach((t: any) => {
        get().upsertTask(conversationId, {
          task_id: t.task_id,
          title: t.title,
          agent_type: t.agent_type,
          mode: t.mode,
          status: t.status,
          progress_pct: t.progress_pct ?? 0,
          current_phase: t.current_phase,
          estimated_sec: t.estimated_sec,
          summary: t.summary,
          output_artifact_keys: t.output_artifact_keys,
          artifacts: t.artifacts ?? [],
          execution_log: [],
        });
        if (!TERMINAL.includes(t.status)) {
          get().subscribeTask(conversationId, t.task_id);
        }
      });
    } catch {
      // ignore load failures; panel just stays empty.
    }
  },

  reset: (conversationId) => {
    Object.keys(get()._streams).forEach((taskId) => get().unsubscribeTask(taskId));
    set((state) => ({
      tasksByConversation: {
        ...state.tasksByConversation,
        [conversationId]: [],
      },
    }));
  },
}));
