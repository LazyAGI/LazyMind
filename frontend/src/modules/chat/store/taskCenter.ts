import { create } from "zustand";
import { AgentAppsAuth } from "@/components/auth";
import { axiosInstance, localizeErrorCode } from "@/components/request";
import { Method, SSE } from "@/modules/chat/utils/sse";
import { TaskServiceApi, taskStreamUrl, convEventsUrl } from "@/modules/chat/utils/request";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";
import UIUtils from "@/modules/chat/utils/ui";
import { WORKFLOW_GRAPH_REFRESH_EVENT } from "@/components/StateGraphModal";
import { CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT } from "@/modules/chat/constants/chat";

const taskReconnectTimers = new Map<string, ReturnType<typeof setTimeout>>();
const convReconnectTimers = new Map<string, ReturnType<typeof setTimeout>>();

export type TaskStatus =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "interrupted"
  | "canceled";

export interface TaskArtifact {
  slot: string;
  content_type: string;
  seq: number;
  value: any;
}

/** Ephemeral Markdown preview emitted before the task persists its file artifact. */
export interface TaskArtifactStream {
  task_id: string;
  slot: string;
  content_type: string;
  stream_id: string;
  chunk_index: number;
  content: string;
  /** Exact deltas received from the task SSE stream, in server order. */
  deltas?: string[];
  state: "streaming" | "ended" | "aborted" | "ready";
  message?: string;
  artifact?: TaskArtifact;
  final_content?: string;
  final_content_error?: string;
}

export interface ConversationArtifact extends TaskArtifact {
  artifact_id: string;
  conversation_id: string;
  history_id: string;
  producer_type: "main_agent" | "subagent" | string;
  producer_id?: string;
  filename?: string;
  caption?: string;
  created_at?: string;
}

export interface ToolCallItem {
  id: string;
  name: string;
  args: any;
}

export interface ToolResultItem {
  tool_call_id: string;
  name: string;
  result: string;
}

export interface TaskLogEntry {
  type: "text" | "think" | "tool_calls" | "tool_results";
  content: string;
  // For tool_calls type
  tool_calls?: ToolCallItem[];
  // For tool_results type
  tool_results?: ToolResultItem[];
}

export interface SubAgentTask {
  task_id: string;
  conversation_id?: string;
  trigger_history_id?: string;
  seq_in_conversation?: number;
  title: string;
  agent_type: string;
  mode: string;
  status: TaskStatus;
  progress_pct: number;
  current_phase?: string;
  estimated_sec?: number;
  summary?: string;
  output_slots?: string[];
  artifacts: TaskArtifact[];
  artifact_streams: TaskArtifactStream[];
  execution_log: TaskLogEntry[];
}

const TERMINAL: TaskStatus[] = [
  "succeeded",
  "failed",
  "interrupted",
  "canceled",
];

const WRITER_MARKDOWN_STREAM_SLOT_IDS = new Set(['outline_document', 'draft_document']);

function artifactKey(a: TaskArtifact): string {
  return `${a.slot}#${a.seq}`;
}

