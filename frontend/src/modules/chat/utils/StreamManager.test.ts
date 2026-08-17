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
    } as CustomEvent);
  }
  emitEvent(type: "error" | "timeout") {
    this.listeners.get(type)?.({ type } as CustomEvent);
  }
}

const terminal = (runId: string) => ({
  schema_version: 1,
  event_id: `evt_${runId}`,
  run_id: runId,
  type: "run_finished",
  data: { status: "completed", reason: "normal", partial_output: true },
});

describe("StreamManager runtime terminal", () => {
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
