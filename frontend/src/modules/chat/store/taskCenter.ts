import { create } from "zustand";
import { AgentAppsAuth } from "@/components/auth";
import { axiosInstance, localizeErrorCode } from "@/components/request";
import { Method, SSE } from "@/modules/chat/utils/sse";
import { TaskServiceApi, convEventsUrl, taskStreamUrl } from "@/modules/chat/utils/request";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";
import UIUtils from "@/modules/chat/utils/ui";
import { WORKFLOW_GRAPH_REFRESH_EVENT } from "@/components/StateGraphModal";
import {
  CHAT_AUTO_ADVANCE_EVENT,
  CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT,
} from "@/modules/chat/constants/chat";
import { useWorkflowStore } from "@/modules/chat/store/workflowPanel";
import type { ChatSource } from "@/modules/chat/utils/sourceAdapter";

let convReconnectTimer: ReturnType<typeof setTimeout> | null = null;
let workflowRefreshTimer: ReturnType<typeof setTimeout> | null = null;
const taskReconnectTimers = new Map<string, ReturnType<typeof setTimeout>>();
const liveTaskIdsCreatedDuringLoad = new Map<string, Set<string>>();

function scheduleWorkflowSessionRefresh(conversationId: string, delayMs = 100): void {
  if (workflowRefreshTimer) clearTimeout(workflowRefreshTimer);
  workflowRefreshTimer = setTimeout(() => {
    workflowRefreshTimer = null;
    if (useTaskCenterStore.getState().activeConversationId !== conversationId) return;
    void useWorkflowStore.getState().loadActiveSession(conversationId, {
      silentError: true,
    });
  }, delayMs);
}

function cancelWorkflowSessionRefresh(): void {
  if (workflowRefreshTimer) clearTimeout(workflowRefreshTimer);
  workflowRefreshTimer = null;
}

export type TaskStatus =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "interrupted"
  | "canceled";

const TERMINAL_TASK_STATUSES = new Set<TaskStatus>([
  "succeeded",
  "failed",
  "interrupted",
  "canceled",
]);

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

export interface WritingSubtask {
  subtask_id: string;
  node_id: string;
  node_title?: string;
  question: string;
  subtask_type: "retrieve" | "extract" | "reason";
  status: "pending" | "running" | "completed" | "retrying" | "failed";
  result_summary?: string;
  retry_count: number;
  result_references?: Array<Record<string, unknown>>;
  tools_used?: string[];
}

