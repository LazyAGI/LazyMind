import { SSE } from "./sse";
import i18n from "@/i18n";
import type { RunPerformanceMetrics } from "./performanceStats";

export interface StreamCallbacks {
  message?: (e: CustomEvent) => void;
  error?: (e: CustomEvent) => void;
  timeout?: (e: CustomEvent) => void;
}

export interface StreamState {
  conversationId: string;
  delta: string;
  reasoning_content: string;
  sources?: any[];
  legacyFinishReason?: string;
  runTerminals: Record<string, RunTerminal>;
  historyRunIds: Record<string, string>;
  activeHistoryIds: string[];
  connectionState: "connected" | "disconnected" | "resuming";
  messageId?: string;
  history_id?: string;
  messageList?: any[];
}

export interface StreamRegistrationOptions {
  /**
   * Keeps this stream outside the legacy single-active-conversation eviction
   * policy. Side chats opt in so opening either composer cannot interrupt the
   * other one; existing callers retain the original exclusive behaviour.
   */
  allowConcurrent?: boolean;
}

export interface RunTerminal {
  status: "completed" | "interrupted" | "failed" | "cancelled";
  reason:
    | "normal"
    | "awaiting_user_input"
    | "model_incomplete"
    | "model_failure"
    | "runtime_failure"
    | "user_cancelled";
  code?: string;
  partial_output: boolean;
  model_call_id?: string;
  model_invoked?: boolean;
  diagnostic_id?: string;
  metrics?: RunPerformanceMetrics;
}

export class StreamManager {
  private streams: Map<string, SSE> = new Map();
  private callbacks: Map<string, StreamCallbacks> = new Map();
  private streamStates: Map<string, StreamState> = new Map();
  private registrations: Map<string, StreamRegistrationOptions> = new Map();
  private activeConversationId: string | null = null;

  registerStream(
    conversationId: string,
    sse: SSE,
    callbacks: StreamCallbacks,
    initialEvent?: CustomEvent,
    options: StreamRegistrationOptions = {},
  ): void {
    this.streams.forEach((existing, existingConversationId) => {
      const existingAllowsConcurrent =
        this.registrations.get(existingConversationId)?.allowConcurrent === true;
      if (
        existingConversationId !== conversationId &&
        options.allowConcurrent !== true &&
        !existingAllowsConcurrent
      ) {
        try {
          existing.close();
        } catch (error) {
          console.warn("Failed to close stale stream:", error);
        }
        this.streams.delete(existingConversationId);
        this.callbacks.delete(existingConversationId);
        this.streamStates.delete(existingConversationId);
        this.registrations.delete(existingConversationId);
      }
    });

    const existingStream = this.streams.get(conversationId);
    if (existingStream) {
      const oldCallbacks = this.callbacks.get(conversationId);
      if (oldCallbacks) {
        if (oldCallbacks.message) {
          existingStream.removeEventListener("message", oldCallbacks.message);
        }
        if (oldCallbacks.error) {
          existingStream.removeEventListener("error", oldCallbacks.error);
        }
        if (oldCallbacks.timeout) {
          existingStream.removeEventListener("timeout", oldCallbacks.timeout);
        }
      }
      existingStream.close();
    }

    this.streams.set(conversationId, sse);
    this.callbacks.set(conversationId, callbacks);
    this.registrations.set(conversationId, options);

    if (!this.streamStates.has(conversationId)) {
      this.streamStates.set(conversationId, {
        conversationId,
        delta: "",
        reasoning_content: "",
        sources: undefined,
        legacyFinishReason: undefined,
        runTerminals: {},
        historyRunIds: {},
        activeHistoryIds: [],
        connectionState: "connected",
        messageId: undefined,
        history_id: undefined,
      });
    } else {
      const existingState = this.streamStates.get(conversationId);
      if (existingState) {
        existingState.delta = "";
        existingState.reasoning_content = "";
        existingState.legacyFinishReason = undefined;
        existingState.runTerminals = {};
        existingState.historyRunIds = {};
        existingState.activeHistoryIds = [];
        existingState.connectionState = "connected";
      }
    }

    const wrappedCallbacks: StreamCallbacks = {
      message: (e: CustomEvent) => {
        try {
          const data = (e as any).data;
          if (typeof data === "string") {
            if (data.trim() === "[DONE]") {
              return;
            }
            const parsed = JSON.parse(data);
            const result = parsed?.result;
            const isTempId = conversationId.startsWith("temp_");
            if (
              result?.conversation_id &&
              result.conversation_id !== conversationId &&
              !isTempId
            ) {
              return;
            }
          }
        } catch {}

        if (!this.updateStreamState(conversationId, e)) {
          return;
        }
        if (callbacks.message) {
          callbacks.message(e);
        }
      },
      error: (e: CustomEvent) => {
        const state = this.streamStates.get(conversationId);
        if (state && !this.isStreamFinished(conversationId)) {
          state.connectionState = "disconnected";
        }
        if (callbacks.error) {
          callbacks.error(e);
        }
        this.cleanupStream(conversationId);
      },
      timeout: (e: CustomEvent) => {
        const state = this.streamStates.get(conversationId);
        if (state && !this.isStreamFinished(conversationId)) {
          state.connectionState = "resuming";
        }
        if (callbacks.timeout) {
          callbacks.timeout(e);
        }
        this.cleanupStream(conversationId);
      },
    };

    this.callbacks.set(conversationId, wrappedCallbacks);

    if (wrappedCallbacks.message) {
      sse.addEventListener("message", wrappedCallbacks.message);
    }
    if (wrappedCallbacks.error) {
      sse.addEventListener("error", wrappedCallbacks.error);
    }
    if (wrappedCallbacks.timeout) {
      sse.addEventListener("timeout", wrappedCallbacks.timeout);
    }
    if (initialEvent) {
      this.updateStreamState(conversationId, initialEvent);
    }
  }

