

import { SSE } from "./sse";

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
  finish_reason?: string;
  messageId?: string;
  history_id?: string;
  messageList?: any[];
}


class StreamManager {
  private streams: Map<string, SSE> = new Map();
  private callbacks: Map<string, StreamCallbacks> = new Map();
  private streamStates: Map<string, StreamState> = new Map();
  private activeConversationId: string | null = null;

  
  registerStream(
    conversationId: string,
    sse: SSE,
    callbacks: StreamCallbacks,
  ): void {
    this.streams.forEach((existing, existingConversationId) => {
      if (existingConversationId !== conversationId) {
        try {
          existing.close();
        } catch (error) {
          console.warn("Failed to close stale stream:", error);
        }
        this.streams.delete(existingConversationId);
        this.callbacks.delete(existingConversationId);
        this.streamStates.delete(existingConversationId);
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

    if (!this.streamStates.has(conversationId)) {
      this.streamStates.set(conversationId, {
        conversationId,
        delta: "",
        reasoning_content: "",
        sources: undefined,
        finish_reason: undefined,
        messageId: undefined,
        history_id: undefined,
      });
    } else {
      const existingState = this.streamStates.get(conversationId);
      if (existingState) {
        existingState.delta = "";
        existingState.reasoning_content = "";
        existingState.finish_reason = undefined;
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
        } catch {
        }

        this.updateStreamState(conversationId, e);
        if (callbacks.message) {
          callbacks.message(e);
        }
      },
      error: (e: CustomEvent) => {
        if (callbacks.error) {
          callbacks.error(e);
        }
        this.cleanupStream(conversationId);
      },
      timeout: (e: CustomEvent) => {
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
  }

  
  private updateStreamState(conversationId: string, e: CustomEvent): void {
    if (!this.streamStates.has(conversationId)) {
      this.streamStates.set(conversationId, {
        conversationId,
        delta: "",
        reasoning_content: "",
        sources: undefined,
        finish_reason: undefined,
        messageId: undefined,
        history_id: undefined,
      });
    }

    const state = this.streamStates.get(conversationId);
    if (!state) {
      return;
    }

    try {
      const data = (e as any).data;
      if (typeof data === "string") {
        if (data.trim() === "[DONE]") {
          return;
        }
        const parsed = JSON.parse(data);
        const result = parsed?.result;
        if (result) {
          if (result.sources && result.sources.length > 0) {
            state.sources = result.sources;
          }
          if (result.finish_reason) {
            state.finish_reason = result.finish_reason;
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
        }
      }
    } catch (error) {
      console.error("Failed to parse stream data:", error);
    }
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
    if (state?.finish_reason) {
      this.streams.delete(conversationId);
      this.callbacks.delete(conversationId);
    }
  }

  
  isStreamFinished(conversationId: string): boolean {
    const state = this.streamStates.get(conversationId);
    if (!state || !state.finish_reason) {
      return false;
    }
    return state.finish_reason !== "FINISH_REASON_UNSPECIFIED";
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
        console.error("[StreamManager] 关闭流失败:", error);
      }
    }

    this.streams.delete(conversationId);
    this.callbacks.delete(conversationId);
    this.streamStates.delete(conversationId);

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
        } catch {
        }

        this.updateStreamState(conversationId, e);
        if (callbacks.message) {
          callbacks.message(e);
        }
      },
      error: (e: CustomEvent) => {
        if (callbacks.error) {
          callbacks.error(e);
        }
        this.cleanupStream(conversationId);
      },
      timeout: (e: CustomEvent) => {
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
        finish_reason: state.finish_reason,
        messageListLength: state.messageList?.length || 0,
      };
    });

    return info;
  }
}

export const streamManager = new StreamManager();