export interface SubAgentTask {
  task_id: string;
  conversation_id?: string;
  trigger_history_id?: string;
  seq_in_conversation?: number;
  created_at?: string;
  updated_at?: string;
  title: string;
  agent_type: string;
  mode: string;
  status: TaskStatus;
  progress_pct: number;
  current_phase?: string;
  estimated_sec?: number;
  summary?: string;
  input_slots?: string[];
  output_slots?: string[];
  artifacts: TaskArtifact[];
  sources: ChatSource[];
  artifact_streams: TaskArtifactStream[];
  execution_log: TaskLogEntry[];
  writing_subtasks?: WritingSubtask[];
}

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
  // A refresh requested while the current task snapshot is still loading.
  _queuedTaskLoads: Record<string, boolean>;
  _taskLoadErrors: Record<string, boolean>;
  _loadingArtifacts: Record<string, boolean>;
  // Conversation lifecycle stream plus granular execution streams keyed by task ID.
  _convStream: SSE | null;
  _taskStreams: Record<string, SSE>;

  getTasks: (conversationId: string) => SubAgentTask[];
  upsertTask: (conversationId: string, task: Partial<SubAgentTask> & { task_id: string }) => void;
  applyTaskEvent: (conversationId: string, taskId: string, event: any) => void;
  subscribeTask: (conversationId: string, taskId: string) => void;
  unsubscribeTask: (taskId: string) => void;
  loadArtifactStreamContent: (conversationId: string, taskId: string, artifact: TaskArtifact) => Promise<void>;
  loadConversationTasks: (conversationId: string) => Promise<void>;
  loadConversationArtifacts: (conversationId: string) => Promise<void>;
  refreshConversationExecution: (conversationId: string) => Promise<void>;
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
  _queuedTaskLoads: {},
  _taskLoadErrors: {},
  _loadingArtifacts: {},
  _convStream: null,
  _taskStreams: {},

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
        if (task.created_at === undefined) {
          incoming.created_at = current.created_at;
        }
        next[idx] = incoming;
      } else {
        const createdAt = task.created_at ?? new Date().toISOString();
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
            sources: task.sources ?? [],
            artifact_streams: task.artifact_streams ?? [],
            execution_log: task.execution_log ?? [],
            conversation_id: conversationId,
            trigger_history_id: task.trigger_history_id,
            seq_in_conversation: task.seq_in_conversation,
            created_at: createdAt,
            updated_at: task.updated_at ?? createdAt,
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
      task.updated_at = new Date().toISOString();
      switch (event.type) {
        case "task_start":
          task.status = "running";
          break;
        case "progress":
          task.status = "running";
          task.progress_pct = event.progress ?? task.progress_pct;
          task.current_phase = event.current_phase ?? task.current_phase;
          task.estimated_sec = event.estimated_sec ?? task.estimated_sec;
          if (Array.isArray(event.writing_subtasks)) {
            task.writing_subtasks = event.writing_subtasks;
          }
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
        case "sources":
          task.sources = Array.isArray(event.sources) ? event.sources : [];
          break;
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

  subscribeTask: (conversationId, taskId) => {
    if (!conversationId || !taskId || get()._taskStreams[taskId]) return;
    const task = get().getTasks(conversationId).find((item) => item.task_id === taskId);
    if (task && TERMINAL_TASK_STATUSES.has(task.status)) return;

    const sse = new SSE(taskStreamUrl(taskId), {
      method: Method.GET,
      headers: {
        Accept: "text/event-stream",
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 3600000,
      callbacks: {
        message: (e: CustomEvent) => {
          if (get().activeConversationId !== conversationId) return;
          const raw = (e as any).data;
          if (!raw || raw === "[DONE]") return;
          const event = UIUtils.jsonParser(raw);
          if (!event?.type) return;

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
            void get().loadConversationTasks(conversationId);
            void get().loadConversationArtifacts(conversationId);
          }
        },
        error: () => {
          if (get().activeConversationId !== conversationId) return;
          const stream = get()._taskStreams[taskId];
          try { stream?.close(); } catch { /* ignore */ }
          set((state) => {
            const nextStreams = { ...state._taskStreams };
            delete nextStreams[taskId];
            const tasks = state.tasksByConversation[conversationId] ?? [];
            return {
              _taskStreams: nextStreams,
              tasksByConversation: {
                ...state.tasksByConversation,
                [conversationId]: tasks.map((item) => item.task_id === taskId
                  ? { ...item, execution_log: [], artifacts: [] }
                  : item),
              },
            };
          });
          void get().loadConversationTasks(conversationId);
          if (!taskReconnectTimers.has(taskId)) {
            taskReconnectTimers.set(taskId, setTimeout(() => {
              taskReconnectTimers.delete(taskId);
              if (get().activeConversationId === conversationId) {
                get().subscribeTask(conversationId, taskId);
              }
            }, 1000));
          }
        },
      },
    });
    set((state) => ({
      _taskStreams: { ...state._taskStreams, [taskId]: sse },
    }));
  },

  unsubscribeTask: (taskId) => {
    const retryTimer = taskReconnectTimers.get(taskId);
    if (retryTimer) clearTimeout(retryTimer);
    taskReconnectTimers.delete(taskId);
    try { get()._taskStreams[taskId]?.close(); } catch { /* ignore */ }
    set((state) => {
      const nextStreams = { ...state._taskStreams };
      delete nextStreams[taskId];
      return { _taskStreams: nextStreams };
    });
  },

  loadArtifactStreamContent: async (conversationId, taskId, artifact) => {
    if (artifact.content_type !== "file") return;
    if (isWriterIRArtifact(artifact)) return;
    const rawUrl = typeof artifact.value?.url === "string" ? artifact.value.url : "";
    const url = resolveCoreAssetUrl(rawUrl);
    if (!url) return;
    const task = (get().tasksByConversation[conversationId] ?? [])
      .find((candidate) => candidate.task_id === taskId);
    const hasMatchingTextStream = (task?.artifact_streams ?? []).some((stream) => (
      stream.slot === artifact.slot
      && stream.artifact?.value?.url === rawUrl
      && stream.content_type === "text/markdown"
    ));
    if (!hasMatchingTextStream) return;

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
  loadConversationTasks: async (conversationId) => {
    if (!conversationId) {
      return;
    }
    // Do not drop a refresh requested while an older snapshot is in flight.
    // The active loader will run one more request before it releases the lock.
    if (get()._loadingTasks[conversationId]) {
      set((s) => ({
        _queuedTaskLoads: { ...s._queuedTaskLoads, [conversationId]: true },
      }));
      return;
    }
    set((s) => ({
      _loadingTasks: { ...s._loadingTasks, [conversationId]: true },
      _queuedTaskLoads: { ...s._queuedTaskLoads, [conversationId]: false },
      _taskLoadErrors: { ...s._taskLoadErrors, [conversationId]: false },
    }));
    try {
      do {
        const liveCreatedTaskIds = new Set<string>();
        liveTaskIdsCreatedDuringLoad.set(conversationId, liveCreatedTaskIds);
        set((s) => ({
          _queuedTaskLoads: { ...s._queuedTaskLoads, [conversationId]: false },
          _taskLoadErrors: { ...s._taskLoadErrors, [conversationId]: false },
        }));
        try {
          const res = await TaskServiceApi().listConversationTasks(conversationId);
          const tasks = res?.data?.data?.tasks ?? res?.data?.tasks ?? [];
          const normalized: SubAgentTask[] = tasks.map((t: any): SubAgentTask => ({
            task_id: t.task_id,
            conversation_id: conversationId,
            trigger_history_id: t.trigger_history_id,
            seq_in_conversation: t.seq_in_conversation,
            created_at: t.created_at,
            updated_at: t.updated_at,
            title: t.title ?? "",
            agent_type: t.agent_type ?? "",
            mode: t.mode ?? "auto",
            status: t.status ?? "pending",
            progress_pct: t.progress_pct ?? 0,
            current_phase: t.current_phase,
            estimated_sec: t.estimated_sec,
            summary: t.summary,
            input_slots: t.input_slots,
            output_slots: t.output_slots,
            artifacts: t.artifacts ?? [],
            sources: t.sources ?? [],
            artifact_streams: t.artifact_streams ?? [],
            execution_log: stepsToExecutionLog(t.steps ?? []),
            writing_subtasks: t.writing_subtasks ?? t.progress?.writing_subtasks,
          }));
          set((state) => {
            const snapshotIds = new Set(normalized.map((task) => task.task_id));
            // A task_created event can arrive after this REST request was issued
            // but before its older snapshot resolves. Keep those live additions;
            // the queued follow-up request below will reconcile their full state.
            const liveAdditions = (state.tasksByConversation[conversationId] ?? [])
              .filter((task) => (
                liveCreatedTaskIds.has(task.task_id) && !snapshotIds.has(task.task_id)
              ));
            return {
              tasksByConversation: {
                ...state.tasksByConversation,
                [conversationId]: [...normalized, ...liveAdditions],
              },
            };
          });
          normalized.forEach((task) => {
            if (!TERMINAL_TASK_STATUSES.has(task.status)) {
              get().subscribeTask(conversationId, task.task_id);
            }
          });
        } catch {
          set((s) => ({
            _taskLoadErrors: { ...s._taskLoadErrors, [conversationId]: true },
          }));
        }
      } while (get()._queuedTaskLoads[conversationId]);
    } finally {
      liveTaskIdsCreatedDuringLoad.delete(conversationId);
      set((s) => ({
        _loadingTasks: { ...s._loadingTasks, [conversationId]: false },
        _queuedTaskLoads: { ...s._queuedTaskLoads, [conversationId]: false },
      }));
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

  refreshConversationExecution: async (conversationId) => {
    if (!conversationId) return;
    await Promise.all([
      get().loadConversationTasks(conversationId),
      get().loadConversationArtifacts(conversationId),
      useWorkflowStore.getState().loadActiveSession(conversationId, {
        silentError: true,
      }),
    ]);
  },

  reset: (conversationId) => {
    const taskIds = get().getTasks(conversationId).map((task) => task.task_id);
    taskIds.forEach((taskId) => get().unsubscribeTask(taskId));
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
      _taskLoadErrors: {
        ...state._taskLoadErrors,
        [conversationId]: false,
      },
      _queuedTaskLoads: {
        ...state._queuedTaskLoads,
        [conversationId]: false,
      },
    }));
  },

  subscribeConvEvents: (conversationId) => {
    if (!conversationId) return;
    if (get().activeConversationId === conversationId && get()._convStream) return;
    if (convReconnectTimer) {
      clearTimeout(convReconnectTimer);
      convReconnectTimer = null;
    }
    try { get()._convStream?.close(); } catch { /* ignore */ }
    set({ activeConversationId: conversationId, _convStream: null });
    const sse = new SSE(convEventsUrl(conversationId), {
      method: Method.GET,
      headers: {
        Accept: 'text/event-stream',
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 3600000,
      callbacks: {
        message: (e: CustomEvent) => {
          if (get().activeConversationId !== conversationId) return;
          const raw = (e as any).data;
          if (!raw || raw === '[DONE]') return;
          const event = UIUtils.jsonParser(raw);
          if (!event || !event.type) return;
          const { type, payload } = event;
          const replayed = event.replayed === true;
          if (type === 'task_created' && payload?.task_id) {
            if (replayed) {
              if (payload.agent_type === 'workflow_step') {
                scheduleWorkflowSessionRefresh(conversationId);
              }
              void get().loadConversationTasks(conversationId);
              return;
            }
            if (payload.agent_type === 'workflow_step') {
              scheduleWorkflowSessionRefresh(conversationId);
            }
            // Keep workflow steps in the shared task store. Ordinary mode
            // aggregates them, while developer mode renders every attempt.
            get().upsertTask(conversationId, {
              task_id: payload.task_id,
              trigger_history_id: payload.trigger_history_id,
              seq_in_conversation: payload.seq_in_conversation,
              title: payload.title,
              agent_type: payload.agent_type,
              mode: payload.mode,
              status: payload.status || 'pending',
              created_at: payload.created_at,
              updated_at: payload.updated_at,
            });
            get().subscribeTask(conversationId, payload.task_id);
            // A live event that races an older REST snapshot queues an
            // authoritative reload after preserving this newly created task.
            if (get()._loadingTasks[conversationId]) {
              liveTaskIdsCreatedDuringLoad.get(conversationId)?.add(payload.task_id);
              void get().loadConversationTasks(conversationId);
            }
          } else if (type === 'task_updated' && payload?.task_id && payload?.event) {
            const taskEvent = payload.event;
            if (replayed) {
              // Applying replayed log/progress deltas could duplicate append-only
              // content. Rehydrate them from the persisted task snapshot instead.
              void get().loadConversationTasks(conversationId);
              if (taskEvent.type === 'artifact') {
                void get().loadConversationArtifacts(conversationId);
              }
              return;
            }
            get().applyTaskEvent(conversationId, payload.task_id, taskEvent);
            if (taskEvent.type === 'artifact') {
              void get().loadArtifactStreamContent(conversationId, payload.task_id, {
                slot: taskEvent.slot,
                content_type: taskEvent.content_type,
                seq: taskEvent.seq ?? 1,
                value: taskEvent.value,
              });
            }
            if (taskEvent.type === 'artifact') {
              void get().loadConversationArtifacts(conversationId);
            }
            if (taskEvent.type === 'done' || taskEvent.type === 'error') {
              void get().loadConversationTasks(conversationId);
              void get().loadConversationArtifacts(conversationId);
            }
          } else if (type === 'artifact_created' && payload?.artifact_id) {
            if (replayed) {
              void get().loadConversationArtifacts(conversationId);
              return;
            }
            get().upsertConversationArtifact(conversationId, payload as ConversationArtifact);
          } else if (type === 'driver_input') {
            if (replayed) return;
            const driverMessage = payload.message || '';
            window.dispatchEvent(new CustomEvent(CHAT_AUTO_ADVANCE_EVENT, {
              detail: {
                conversationId,
                driverMessage,
                phase: 'append',
              },
            }));
            useWorkflowStore.getState().setAutoRunning(conversationId, true);
          } else if (
            type === 'workflow_runtime_updated' ||
            type === 'step_waiting' ||
            type === 'workflow_completed' ||
            type === 'workflow_error'
          ) {
            if (!replayed) {
              window.dispatchEvent(
                new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
              );
              useWorkflowStore.getState().setAutoRunning(conversationId, false);
            }
            // Completion can be emitted just before its artifact transaction is
            // visible. Delay that one refresh instead of issuing an immediate
            // request followed by a second reconciliation request.
            scheduleWorkflowSessionRefresh(
              conversationId,
              type === 'workflow_completed' ? 800 : 100,
            );
          } else if (type === 'step_partial_done') {
            if (replayed) {
              scheduleWorkflowSessionRefresh(conversationId);
            } else {
              window.dispatchEvent(
                new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
              );
            }
          } else if (type === 'intent_updated') {
            scheduleWorkflowSessionRefresh(conversationId);
          } else if (type === 'workflow_artifact_updated') {
            if (!replayed) {
              window.dispatchEvent(
                new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
              );
            }
            scheduleWorkflowSessionRefresh(conversationId);
          } else if (type === 'workflow_session_created') {
            if (!replayed) {
              window.dispatchEvent(
                new CustomEvent(WORKFLOW_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
              );
            }
            scheduleWorkflowSessionRefresh(conversationId);
          } else if (type === 'ask_pending') {
            if (replayed) return;
            // ask_pending is persisted in chat history. Resuming the chat turn
            // reuses the normal message reducer and renders the AskCard.
            window.dispatchEvent(new CustomEvent(CHAT_AUTO_ADVANCE_EVENT, {
              detail: { conversationId, driverMessage: '', phase: 'resume' },
            }));
          } else if (type === 'max_retries_exceeded' || type === 'driver_fallback') {
            if (replayed) {
              scheduleWorkflowSessionRefresh(conversationId);
              return;
            }
            const workflowState = useWorkflowStore.getState();
            workflowState.setAutoRunning(conversationId, false);
            scheduleWorkflowSessionRefresh(conversationId);
          } else if (type === 'auto_chat_started') {
            if (replayed) return;
            useWorkflowStore.getState().setAutoRunning(conversationId, true);
            window.dispatchEvent(new CustomEvent(CHAT_AUTO_ADVANCE_EVENT, {
              detail: {
                conversationId,
                driverMessage: payload.driver_message || payload.message || '',
                phase: 'resume',
              },
            }));
          }
        },
        error: () => {
          if (get().activeConversationId !== conversationId) return;
          try { get()._convStream?.close(); } catch { /* ignore */ }
          set({ _convStream: null });
          cancelWorkflowSessionRefresh();
          void get().refreshConversationExecution(conversationId);
          if (!convReconnectTimer) {
            convReconnectTimer = setTimeout(() => {
              convReconnectTimer = null;
              if (get().activeConversationId === conversationId) {
                get().subscribeConvEvents(conversationId);
              }
            }, 1000);
          }
        },
      },
    });
    set({ _convStream: sse });
  },

  unsubscribeConvEvents: (conversationId) => {
    if (get().activeConversationId !== conversationId) return;
    if (convReconnectTimer) clearTimeout(convReconnectTimer);
    convReconnectTimer = null;
    cancelWorkflowSessionRefresh();
    get().getTasks(conversationId).forEach((task) => get().unsubscribeTask(task.task_id));
    try { get()._convStream?.close(); } catch { /* ignore */ }
    set({ activeConversationId: '', _convStream: null });
  },
}));