  private updateStreamState(conversationId: string, e: CustomEvent): boolean {
    if (!this.streamStates.has(conversationId)) {
      this.streamStates.set(conversationId, {
        conversationId,
        delta: "",
        reasoning_content: "",
        sources: undefined,
        legacyFinishReason: undefined,
        runTerminals: {},
        historyRunIds: {},
        activeHistoryIds: [],
        connectionState: "connected",
        messageId: undefined,
        history_id: undefined,
      });
    }

    const state = this.streamStates.get(conversationId);
    if (!state) {
      return false;
    }

    try {
      const data = (e as any).data;
      if (typeof data === "string") {
        if (data.trim() === "[DONE]") {
          return true;
        }
        const parsed = JSON.parse(data);
        const result = parsed?.result;
        if (result) {
          if (result.sources && result.sources.length > 0) {
            state.sources = result.sources;
          }
          if (result.finish_reason) {
            state.legacyFinishReason = result.finish_reason;
          }
          const runtimeEvent = result.runtime_event;
          const hasBusinessPayload = Boolean(
            result.delta ||
              result.reasoning_content ||
              result.task_created ||
              result.artifact_created ||
              result.ask_pending ||
              result.tool_limit_pending,
          );
          if (this.isStreamFinished(conversationId) && hasBusinessPayload) {
            console.error("Ignored payload emitted after run_finished");
            return false;
          }
          if (
            result.history_id &&
            !state.activeHistoryIds.includes(result.history_id)
          ) {
            state.activeHistoryIds.push(result.history_id);
          }
          if (result.history_id && runtimeEvent?.run_id) {
            state.historyRunIds[result.history_id] = runtimeEvent.run_id;
          }
          if (runtimeEvent?.type === "run_finished" && runtimeEvent.run_id) {
            state.runTerminals[runtimeEvent.run_id] =
              runtimeEvent.data as RunTerminal;
          }
          if (result.messageId) {
            state.messageId = result.messageId;
          }
          if (result.history_id) {
            state.history_id = result.history_id;
          }
          if (result.conversation_id) {
            state.conversationId = result.conversation_id;
          }
          state.connectionState = "connected";
        }
      }
    } catch (error) {
      console.error("Failed to parse stream data:", error);
      return false;
    }
    return true;
  }

  setActiveConversation(conversationId: string | null): void {
    this.activeConversationId = conversationId;
  }

  getStreamState(conversationId: string): StreamState | null {
    return this.streamStates.get(conversationId) || null;
  }

