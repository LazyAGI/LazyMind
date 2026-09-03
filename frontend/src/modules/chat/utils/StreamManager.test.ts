import { describe, expect, it, vi } from "vitest";
import { StreamManager } from "./StreamManager";

class FakeSSE {
  readyState = 1;
  listeners = new Map<string, (event: CustomEvent) => void>();
  addEventListener(type: string, callback: (event: CustomEvent) => void) {
    this.listeners.set(type, callback);
  }
  removeEventListener(type: string) {
    this.listeners.delete(type);
  }
  close() {
    this.readyState = 2;
  }
  emit(result: Record<string, unknown>) {
    this.listeners.get("message")?.({
      data: JSON.stringify({ result }),
    } as unknown as CustomEvent);
  }
  emitEvent(type: "error" | "timeout") {
    this.listeners.get(type)?.({ type } as CustomEvent);
  }
}

const terminal = (
  runId: string,
  status: "completed" | "interrupted" | "failed" | "cancelled" = "completed",
) => ({
  schema_version: 1,
  event_id: `evt_${runId}`,
  run_id: runId,
  type: "run_finished",
  data: {
    status,
    reason: {
      completed: "normal",
      interrupted: "model_incomplete",
      failed: "runtime_failure",
      cancelled: "user_cancelled",
    }[status],
    partial_output: true,
  },
});

describe("StreamManager runtime terminal", () => {
  it("keeps an opted-in side-chat stream alive beside the main stream", () => {
    const manager = new StreamManager();
    const main = new FakeSSE();
    const side = new FakeSSE();

    manager.registerStream("main", main as any, {});
    manager.registerStream("side", side as any, {}, undefined, {
      allowConcurrent: true,
    });

    expect(manager.hasActiveStream("main")).toBe(true);
    expect(manager.hasActiveStream("side")).toBe(true);
    expect(main.readyState).toBe(1);
    expect(side.readyState).toBe(1);
  });

  it("closes a side-chat stream without interrupting the main stream", () => {
    const manager = new StreamManager();
    const main = new FakeSSE();
    const side = new FakeSSE();

    manager.registerStream("main", main as any, {});
    manager.registerStream("side", side as any, {}, undefined, {
      allowConcurrent: true,
    });
    manager.closeAndCleanup("side");

    expect(manager.hasActiveStream("main")).toBe(true);
    expect(manager.hasActiveStream("side")).toBe(false);
    expect(main.readyState).toBe(1);
    expect(side.readyState).toBe(2);
  });

  it("preserves legacy main-chat switching while a side chat is active", () => {
    const manager = new StreamManager();
    const firstMain = new FakeSSE();
    const side = new FakeSSE();
    const nextMain = new FakeSSE();

    manager.registerStream("main-a", firstMain as any, {});
    manager.registerStream("side", side as any, {}, undefined, {
      allowConcurrent: true,
    });
    manager.registerStream("main-b", nextMain as any, {});

    expect(manager.hasActiveStream("main-a")).toBe(false);
    expect(manager.hasActiveStream("main-b")).toBe(true);
    expect(manager.hasActiveStream("side")).toBe(true);
    expect(firstMain.readyState).toBe(2);
    expect(side.readyState).toBe(1);
  });

  it("finishes a legacy stream from its terminal finish reason", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    manager.registerStream("conv", stream as any, {});
    stream.emit({
      conversation_id: "conv",
      history_id: "h1",
      finish_reason: "FINISH_REASON_UNSPECIFIED",
      delta: "answer",
    });
    expect(manager.isStreamFinished("conv")).toBe(false);

    stream.emit({
      conversation_id: "conv",
      history_id: "h1",
      finish_reason: "FINISH_REASON_STOP",
    });

    expect(manager.isStreamFinished("conv")).toBe(true);
  });

  it("finishes a terminal-only run without a history id", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    manager.registerStream("conv", stream as any, {});
    stream.emit({ conversation_id: "conv", runtime_event: terminal("r1") });

    expect(manager.isStreamFinished("conv")).toBe(true);
    expect(manager.getStreamState("conv")?.runTerminals.r1.status).toBe(
      "completed",
    );
  });

  it("keeps a terminal first frame when registering the real conversation", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const firstFrame = {
      data: JSON.stringify({
        result: {
          conversation_id: "real-conv",
          history_id: "h1",
          runtime_event: terminal("r1"),
        },
      }),
    } as unknown as CustomEvent;

    manager.registerStream("real-conv", stream as any, {}, firstFrame);

    expect(manager.isStreamFinished("real-conv")).toBe(true);
  });

  it("finishes only after every answer branch has run_finished", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    manager.registerStream("conv", stream as any, {});
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "a" });
    stream.emit({ conversation_id: "conv", history_id: "h2", delta: "b" });
    stream.emit({
      conversation_id: "conv",
      history_id: "h1",
      runtime_event: terminal("r1"),
    });
    expect(manager.isStreamFinished("conv")).toBe(false);
    stream.emit({
      conversation_id: "conv",
      history_id: "h2",
      runtime_event: terminal("r2"),
    });
    expect(manager.isStreamFinished("conv")).toBe(true);
  });

  it("aggregates dual-answer terminal status using the worst outcome", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    manager.registerStream("conv", stream as any, {});
    stream.emit({ conversation_id: "conv", history_id: "h1" });
    stream.emit({ conversation_id: "conv", history_id: "h2" });
    stream.emit({
      conversation_id: "conv",
      history_id: "h1",
      runtime_event: terminal("r1"),
    });
    stream.emit({
      conversation_id: "conv",
      history_id: "h2",
      runtime_event: terminal("r2", "failed"),
    });

    expect(manager.getAggregatedRunTerminal("conv")?.status).toBe("failed");
    expect(manager.getStreamState("conv")?.historyRunIds).toEqual({
      h1: "r1",
      h2: "r2",
    });
  });

  it("does not deliver body frames after the terminal", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const onMessage = vi.fn();
    manager.registerStream("conv", stream as any, { message: onMessage });
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "ok" });
    stream.emit({
      conversation_id: "conv",
      history_id: "h1",
      runtime_event: terminal("r1"),
    });
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "late" });
    expect(onMessage).toHaveBeenCalledTimes(2);
  });

  it("keeps unfinished state resumable after a transport error", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    manager.registerStream("conv", stream as any, {});
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "partial" });

    stream.emitEvent("error");

    expect(manager.getStreamState("conv")?.connectionState).toBe(
      "disconnected",
    );
    expect(manager.isStreamFinished("conv")).toBe(false);
  });

  it("marks timeouts as resuming without inventing a terminal", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    manager.registerStream("conv", stream as any, {});
    stream.emit({ conversation_id: "conv", history_id: "h1" });

    stream.emitEvent("timeout");

    expect(manager.getStreamState("conv")?.connectionState).toBe("resuming");
    expect(manager.isStreamFinished("conv")).toBe(false);
  });
});