function isWriterIRArtifact(artifact: TaskArtifact): boolean {
  const value = artifact.value;
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  const format = String(record.document_format ?? "").toLowerCase();
  if (format === "writer_ir" || format === "lmd") return true;
  return [record.filename, record.name, record.path, record.url].some((source) => {
    const path = String(source ?? "").split(/[?#]/, 1)[0].toLowerCase();
    return path.endsWith(".lmd") || path.endsWith("_ir.json");
  });
}

interface TaskCenterStore {
  // tasks keyed by conversation_id, each an ordered list.
  tasksByConversation: Record<string, SubAgentTask[]>;
  artifactsByConversation: Record<string, ConversationArtifact[]>;
  activeConversationId: string;
  // in-flight loadConversationTasks calls keyed by conversation_id.
  _loadingTasks: Record<string, boolean>;
  _loadingArtifacts: Record<string, boolean>;
  // live SSE connections keyed by task_id.
  _streams: Record<string, SSE>;
  // conversation-level events SSE connections keyed by conversation_id.
  _convStreams: Record<string, SSE>;

  setActiveConversation: (conversationId: string) => void;
  getTasks: (conversationId: string) => SubAgentTask[];
  upsertTask: (conversationId: string, task: Partial<SubAgentTask> & { task_id: string }) => void;
  applyTaskEvent: (conversationId: string, taskId: string, event: any) => void;
  loadArtifactStreamContent: (conversationId: string, taskId: string, artifact: TaskArtifact) => Promise<void>;
  subscribeTask: (conversationId: string, taskId: string) => void;
  unsubscribeTask: (taskId: string) => void;
  loadConversationTasks: (conversationId: string) => Promise<void>;
  loadConversationArtifacts: (conversationId: string) => Promise<void>;
  upsertConversationArtifact: (conversationId: string, artifact: ConversationArtifact) => void;
  subscribeConvEvents: (conversationId: string) => void;
  unsubscribeConvEvents: (conversationId: string) => void;
  reset: (conversationId: string) => void;
}

// Convert persisted sub_agent_steps rows back to TaskLogEntry[] for display.
function stepsToExecutionLog(steps: any[]): TaskLogEntry[] {
  if (!steps || steps.length === 0) return [];
  return steps.flatMap((s): TaskLogEntry[] => {
    const role: string = s.role ?? "";
    const content = s.content ?? {};
    if (role === "think") {
      const text: string = content.content ?? "";
      return text ? [{ type: "think", content: text }] : [];
    }
    if (role === "text") {
      const text: string = content.content ?? "";
      return text ? [{ type: "text", content: text }] : [];
    }
    if (role === "assistant") {
      const calls: ToolCallItem[] = (content.tool_calls ?? []).map((tc: any) => ({
        id: tc.id ?? "",
        name: tc.name ?? (tc.function?.name ?? ""),
        args: tc.args ?? tc.function?.arguments ?? {},
      }));
      return calls.length > 0 ? [{ type: "tool_calls", content: "", tool_calls: calls }] : [];
    }
    if (role === "tool") {
      const results: ToolResultItem[] = (content.tool_results ?? []).map((tr: any) => ({
        tool_call_id: tr.id ?? tr.tool_call_id ?? "",
        name: tr.name ?? "",
        result: tr.result ?? tr.content ?? "",
      }));
      return results.length > 0 ? [{ type: "tool_results", content: "", tool_results: results }] : [];
    }
    return [];
  });
}

export const useTaskCenterStore = create<TaskCenterStore>()((set, get) => ({
  tasksByConversation: {},
  artifactsByConversation: {},
  activeConversationId: '',
  _loadingTasks: {},
  _loadingArtifacts: {},
  _streams: {},
  _convStreams: {},

  setActiveConversation: (conversationId) => {
    set({ activeConversationId: conversationId });
  },

  getTasks: (conversationId) => {
    return get().tasksByConversation[conversationId] ?? [];
  },

  upsertConversationArtifact: (conversationId, artifact) => {
    if (!conversationId || !artifact?.artifact_id) return;
    set((state) => {
      const list = state.artifactsByConversation[conversationId] ?? [];
      const idx = list.findIndex((item) => item.artifact_id === artifact.artifact_id);
      const next = list.slice();
      if (idx >= 0) next[idx] = { ...next[idx], ...artifact };
      else next.push(artifact);
      return { artifactsByConversation: { ...state.artifactsByConversation, [conversationId]: next } };
    });
  },

  upsertTask: (conversationId, task) => {
    set((state) => {
      const list = state.tasksByConversation[conversationId] ?? [];
      const idx = list.findIndex((t) => t.task_id === task.task_id);
      let next: SubAgentTask[];
      if (idx >= 0) {
        next = list.slice();
        const current = next[idx];
        const incoming = { ...current, ...task };
        // Prefer the longer execution_log: DB snapshots only have completed steps,
        // while the live SSE stream may have buffered more content in memory.
        if (
          current.execution_log &&
          task.execution_log &&
          current.execution_log.length > task.execution_log.length
        ) {
          incoming.execution_log = current.execution_log;
        }
        // Replayed task_created events from older deployments may not carry the
        // turn relationship. Never erase the authoritative value loaded from DB.
        if (task.trigger_history_id === undefined) {
          incoming.trigger_history_id = current.trigger_history_id;
        }
        if (task.seq_in_conversation === undefined) {
          incoming.seq_in_conversation = current.seq_in_conversation;
        }
        next[idx] = incoming;
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
            output_slots: task.output_slots,
            artifacts: task.artifacts ?? [],
            artifact_streams: task.artifact_streams ?? [],
            execution_log: task.execution_log ?? [],
            conversation_id: conversationId,
            trigger_history_id: task.trigger_history_id,
            seq_in_conversation: task.seq_in_conversation,
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
            slot: event.slot,
            content_type: event.content_type,
            seq: event.seq ?? 1,
            value: event.value,
          };
          const existing = task.artifacts ?? [];
          if (!existing.some((a) => artifactKey(a) === artifactKey(newArtifact))) {
            task.artifacts = [...existing, newArtifact];
          }
          const streams = task.artifact_streams ?? [];
          const streamIndex = streams.reduce(
            (latestIndex, stream, index) => (
              stream.slot === newArtifact.slot && stream.content_type === "text/markdown"
                ? index
                : latestIndex
            ),
            -1,
          );
          if (streamIndex >= 0) {
            const nextStreams = streams.slice();
            nextStreams[streamIndex] = {
              ...nextStreams[streamIndex],
              artifact: newArtifact,
              // A .lmd file is the final Writer IR, not Markdown text. Keep the
              // streamed Markdown preview until the plugin session exposes the
              // IR revision, then let the slot renderer switch to its editor.
              state: isWriterIRArtifact(newArtifact) ? "ready" : nextStreams[streamIndex].state,
            };
            task.artifact_streams = nextStreams;
          }
          break;
        }
        case "artifact_stream_start": {
          if (!event.stream_id || !event.slot || !event.content_type) break;
          const current = task.artifact_streams ?? [];
          const next = current.filter((stream) => stream.stream_id !== event.stream_id);
          next.push({
            task_id: taskId,
            slot: event.slot,
            content_type: event.content_type,
            stream_id: event.stream_id,
            chunk_index: event.chunk_index ?? 1,
            content: "",
            deltas: [],
            state: "streaming",
          });
          task.artifact_streams = next;
          break;
        }
        case "artifact_stream": {
          if (!event.stream_id) break;
          const streams = task.artifact_streams ?? [];
          const streamIndex = streams.findIndex((stream) => stream.stream_id === event.stream_id);
          if (streamIndex < 0) break;
          const stream = streams[streamIndex];
          const chunkIndex = event.chunk_index ?? 0;
          // The server guarantees monotonically increasing chunk indexes. Ignore replayed
          // or out-of-order chunks so reconnects never duplicate preview text.
          if (chunkIndex <= stream.chunk_index) break;
          const delta = typeof event.delta === "string" ? event.delta : "";
          const nextStreams = streams.slice();
          nextStreams[streamIndex] = {
            ...stream,
            chunk_index: chunkIndex,
            content: stream.content + delta,
            // Preserve backend event boundaries. The renderer can expose every
            // server delta even when XHR delivers several SSE frames together.
            deltas: [...(stream.deltas ?? (stream.content ? [stream.content] : [])), delta],
            state: "streaming",
          };
          task.artifact_streams = nextStreams;
          break;
        }
        case "artifact_stream_end":
        case "artifact_stream_abort": {
          if (!event.stream_id) break;
          const streams = task.artifact_streams ?? [];
          const streamIndex = streams.findIndex((stream) => stream.stream_id === event.stream_id);
          if (streamIndex < 0) break;
          const stream = streams[streamIndex];
          const chunkIndex = event.chunk_index ?? stream.chunk_index;
          if (chunkIndex < stream.chunk_index) break;
          const nextStreams = streams.slice();
          nextStreams[streamIndex] = {
            ...stream,
            chunk_index: chunkIndex,
            state: event.type === "artifact_stream_abort" ? "aborted" : "ended",
            message: event.message || stream.message,
          };
          task.artifact_streams = nextStreams;
          break;
        }
        case "done":
          task.status = (event.status as TaskStatus) ?? "succeeded";
          task.progress_pct = 100;
          task.summary = event.summary ?? task.summary;
          break;
        case "error":
          task.status = (event.status as TaskStatus) ?? "failed";
          task.summary = event.message || localizeErrorCode(
            event.error_code ?? event.errorCode ?? event.code,
            localizeErrorCode("2000509"),
          );
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
        case "tool_calls": {
          const calls: ToolCallItem[] = (event.tool_calls ?? []).map((tc: any) => ({
            id: tc.id ?? tc.tool_call_id ?? "",
            name: tc.name ?? tc.function?.name ?? "",
            args: tc.args ?? tc.function?.arguments ?? {},
          }));
          if (calls.length > 0) {
            task.execution_log = [
              ...(task.execution_log ?? []),
              { type: "tool_calls", content: "", tool_calls: calls },
            ];
          }
          break;
        }
        case "tool_results": {
          const results: ToolResultItem[] = (event.tool_results ?? []).map((tr: any) => ({
            tool_call_id: tr.id ?? tr.tool_call_id ?? "",
            name: tr.name ?? "",
            result: tr.result ?? tr.content ?? "",
          }));
          if (results.length > 0) {
            task.execution_log = [
              ...(task.execution_log ?? []),
              { type: "tool_results", content: "", tool_results: results },
            ];
            if (
              results.some((result) =>
                JSON.stringify(result.result).includes("FFMPEG_DEPENDENCY_MISSING"),
              )
            ) {
              window.dispatchEvent(
                new CustomEvent(CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT),
              );
            }
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

  loadArtifactStreamContent: async (conversationId, taskId, artifact) => {
    if (!WRITER_MARKDOWN_STREAM_SLOT_IDS.has(artifact.slot) || artifact.content_type !== "file") return;
    if (isWriterIRArtifact(artifact)) return;
    const rawUrl = typeof artifact.value?.url === "string" ? artifact.value.url : "";
    const url = resolveCoreAssetUrl(rawUrl);
    if (!url) return;

    try {
      const response = await axiosInstance.get<string>(url, { responseType: "text" });
      const content = typeof response.data === "string" ? response.data : "";
      if (!content) throw new Error("empty artifact content");
      set((state) => {
        const tasks = state.tasksByConversation[conversationId] ?? [];
        const taskIndex = tasks.findIndex((task) => task.task_id === taskId);
        if (taskIndex < 0) return state;
        const task = tasks[taskIndex];
        const streamIndex = (task.artifact_streams ?? []).reduce(
          (latestIndex, stream, index) => (
            stream.slot === artifact.slot && stream.artifact?.value?.url === rawUrl
              ? index
              : latestIndex
          ),
          -1,
        );
        if (streamIndex < 0) return state;
        const nextStreams = task.artifact_streams.slice();
        nextStreams[streamIndex] = {
          ...nextStreams[streamIndex],
          state: "ready",
          final_content: content,
          final_content_error: undefined,
        };
        const nextTasks = tasks.slice();
        nextTasks[taskIndex] = { ...task, artifact_streams: nextStreams };
        return {
          tasksByConversation: {
            ...state.tasksByConversation,
            [conversationId]: nextTasks,
          },
        };
      });
    } catch {
      set((state) => {
        const tasks = state.tasksByConversation[conversationId] ?? [];
        const taskIndex = tasks.findIndex((task) => task.task_id === taskId);
        if (taskIndex < 0) return state;
        const task = tasks[taskIndex];
        const streamIndex = (task.artifact_streams ?? []).reduce(
          (latestIndex, stream, index) => (
            stream.slot === artifact.slot && stream.artifact?.value?.url === rawUrl
              ? index
              : latestIndex
          ),
          -1,
        );
        if (streamIndex < 0) return state;
        const nextStreams = task.artifact_streams.slice();
        nextStreams[streamIndex] = {
          ...nextStreams[streamIndex],
          final_content_error: localizeErrorCode("2000509"),
        };
        const nextTasks = tasks.slice();
        nextTasks[taskIndex] = { ...task, artifact_streams: nextStreams };
        return {
          tasksByConversation: {
            ...state.tasksByConversation,
            [conversationId]: nextTasks,
          },
        };
      });
    }
  },

  subscribeTask: (conversationId, taskId) => {
    const existing = get()._streams[taskId];
    if (existing) {
      return;
    }
    // Don't subscribe to tasks that are already in a terminal state.
    const task = get().getTasks(conversationId).find((t) => t.task_id === taskId);
    if (task && TERMINAL.includes(task.status)) {
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
          if (event.type === "artifact") {
            const artifact: TaskArtifact = {
              slot: event.slot,
              content_type: event.content_type,
              seq: event.seq ?? 1,
              value: event.value,
            };
            void get().loadArtifactStreamContent(conversationId, taskId, artifact);
          }
          if (event.type === "done" || event.type === "error") {
            get().unsubscribeTask(taskId);
            // Reload the authoritative DB snapshot so file artifacts receive
            // fresh signed URLs and hidden/replaced artifacts are filtered.
            void get().loadConversationTasks(conversationId);
            void get().loadConversationArtifacts(conversationId);
          }
        },
        error: () => {
          const stream = get()._streams[taskId];
          try { stream?.close(); } catch { /* ignore */ }
          set((state) => {
            const next = { ...state._streams };
            delete next[taskId];
            const tasks = state.tasksByConversation[conversationId] ?? [];
            return {
              _streams: next,
              // A reconnect starts with a complete DB snapshot. Clear replayed
              // collections first so historic log/artifact events are replaced
              // instead of appended a second time.
              tasksByConversation: {
                ...state.tasksByConversation,
                [conversationId]: tasks.map((task) => task.task_id === taskId
                  ? { ...task, execution_log: [], artifacts: [] }
                  : task),
              },
            };
          });
          // Re-read the authoritative snapshot before reconnecting. StreamTask
          // itself starts with a DB snapshot, so a transient disconnect cannot
          // leave the card permanently running.
          void get().loadConversationTasks(conversationId);
          if (!taskReconnectTimers.has(taskId)) {
            taskReconnectTimers.set(taskId, setTimeout(() => {
              taskReconnectTimers.delete(taskId);
              get().subscribeTask(conversationId, taskId);
            }, 1000));
          }
        },
      },
    });
    set((state) => ({ _streams: { ...state._streams, [taskId]: sse } }));
  },

  unsubscribeTask: (taskId) => {
    const retryTimer = taskReconnectTimers.get(taskId);
    if (retryTimer) clearTimeout(retryTimer);
    taskReconnectTimers.delete(taskId);
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
    // Deduplicate concurrent calls for the same conversation.
    if (get()._loadingTasks[conversationId]) return;
    set((s) => ({ _loadingTasks: { ...s._loadingTasks, [conversationId]: true } }));
    try {
      const res = await TaskServiceApi().listConversationTasks(conversationId);
      const tasks = res?.data?.data?.tasks ?? res?.data?.tasks ?? [];
      tasks.forEach((t: any) => {
        get().upsertTask(conversationId, {
          task_id: t.task_id,
          trigger_history_id: t.trigger_history_id,
          seq_in_conversation: t.seq_in_conversation,
          title: t.title,
          agent_type: t.agent_type,
          mode: t.mode,
          status: t.status,
          progress_pct: t.progress_pct ?? 0,
          current_phase: t.current_phase,
          estimated_sec: t.estimated_sec,
          summary: t.summary,
          output_slots: t.output_slots,
          artifacts: t.artifacts ?? [],
          execution_log: stepsToExecutionLog(t.steps ?? []),
        });
        if (!TERMINAL.includes(t.status)) {
          get().subscribeTask(conversationId, t.task_id);
        }
      });
    } catch {
      // ignore load failures; panel just stays empty.
    } finally {
      set((s) => ({ _loadingTasks: { ...s._loadingTasks, [conversationId]: false } }));
    }
  },

  loadConversationArtifacts: async (conversationId) => {
    if (!conversationId || get()._loadingArtifacts[conversationId]) return;
    set((s) => ({ _loadingArtifacts: { ...s._loadingArtifacts, [conversationId]: true } }));
    try {
      const res = await TaskServiceApi().listConversationArtifacts(conversationId);
      const artifacts = res?.data?.data?.artifacts ?? res?.data?.artifacts ?? [];
      set((state) => ({
        artifactsByConversation: {
          ...state.artifactsByConversation,
          [conversationId]: artifacts,
        },
      }));
    } catch {
      // Keep the last good snapshot when a refresh fails.
    } finally {
      set((s) => ({ _loadingArtifacts: { ...s._loadingArtifacts, [conversationId]: false } }));
    }
  },

  reset: (conversationId) => {
    Object.keys(get()._streams).forEach((taskId) => get().unsubscribeTask(taskId));
    get().unsubscribeConvEvents(conversationId);
    set((state) => ({
      tasksByConversation: {
        ...state.tasksByConversation,
        [conversationId]: [],
      },
      artifactsByConversation: {
        ...state.artifactsByConversation,
        [conversationId]: [],
      },
    }));
  },

  subscribeConvEvents: (conversationId) => {
    if (!conversationId) return;
    const existing = get()._convStreams[conversationId];
    if (existing) return;
    const sse = new SSE(convEventsUrl(conversationId), {
      method: Method.GET,
      headers: {
        Accept: 'text/event-stream',
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 3600000,
      callbacks: {
        message: (e: CustomEvent) => {
          const raw = (e as any).data;
          if (!raw || raw === '[DONE]') return;
          const event = UIUtils.jsonParser(raw);
          if (!event || !event.type) return;
          const { type, payload } = event;
          const replayed = event.replayed === true;
          if (type === 'task_created' && payload?.task_id) {
            // Check the existing task state BEFORE upsert — the replay payload carries
            // the creation-time status ('pending'/'running'), not the terminal status.
            // If we upsert first and then read, we'd always see a non-terminal status
            // and the alreadyDone guard would never fire.
            const existingTask = get().getTasks(conversationId).find(
              (t) => t.task_id === payload.task_id,
            );
            const alreadyDone = existingTask && TERMINAL.includes(existingTask.status);

            if (alreadyDone) {
              // Task already finished — only upsert non-status fields (title, agent_type, mode)
              // so we never overwrite a terminal status with a stale 'pending'/'running' from replay.
              get().upsertTask(conversationId, {
                task_id: payload.task_id,
                trigger_history_id: payload.trigger_history_id,
                seq_in_conversation: payload.seq_in_conversation,
                title: payload.title,
                agent_type: payload.agent_type,
                mode: payload.mode,
              });
            } else {
              get().upsertTask(conversationId, {
                task_id: payload.task_id,
                trigger_history_id: payload.trigger_history_id,
                seq_in_conversation: payload.seq_in_conversation,
                title: payload.title,
                agent_type: payload.agent_type,
                mode: payload.mode,
                status: payload.status || 'pending',
              });
              // Only subscribe to the task SSE stream when the task is not yet in a
              // terminal state.  convEvents are replayed from the beginning every time
              // the SSE connection is (re-)established, so without this guard a
              // task_created replay would re-open the task stream, causing all historic
              // text/think/tool_calls events to be appended again and the execution log
              // to appear duplicated.
              get().subscribeTask(conversationId, payload.task_id);
            }
            if (payload.agent_type === 'workflow_step' && payload.workflow_session_id) {
              import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
                const workflowState = useWorkflowStore.getState();
                // Discovery only: once a session exists its dedicated Workflow Stream is authoritative.
                if (!workflowState.sessionByConversation[conversationId]) {
                  workflowState.loadActiveSession(conversationId);
                }
              });
            }
          } else if (type === 'artifact_created' && payload?.artifact_id) {
            get().upsertConversationArtifact(conversationId, payload as ConversationArtifact);
          } else if (type === 'driver_input') {
            if (replayed) return;
            const driverMessage = payload.message || '';
            import('@/modules/chat/constants/chat').then(({ CHAT_AUTO_ADVANCE_EVENT }) => {
              window.dispatchEvent(new CustomEvent(CHAT_AUTO_ADVANCE_EVENT, {
                detail: {
                  conversationId,
                  driverMessage,
                  phase: 'append',
                },
              }));
            });
            import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
              useWorkflowStore.getState().setAutoRunning(conversationId, true);
            });
          } else if (
			type === 'workflow_runtime_updated' ||
            type === 'step_waiting' ||
            type === 'workflow_completed' ||
            type === 'workflow_error'
          ) {
            get().loadConversationTasks(conversationId);
            window.dispatchEvent(
              new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
            );
            const refreshActiveWorkflowSession = () => {
              import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
                useWorkflowStore.getState().loadActiveSession(conversationId);
                useWorkflowStore.getState().setAutoRunning(conversationId, false);
              });
            };
            refreshActiveWorkflowSession();
            // Artifact files and their slot projection can commit just after the
            // completion event. Reconcile once more so the completed Writer panel
            // gets its generated image without requiring a route change.
            if (type === 'workflow_completed') {
              window.setTimeout(refreshActiveWorkflowSession, 800);
            }
          } else if (type === 'step_partial_done') {
            window.dispatchEvent(
              new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
            );
          } else if (type === 'intent_updated') {
            // Workflow state changes arrive through the dedicated Workflow Stream.
          } else if (type === 'workflow_artifact_updated') {
            window.dispatchEvent(
              new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
            );
            import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
              void useWorkflowStore.getState().loadActiveSession(conversationId);
            });
          } else if (type === 'ask_pending') {
            if (replayed) return;
            // ask_pending is persisted in chat history. Resuming the chat turn
            // reuses the normal message reducer and renders the AskCard.
            import('@/modules/chat/constants/chat').then(({ CHAT_AUTO_ADVANCE_EVENT }) => {
              window.dispatchEvent(new CustomEvent(CHAT_AUTO_ADVANCE_EVENT, {
                detail: { conversationId, driverMessage: '', phase: 'resume' },
              }));
            });
          } else if (type === 'max_retries_exceeded' || type === 'driver_fallback') {
            import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
              const workflowState = useWorkflowStore.getState();
              workflowState.setAutoRunning(conversationId, false);
              void workflowState.loadActiveSession(conversationId);
            });
          } else if (type === 'auto_chat_started') {
            if (replayed) return;
            import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
              useWorkflowStore.getState().setAutoRunning(conversationId, true);
            });
            import('@/modules/chat/constants/chat').then(({ CHAT_AUTO_ADVANCE_EVENT }) => {
              window.dispatchEvent(new CustomEvent(CHAT_AUTO_ADVANCE_EVENT, {
                detail: {
                  conversationId,
                  driverMessage: payload.driver_message || payload.message || '',
                  phase: 'resume',
                },
              }));
            });
          }
        },
        error: () => {
          const stream = get()._convStreams[conversationId];
          try { stream?.close(); } catch { /* ignore */ }
          set((state) => {
            const next = { ...state._convStreams };
            delete next[conversationId];
            return { _convStreams: next };
          });
          void get().loadConversationTasks(conversationId);
          import('@/modules/chat/store/workflowPanel').then(({ useWorkflowStore }) => {
            void useWorkflowStore.getState().loadActiveSession(conversationId);
          });
          if (!convReconnectTimers.has(conversationId)) {
            convReconnectTimers.set(conversationId, setTimeout(() => {
              convReconnectTimers.delete(conversationId);
              get().subscribeConvEvents(conversationId);
            }, 1000));
          }
        },
      },
    });
    set((state) => ({ _convStreams: { ...state._convStreams, [conversationId]: sse } }));
  },

  unsubscribeConvEvents: (conversationId) => {
    const retryTimer = convReconnectTimers.get(conversationId);
    if (retryTimer) clearTimeout(retryTimer);
    convReconnectTimers.delete(conversationId);
    const sse = get()._convStreams[conversationId];
    if (sse) {
      try { sse.close(); } catch { /* ignore */ }
    }
    set((state) => {
      const next = { ...state._convStreams };
      delete next[conversationId];
      return { _convStreams: next };
    });
  },
}));