  saveMessageList(conversationId: string, messageList: any[]): void {
    const state = this.streamStates.get(conversationId);
    if (state) {
      state.messageList = messageList;
    } else {
      this.streamStates.set(conversationId, {
        conversationId,
        delta: "",
        reasoning_content: "",
        runTerminals: {},
        historyRunIds: {},
        activeHistoryIds: [],
        connectionState: "connected",
        messageList,
      });
    }
  }

  hasActiveStream(conversationId: string): boolean {
    const stream = this.streams.get(conversationId);
    if (!stream) {
      return false;
    }
    return stream.readyState === 0 || stream.readyState === 1;
  }

  getStream(conversationId: string): SSE | null {
    return this.streams.get(conversationId) || null;
  }

  getCallbacks(conversationId: string): StreamCallbacks | null {
    return this.callbacks.get(conversationId) || null;
  }

  closeStream(conversationId: string): void {
    const stream = this.streams.get(conversationId);
    if (stream) {
      stream.close();
    }
    this.cleanupStream(conversationId);
  }

  private cleanupStream(conversationId: string): void {
    const stream = this.streams.get(conversationId);
    if (stream) {
      const callbacks = this.callbacks.get(conversationId);
      if (callbacks) {
        try {
          if (callbacks.message) {
            stream.removeEventListener("message", callbacks.message);
          }
          if (callbacks.error) {
            stream.removeEventListener("error", callbacks.error);
          }
          if (callbacks.timeout) {
            stream.removeEventListener("timeout", callbacks.timeout);
          }
        } catch (error) {
          console.warn(
            "Failed to remove event listeners during cleanup:",
            error,
          );
        }
      }
    }

    const state = this.streamStates.get(conversationId);
    if (state && this.isStreamFinished(conversationId)) {
      this.streams.delete(conversationId);
      this.callbacks.delete(conversationId);
      this.registrations.delete(conversationId);
    }
  }

  isStreamFinished(conversationId: string): boolean {
    const state = this.streamStates.get(conversationId);
    if (!state) {
      return false;
    }
    if (
      state.legacyFinishReason &&
      state.legacyFinishReason !== "FINISH_REASON_UNSPECIFIED"
    ) {
      return true;
    }
    if (state.activeHistoryIds.length === 0) {
      return Object.keys(state.runTerminals).length > 0;
    }
    return state.activeHistoryIds.every((historyId) =>
      Boolean(
        state.historyRunIds[historyId] &&
          state.runTerminals[state.historyRunIds[historyId]],
      ),
    );
  }

  getAggregatedRunTerminal(conversationId: string): RunTerminal | undefined {
    const state = this.streamStates.get(conversationId);
    if (!state || !this.isStreamFinished(conversationId)) {
      return undefined;
    }
    const terminals =
      state.activeHistoryIds.length > 0
        ? state.activeHistoryIds
            .map((historyId) => state.historyRunIds[historyId])
            .map((runId) => state.runTerminals[runId])
        : Object.values(state.runTerminals);
    const rank: Record<RunTerminal["status"], number> = {
      failed: 0,
      interrupted: 1,
      cancelled: 2,
      completed: 3,
    };
    return terminals.reduce<RunTerminal | undefined>((worst, terminal) => {
      if (!terminal) {
        return worst;
      }
      return !worst || rank[terminal.status] < rank[worst.status]
        ? terminal
        : worst;
    }, undefined);
  }

  closeAndCleanup(conversationId: string): void {
    const stream = this.streams.get(conversationId);
    if (stream) {
      try {
        const callbacks = this.callbacks.get(conversationId);
        if (callbacks) {
          if (callbacks.message) {
            stream.removeEventListener("message", callbacks.message);
          }
          if (callbacks.error) {
            stream.removeEventListener("error", callbacks.error);
          }
          if (callbacks.timeout) {
            stream.removeEventListener("timeout", callbacks.timeout);
          }
        }
        stream.close();
      } catch (error) {
        console.error(i18n.t("chat.streamCloseFailedLog"), error);
      }
    }

    this.streams.delete(conversationId);
    this.callbacks.delete(conversationId);
    this.streamStates.delete(conversationId);
    this.registrations.delete(conversationId);

    if (this.activeConversationId === conversationId) {
      this.activeConversationId = null;
    }
  }

