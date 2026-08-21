export const STREAM_RECOVERY_INITIAL_ATTEMPTS = 5;
export const STREAM_RECOVERY_MAX_ATTEMPTS = 8;
export const STREAM_RECOVERY_DELAYS_MS = [
  1_000,
  2_000,
  4_000,
  8_000,
  10_000,
  10_000,
  10_000,
  10_000,
] as const;

export type StreamRecoveryStatus = "idle" | "resuming" | "failed";

export interface StreamRecoveryViewState {
  conversationId: string;
  status: StreamRecoveryStatus;
  attempt: number;
  maxAttempts: number;
}

export interface StreamRecoveryEntry {
  attempt: number;
  status: Exclude<StreamRecoveryStatus, "idle">;
  timer: ReturnType<typeof setTimeout> | null;
}

export function idleStreamRecoveryState(
  conversationId = "",
): StreamRecoveryViewState {
  return {
    conversationId,
    status: "idle",
    attempt: 0,
    maxAttempts: STREAM_RECOVERY_MAX_ATTEMPTS,
  };
}

export function isTemporaryStreamFailure(status: number): boolean {
  return (
    status === 0 ||
    status === 408 ||
    status === 425 ||
    status === 429 ||
    status >= 500
  );
}

export function recoveryActionAfterFailure(
  completedAttempts: number,
): "retry" | "reconcile" | "final-reconcile" {
  if (completedAttempts >= STREAM_RECOVERY_MAX_ATTEMPTS) {
    return "final-reconcile";
  }
  if (completedAttempts === STREAM_RECOVERY_INITIAL_ATTEMPTS) {
    return "reconcile";
  }
  return "retry";
}

export function recoveryDelayForAttempt(attempt: number): number {
  const index = Math.max(
    0,
    Math.min(attempt - 1, STREAM_RECOVERY_DELAYS_MS.length - 1),
  );
  return STREAM_RECOVERY_DELAYS_MS[index];
}

export function preserveProviderRetryAfterReconciliation(
  reconciledList: any[],
  cachedList: any[],
  assistantRole: string,
): any[] {
  const merged = [...reconciledList];
  const latestCachedAssistant = cachedList.findLast(
    (item) => item?.role === assistantRole,
  );
  const latestMergedAssistantIndex = merged.findLastIndex(
    (item) => item?.role === assistantRole,
  );
  if (
    latestCachedAssistant?.model_retry &&
    latestMergedAssistantIndex >= 0 &&
    !merged[latestMergedAssistantIndex]?.run_terminal &&
    !merged[latestMergedAssistantIndex]?.run_status
  ) {
    merged[latestMergedAssistantIndex] = {
      ...merged[latestMergedAssistantIndex],
      model_retry: latestCachedAssistant.model_retry,
    };
  }
  return merged;
}

export class StreamRecoveryRegistry {
  private entries = new Map<string, StreamRecoveryEntry>();

  get(conversationId: string): StreamRecoveryEntry | undefined {
    return this.entries.get(conversationId);
  }

  ensure(conversationId: string): StreamRecoveryEntry {
    const existing = this.entries.get(conversationId);
    if (existing) {
      return existing;
    }
    const entry: StreamRecoveryEntry = {
      attempt: 0,
      status: "resuming",
      timer: null,
    };
    this.entries.set(conversationId, entry);
    return entry;
  }

  clear(conversationId: string): void {
    const entry = this.entries.get(conversationId);
    if (entry?.timer) {
      clearTimeout(entry.timer);
    }
    this.entries.delete(conversationId);
  }

  clearAll(): void {
    this.entries.forEach((entry) => {
      if (entry.timer) {
        clearTimeout(entry.timer);
      }
    });
    this.entries.clear();
  }
}
