import { describe, expect, it, vi } from "vitest";
import { StreamManager } from "./StreamManager";
import type { ChatModelOutput } from "@/api/generated/core-client";

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

const output = (call_index: number, phase?: ChatModelOutput["phase"], run_id = "output-run"): ChatModelOutput =>
  ({ run_id, call_index, ...(phase ? { phase } : {}) });

describe("StreamManager Host output projection", () => {
  it("rejects old calls and repeated boundaries while accepting absolute empty sources", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const message = vi.fn();
    manager.registerStream("conv", stream as any, { message });
    const emit = (frame: object) => stream.emit({ conversation_id: "conv", history_id: "h", ...frame });
    emit({ model_output: output(0, "commit") });
    emit({ model_output: output(1, "begin") });
    emit({ model_output: output(1), delta: "committed" });
    emit({ model_output: output(1, "commit") });
    emit({ delta: "tool result" });
    emit({ model_output: output(2, "begin") });
    emit({ model_output: output(2), delta: "bad", sources: [{ title: "bad" }] });
    const rollback = { model_output: output(2, "rollback"), delta_mode: "replace", delta: "committed tool result",
      reasoning_content: "", sources: [], exports: [] };
    emit(rollback);
    expect(manager.getStreamState("conv")?.sources).toEqual([]);
    const accepted = message.mock.calls.length;
    for (const frame of [rollback, { model_output: output(2, "begin") }, { model_output: output(2), delta: "late" },
      { model_output: output(2, "commit") }, { model_output: output(3), delta: "missing begin" },
      { model_output: output(3, "begin", "other-run"), delta: "foreign" }]) emit(frame);
    expect(message).toHaveBeenCalledTimes(accepted);
    emit({ model_output: output(3, "begin") });
    emit({ model_output: output(3), delta: "replacement" });
    emit({ model_output: output(3, "commit") });
    emit({ delta_mode: "replace", delta: "final committed export", exports: [] });
    expect(message).toHaveBeenCalledTimes(accepted + 4);
  });

  it("hydrates a closed snapshot once per connection and keeps same-SSE callback restores fenced", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const message = vi.fn();
    manager.registerStream("conv", stream as any, { message });
    const snapshot = { conversation_id: "conv", history_id: "h", model_output: output(4, "rollback"),
      delta_mode: "replace", delta: "restored", reasoning_content: "", sources: [], exports: [] };
    stream.emit({ conversation_id: "conv", model_output: output(0, "commit") });
    stream.emit(snapshot);
    stream.emit({ conversation_id: "conv", delta: "true tool result" });
    const queued = manager.getCallbacks("conv")!;
    const restoredMessage = vi.fn();
    manager.restoreStreamCallbacks("conv", { message: restoredMessage });
    stream.emit(snapshot);
    expect(restoredMessage).not.toHaveBeenCalled();
    queued.message?.({ data: JSON.stringify({ result: { conversation_id: "conv", delta: "old callback" } }) } as any);
    queued.error?.({ type: "error" } as CustomEvent);
    queued.timeout?.({ type: "timeout" } as CustomEvent);
    expect(message).toHaveBeenCalledTimes(3);
    expect(manager.getStreamState("conv")?.connectionState).toBe("connected");
    const previous = manager.getCallbacks("conv")!;
    const resumed = new FakeSSE();
    manager.registerStream("conv", resumed as any, { message: restoredMessage });
    resumed.emit(snapshot);
    expect(restoredMessage).toHaveBeenCalledTimes(1);
    previous.message?.({ data: JSON.stringify({ result: snapshot }) } as any);
    previous.error?.({ type: "error" } as CustomEvent);
    previous.timeout?.({ type: "timeout" } as CustomEvent);
    expect(restoredMessage).toHaveBeenCalledTimes(1);
    expect(manager.getStreamState("conv")?.connectionState).toBe("connected");
  });

  it("hydrates an open snapshot and binds the next connection to its new run", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const message = vi.fn();
    manager.registerStream("conv", stream as any, { message });
    stream.emit({ conversation_id: "conv", model_output: output(2, "begin"), delta_mode: "replace", delta: "preview", sources: [] });
    stream.emit({ conversation_id: "conv", model_output: output(2), delta: "more" });
    expect(message).toHaveBeenCalledTimes(2);
    const next = new FakeSSE();
    manager.registerStream("conv", next as any, { message });
    next.emit({ conversation_id: "conv", model_output: output(0, "commit", "new-run") });
    next.emit({ conversation_id: "conv", model_output: output(3, "begin"), delta: "old run" });
    next.emit({ conversation_id: "conv", runtime_event: terminal("output-run") });
    expect(message).toHaveBeenCalledTimes(3);
    expect(manager.isStreamFinished("conv")).toBe(false);
    next.emit({ conversation_id: "conv", model_output: output(1, "begin", "new-run") });
    next.emit({ conversation_id: "conv", model_output: output(1, undefined, "new-run"), delta: "new" });
    expect(message).toHaveBeenCalledTimes(5);
  });

  it("delivers a metadata-less terminal after rollback without replaying projection", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const message = vi.fn();
    manager.registerStream("conv", stream as any, { message });
    stream.emit({ conversation_id: "conv", history_id: "h", model_output: output(0, "commit") });
    stream.emit({ conversation_id: "conv", history_id: "h", model_output: output(1, "begin") });
    stream.emit({ conversation_id: "conv", history_id: "h", model_output: output(1, "rollback"), delta_mode: "replace", delta: "", sources: [], exports: [] });
    const frame = { conversation_id: "conv", history_id: "h", runtime_event: terminal("output-run", "failed") };
    stream.emit(frame);
    expect(manager.isStreamFinished("conv")).toBe(true);
    expect(message).toHaveBeenCalledTimes(4);
    stream.emit({ conversation_id: "conv", history_id: "h", model_output: output(1, "rollback"), delta_mode: "replace", delta: "", sources: [], exports: [] });
    expect(message).toHaveBeenCalledTimes(4);
  });
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

  it.each(["FINISH_REASON_STOP", "FINISH_REASON_UNKNOWN"])("delivers the final body with %s before rejecting later body frames", (finishReason) => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const onMessage = vi.fn();
    manager.registerStream("conv", stream as any, { message: onMessage });

    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "partial answer", finish_reason: finishReason });

    expect(onMessage).toHaveBeenCalledTimes(1);
    expect(manager.isStreamFinished("conv")).toBe(true);
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "late answer", finish_reason: "FINISH_REASON_UNSPECIFIED" });
    expect(onMessage).toHaveBeenCalledTimes(1);
    expect(manager.isStreamFinished("conv")).toBe(true);
  });

  it.each([
    ["empty", {}],
    ["sources", { sources: [{ title: "Reference", url: "https://example.com" }] }],
  ])("keeps a legacy terminal after a nonterminal %s frame and resets only on registration", (_name, payload) => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const onMessage = vi.fn();
    manager.registerStream("conv", stream as any, { message: onMessage });
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "final", finish_reason: "FINISH_REASON_STOP" });
    stream.emit({ conversation_id: "conv", history_id: "h1", ...payload, finish_reason: "FINISH_REASON_UNSPECIFIED" });

    expect(manager.isStreamFinished("conv")).toBe(true);
    expect(manager.getStreamState("conv")?.legacyFinishReason).toBe("FINISH_REASON_STOP");
    if ("sources" in payload) expect(manager.getStreamState("conv")?.sources).toEqual(payload.sources);
    expect(onMessage).toHaveBeenCalledTimes(2);
    stream.emit({ conversation_id: "conv", history_id: "h1", delta: "late", finish_reason: "FINISH_REASON_UNSPECIFIED" });
    expect(onMessage).toHaveBeenCalledTimes(2);

    stream.emit({ conversation_id: "conv", history_id: "h1", runtime_event: terminal("r1"), finish_reason: "FINISH_REASON_UNSPECIFIED" });
    expect(manager.getAggregatedRunTerminal("conv")?.status).toBe("completed");
    expect(manager.isStreamFinished("conv")).toBe(true);

    const nextStream = new FakeSSE();
    manager.registerStream("conv", nextStream as any, { message: onMessage });
    expect(manager.isStreamFinished("conv")).toBe(false);
    expect(manager.getStreamState("conv")?.legacyFinishReason).toBeUndefined();
    expect(manager.getStreamState("conv")?.runTerminals).toEqual({});
    nextStream.emit({ conversation_id: "conv", history_id: "h2", delta: "next answer", finish_reason: "FINISH_REASON_UNSPECIFIED" });
    expect(onMessage).toHaveBeenCalledTimes(4);
    expect(manager.isStreamFinished("conv")).toBe(false);
  });

  it("finishes a static response while preserving its model invocation flag", () => {
    const manager = new StreamManager();
    const stream = new FakeSSE();
    const event = terminal("r1");
    manager.registerStream("conv", stream as any, {});
    stream.emit({
      conversation_id: "conv",
      runtime_event: {
        ...event,
        data: { ...event.data, model_invoked: false },
      },
    });

    expect(manager.isStreamFinished("conv")).toBe(true);
    expect(manager.getStreamState("conv")?.runTerminals.r1).toMatchObject({
      status: "completed",
      model_invoked: false,
    });
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
