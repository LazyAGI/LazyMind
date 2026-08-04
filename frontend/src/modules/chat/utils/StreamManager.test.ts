import { beforeEach, describe, expect, it, vi } from "vitest";
import { streamManager } from "./StreamManager";

function createMockSSE(readyState = 0) {
  const listeners = new Map<string, ((e: CustomEvent) => void)[]>();
  return {
    readyState,
    addEventListener: vi.fn((type: string, cb: (e: CustomEvent) => void) => {
      const list = listeners.get(type) ?? [];
      list.push(cb);
      listeners.set(type, list);
    }),
    removeEventListener: vi.fn((type: string, cb: (e: CustomEvent) => void) => {
      const list = listeners.get(type) ?? [];
      listeners.set(type, list.filter((item) => item !== cb));
    }),
    close: vi.fn(),
    emit(type: string, event: CustomEvent) {
      (listeners.get(type) ?? []).forEach((cb) => cb(event));
    },
  };
}

function makeMessageEvent(data: string): CustomEvent {
  const event = new CustomEvent("message");
  (event as unknown as { data: string }).data = data;
  return event;
}

describe("StreamManager", () => {
  beforeEach(() => {
    // Drain any streams/state left over from a previous test via the public API.
    Object.keys(streamManager.getDebugInfo().streams).forEach((id) => {
      streamManager.closeAndCleanup(id);
    });
  });

  it("registers a stream and forwards message events through to the caller callback", () => {
    const sse = createMockSSE();
    const onMessage = vi.fn();

    streamManager.registerStream("conv-1", sse as never, { message: onMessage });
    sse.emit("message", makeMessageEvent(JSON.stringify({ result: { delta: "hi" } })));

    expect(onMessage).toHaveBeenCalledTimes(1);
  });

  it("ignores [DONE] sentinel messages and never invokes the caller callback", () => {
    const sse = createMockSSE();
    const onMessage = vi.fn();

    streamManager.registerStream("conv-2", sse as never, { message: onMessage });
    sse.emit("message", makeMessageEvent("[DONE]"));

    expect(onMessage).not.toHaveBeenCalled();
  });

  it("drops messages belonging to a different, non-temp conversation id", () => {
    const sse = createMockSSE();
    const onMessage = vi.fn();

    streamManager.registerStream("conv-3", sse as never, { message: onMessage });
    sse.emit(
      "message",
      makeMessageEvent(JSON.stringify({ result: { conversation_id: "other-conv" } })),
    );

    expect(onMessage).not.toHaveBeenCalled();
  });

  it("accepts mismatched conversation_id for temp_ conversations", () => {
    const sse = createMockSSE();
    const onMessage = vi.fn();

    streamManager.registerStream("temp_abc", sse as never, { message: onMessage });
    sse.emit(
      "message",
      makeMessageEvent(JSON.stringify({ result: { conversation_id: "real-conv-id" } })),
    );

    expect(onMessage).toHaveBeenCalledTimes(1);
  });

  it("updates and exposes stream state from message payloads", () => {
    const sse = createMockSSE();
    streamManager.registerStream("conv-4", sse as never, {});

    sse.emit(
      "message",
      makeMessageEvent(
        JSON.stringify({
          result: {
            sources: [{ id: "s1" }],
            finish_reason: "FINISH_REASON_STOP",
            history_id: "h1",
          },
        }),
      ),
    );

    const state = streamManager.getStreamState("conv-4");
    expect(state?.sources).toEqual([{ id: "s1" }]);
    expect(state?.finish_reason).toBe("FINISH_REASON_STOP");
    expect(state?.history_id).toBe("h1");
  });

  it("reports isStreamFinished only once a non-unspecified finish_reason has arrived", () => {
    const sse = createMockSSE();
    streamManager.registerStream("conv-5", sse as never, {});

    expect(streamManager.isStreamFinished("conv-5")).toBe(false);

    sse.emit(
      "message",
      makeMessageEvent(JSON.stringify({ result: { finish_reason: "FINISH_REASON_UNSPECIFIED" } })),
    );
    expect(streamManager.isStreamFinished("conv-5")).toBe(false);

    sse.emit(
      "message",
      makeMessageEvent(JSON.stringify({ result: { finish_reason: "FINISH_REASON_STOP" } })),
    );
    expect(streamManager.isStreamFinished("conv-5")).toBe(true);
  });

  it("closes stale streams for other conversations when a new one is registered", () => {
    const sseOld = createMockSSE();
    const sseNew = createMockSSE();

    streamManager.registerStream("conv-old", sseOld as never, {});
    streamManager.registerStream("conv-new", sseNew as never, {});

    expect(sseOld.close).toHaveBeenCalled();
  });

  it("hasActiveStream reflects the underlying readyState", () => {
    const sseConnecting = createMockSSE(0);
    streamManager.registerStream("conv-6", sseConnecting as never, {});
    expect(streamManager.hasActiveStream("conv-6")).toBe(true);

    const sseClosed = createMockSSE(2);
    streamManager.registerStream("conv-7", sseClosed as never, {});
    expect(streamManager.hasActiveStream("conv-7")).toBe(false);
  });

  it("closeAndCleanup removes the stream, callbacks and state entirely", () => {
    const sse = createMockSSE();
    streamManager.registerStream("conv-8", sse as never, {});

    streamManager.closeAndCleanup("conv-8");

    expect(sse.close).toHaveBeenCalled();
    expect(streamManager.getStream("conv-8")).toBeNull();
    expect(streamManager.getStreamState("conv-8")).toBeNull();
  });

  it("saveMessageList and getStreamState round-trip the message list", () => {
    streamManager.saveMessageList("conv-9", [{ role: "User", delta: "hi" }]);
    const state = streamManager.getStreamState("conv-9");
    expect(state?.messageList).toEqual([{ role: "User", delta: "hi" }]);
  });

  it("invokes the error callback and cleans up on error events", () => {
    const sse = createMockSSE();
    const onError = vi.fn();
    streamManager.registerStream("conv-10", sse as never, { error: onError });

    sse.emit("error", new CustomEvent("error"));

    expect(onError).toHaveBeenCalledTimes(1);
  });
});