  clearStreamState(conversationId: string): void {
    this.streamStates.delete(conversationId);
  }

  removeStreamEntry(conversationId: string): void {
    this.streams.delete(conversationId);
    this.callbacks.delete(conversationId);
    this.registrations.delete(conversationId);
  }

  restoreStreamCallbacks(
    conversationId: string,
    callbacks: StreamCallbacks,
  ): void {
    const stream = this.streams.get(conversationId);
    if (!stream) {
      return;
    }

    if (stream.readyState === 2) {
      this.cleanupStream(conversationId);
      return;
    }

    const oldCallbacks = this.callbacks.get(conversationId);
    if (oldCallbacks) {
      try {
        if (oldCallbacks.message) {
          stream.removeEventListener("message", oldCallbacks.message);
        }
        if (oldCallbacks.error) {
          stream.removeEventListener("error", oldCallbacks.error);
        }
        if (oldCallbacks.timeout) {
          stream.removeEventListener("timeout", oldCallbacks.timeout);
        }
      } catch (error) {
        console.warn("Failed to remove event listeners:", error);
      }
    }

    const wrappedCallbacks: StreamCallbacks = {
      message: (e: CustomEvent) => {
        try {
          const data = (e as any).data;
          if (typeof data === "string") {
            if (data.trim() === "[DONE]") {
              return;
            }
            const parsed = JSON.parse(data);
            const result = parsed?.result;
            const isTempId = conversationId.startsWith("temp_");
            if (
              result?.conversation_id &&
              result.conversation_id !== conversationId &&
              !isTempId
            ) {
              return;
            }
          }
        } catch {}

        if (!this.updateStreamState(conversationId, e)) {
          return;
        }
        if (callbacks.message) {
          callbacks.message(e);
        }
      },
      error: (e: CustomEvent) => {
        const state = this.streamStates.get(conversationId);
        if (state && !this.isStreamFinished(conversationId)) {
          state.connectionState = "disconnected";
        }
        if (callbacks.error) {
          callbacks.error(e);
        }
        this.cleanupStream(conversationId);
      },
      timeout: (e: CustomEvent) => {
        const state = this.streamStates.get(conversationId);
        if (state && !this.isStreamFinished(conversationId)) {
          state.connectionState = "resuming";
        }
        if (callbacks.timeout) {
          callbacks.timeout(e);
        }
        this.cleanupStream(conversationId);
      },
    };

    this.callbacks.set(conversationId, wrappedCallbacks);

    if (wrappedCallbacks.message) {
      stream.addEventListener("message", wrappedCallbacks.message);
    }
    if (wrappedCallbacks.error) {
      stream.addEventListener("error", wrappedCallbacks.error);
    }
    if (wrappedCallbacks.timeout) {
      stream.addEventListener("timeout", wrappedCallbacks.timeout);
    }
  }

  getActiveConversationIds(): string[] {
    return Array.from(this.streams.keys()).filter((id) =>
      this.hasActiveStream(id),
    );
  }

  cleanupFinishedStreams(): void {
    const finishedIds: string[] = [];

    this.streamStates.forEach((_state, conversationId) => {
      if (this.isStreamFinished(conversationId)) {
        finishedIds.push(conversationId);
      }
    });

    if (finishedIds.length > 0) {
      finishedIds.forEach((id) => {
        this.closeAndCleanup(id);
      });
    }
  }

  getDebugInfo(): any {
    const info: any = {
      activeConversationId: this.activeConversationId,
      totalStreams: this.streams.size,
      totalStates: this.streamStates.size,
      streams: {},
    };

    this.streamStates.forEach((state, conversationId) => {
      info.streams[conversationId] = {
        isActive: this.hasActiveStream(conversationId),
        isFinished: this.isStreamFinished(conversationId),
        runTerminals: state.runTerminals,
        historyRunIds: state.historyRunIds,
        connectionState: state.connectionState,
        messageListLength: state.messageList?.length || 0,
      };
    });

    return info;
  }
}

export const streamManager = new StreamManager();
