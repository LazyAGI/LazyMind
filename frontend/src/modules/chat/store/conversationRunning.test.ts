import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { startConversationRunningSync, useConversationRunningStore as store } from "./conversationRunning";
import { requestConversationStatusRefresh } from "@/modules/chat/utils/conversationStatusEvents";

const post = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ axiosInstance: { post }, BASE_URL: "" }));

const snapshot = (statuses: Record<string, string>) => ({
  data: { statuses: Object.entries(statuses).map(([conversation_id, status]) => ({ conversation_id, status })) },
});
let stop: (() => void) | undefined;
let online = true;
let visibility: DocumentVisibilityState = "visible";
const tick = (ms = 0) => vi.advanceTimersByTimeAsync(ms);

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-09-09T00:00:00Z"));
  post.mockReset();
  online = true;
  visibility = "visible";
  vi.spyOn(navigator, "onLine", "get").mockImplementation(() => online);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  store.setState({ entries: {}, watchers: {} });
});
afterEach(() => {
  stop?.();
  stop = undefined;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("conversation running status synchronization", () => {
  it("does not query again for unchanged IDs or a different display order", async () => {
    store.getState().watch("sidebar", ["a", "b"]);
    post.mockResolvedValue(snapshot({ a: "idle", b: "idle" }));
    stop = startConversationRunningSync();
    await tick();
    const watchers = store.getState().watchers;
    store.getState().watch("sidebar", ["b", "a", "a", ""]);
    await tick(100);
    expect(store.getState().watchers).toBe(watchers);
    expect(post).toHaveBeenCalledTimes(1);
    store.getState().watch("sidebar", ["a", "c"]);
    await tick(100);
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("publishes one state update for a successful batch with nothing to prune", async () => {
    store.getState().watch("sidebar", ["a"]);
    post.mockResolvedValue(snapshot({ a: "running" }));
    const updates = vi.fn();
    const unsubscribe = store.subscribe((next, previous) => {
      if (next.entries !== previous.entries) updates(next.entries);
    });
    try {
      stop = startConversationRunningSync();
      await tick();
      expect(updates).toHaveBeenCalledTimes(1);
      expect(store.getState().entries.a.status).toBe("running");
    } finally {
      unsubscribe();
    }
  });

  it("batches rows and keeps polling when idle to discover other conversations", async () => {
    store.getState().watch("sidebar", ["a", "b", "a"]);
    post.mockResolvedValueOnce(snapshot({ a: "idle", b: "idle" })).mockResolvedValue(snapshot({ a: "running", b: "running" }));
    stop = startConversationRunningSync();
    await tick();
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][1]).toEqual({ conversation_ids: ["a", "b"] });
    await tick(5_000);
    expect(post).toHaveBeenCalledTimes(2);
    expect(store.getState().entries.a.status).toBe("running");
    expect(store.getState().entries.b.status).toBe("running");
  });

  it("retains background conversations after the current list changes", async () => {
    store.getState().watch("sidebar", ["a"]);
    post.mockResolvedValueOnce(snapshot({ a: "running" })).mockResolvedValueOnce(snapshot({ a: "running", b: "idle" })).mockResolvedValue(snapshot({ a: "idle", b: "idle" }));
    stop = startConversationRunningSync();
    await tick();
    store.getState().watch("sidebar", ["b"]);
    await tick(100);
    expect(post.mock.calls[1][1].conversation_ids.sort()).toEqual(["a", "b"]);
    expect(store.getState().entries.a.status).toBe("running");
    await tick(5_000);
    expect(store.getState().entries.a).toBeUndefined();
    expect(store.getState().entries.b.status).toBe("idle");
  });

  it("coalesces lifecycle events and discards a snapshot superseded during its request", async () => {
    store.getState().watch("sidebar", ["a"]);
    store.setState({ entries: { a: { status: "running", confirmedAt: Date.now() } } });
    let resolve!: (value: ReturnType<typeof snapshot>) => void;
    post.mockImplementationOnce(() => new Promise((done) => { resolve = done; })).mockResolvedValue(snapshot({ a: "running" }));
    stop = startConversationRunningSync();
    await tick();
    requestConversationStatusRefresh("a");
    requestConversationStatusRefresh("a");
    resolve(snapshot({ a: "idle" }));
    await tick();
    expect(store.getState().entries.a.status).toBe("running");
    await tick(100);
    expect(post).toHaveBeenCalledTimes(2);
    await tick(100);
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("pauses hidden tabs and refreshes on return", async () => {
    store.getState().watch("sidebar", ["a"]);
    post.mockResolvedValue(snapshot({ a: "running" }));
    stop = startConversationRunningSync();
    await tick();
    visibility = "hidden";
    document.dispatchEvent(new Event("visibilitychange"));
    await tick(60_000);
    expect(post).toHaveBeenCalledTimes(1);
    visibility = "visible";
    document.dispatchEvent(new Event("visibilitychange"));
    await tick(100);
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("retries a newly started background conversation when a failed request overlaps navigation", async () => {
    let reject!: (reason: Error) => void;
    post.mockImplementationOnce(() => new Promise((_done, fail) => { reject = fail; }))
      .mockImplementation(async (_url, body) => snapshot(Object.fromEntries(
        body.conversation_ids.map((id: string) => [id, id === "background" ? "running" : "idle"]),
      )));
    stop = startConversationRunningSync();
    await tick();
    requestConversationStatusRefresh("background");
    await tick(100);
    store.getState().watch("sidebar", ["other"]);
    reject(new Error("unavailable"));
    await tick(100);
    expect(post.mock.calls[1][1].conversation_ids.sort()).toEqual(["background", "other"]);
    expect(store.getState().entries.background.status).toBe("running");
  });

  it("keeps the last state briefly on failure, then shows unknown and recovers", async () => {
    store.getState().watch("sidebar", ["a"]);
    post.mockResolvedValueOnce(snapshot({ a: "running" })).mockRejectedValue(new Error("unavailable"));
    stop = startConversationRunningSync();
    await tick();
    await tick(5_000);
    expect(store.getState().entries.a.status).toBe("running");
    await tick(25_000);
    expect(store.getState().entries.a.status).toBe("unknown");
    expect(post.mock.calls[1][2].silentError).toBe(true);
    post.mockResolvedValue(snapshot({ a: "idle" }));
    window.dispatchEvent(new Event("online"));
    await tick(100);
    expect(store.getState().entries.a.status).toBe("idle");
  });

  it("does not interpret network disconnection as completion", async () => {
    store.getState().watch("sidebar", ["a"]);
    post.mockResolvedValue(snapshot({ a: "running" }));
    stop = startConversationRunningSync();
    await tick();
    online = false;
    window.dispatchEvent(new Event("offline"));
    await tick(29_999);
    expect(store.getState().entries.a.status).toBe("running");
    await tick(1);
    expect(store.getState().entries.a.status).toBe("unknown");
    expect(post).toHaveBeenCalledTimes(1);
    online = true;
    window.dispatchEvent(new Event("online"));
    await tick(100);
    expect(store.getState().entries.a.status).toBe("running");
  });

  it("removes unavailable IDs and never accepts unsolicited conversation state", async () => {
    store.getState().watch("sidebar", ["a"]);
    store.setState({ entries: { a: { status: "running", confirmedAt: Date.now() } } });
    post.mockResolvedValue(snapshot({ foreign: "running" }));
    stop = startConversationRunningSync();
    await tick();
    expect(store.getState().entries).toEqual({});
  });

  it("splits large lists into serial batches of at most 100 IDs", async () => {
    const ids = Array.from({ length: 205 }, (_, i) => `conversation-${i}`);
    store.getState().watch("sidebar", ids);
    let concurrent = 0;
    let peak = 0;
    post.mockImplementation(async (_url, body) => {
      peak = Math.max(peak, ++concurrent);
      await Promise.resolve();
      concurrent--;
      return snapshot(Object.fromEntries(body.conversation_ids.map((id: string) => [id, "idle"])));
    });
    stop = startConversationRunningSync();
    await tick();
    expect(post.mock.calls.map((call) => call[1].conversation_ids.length)).toEqual([100, 100, 5]);
    expect(peak).toBe(1);
  });

  it("clears state on logout and ignores a late response from the previous user", async () => {
    store.getState().watch("sidebar", ["a"]);
    let resolve!: (value: ReturnType<typeof snapshot>) => void;
    post.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    stop = startConversationRunningSync();
    await tick();
    stop();
    stop = undefined;
    resolve(snapshot({ a: "running" }));
    await tick(60_000);
    expect(store.getState().entries).toEqual({});
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][2].signal.aborted).toBe(true);
  });
});
