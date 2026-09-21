import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { startConversationRunningSync, useConversationRunningStore as store } from "./conversationRunning";

const post = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ axiosInstance: { post }, BASE_URL: "" }));

let stop: (() => void) | undefined;
let visibility: DocumentVisibilityState = "visible";
const receiptKey = "conversation-terminal-read:user-a";
const result = (terminalStatus: string, terminalRead: boolean) => ({ data: { statuses: [{
  conversation_id: "a",
  status: "idle",
  terminal_status: terminalStatus,
  terminal_version: "run-1",
  terminal_read: terminalRead,
}] } });

beforeEach(() => {
  vi.useFakeTimers();
  post.mockReset();
  localStorage.clear();
  visibility = "visible";
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  store.setState({ entries: {}, watchers: {} });
  store.getState().watch("sidebar", ["a"]);
});

afterEach(() => {
  stop?.();
  stop = undefined;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it.each(["completed", "failed", "canceled"])("keeps server-baselined historical %s results read without browser receipts", async (status) => {
  post.mockResolvedValue(result(status, true));
  stop = startConversationRunningSync("user-a");
  await vi.advanceTimersByTimeAsync(0);

  expect(store.getState().entries.a.terminalRead).toBe(true);
});

it.each(["completed", "failed", "canceled"])("keeps acknowledged %s results read after clearing browser storage and restarting", async (status) => {
  localStorage.setItem(receiptKey, JSON.stringify({ a: "run-1" }));
  post.mockResolvedValue(result(status, true));
  stop = startConversationRunningSync("user-a");
  await vi.advanceTimersByTimeAsync(0);
  expect(store.getState().entries.a.terminalRead).toBe(true);

  stop();
  localStorage.clear();
  stop = startConversationRunningSync("user-a");
  await vi.advanceTimersByTimeAsync(0);

  expect(store.getState().entries.a.terminalRead).toBe(true);
});

it("does not let legacy browser receipts override a server-unread result", async () => {
  localStorage.setItem(receiptKey, JSON.stringify({ a: "run-1" }));
  post.mockResolvedValue(result("completed", false));
  stop = startConversationRunningSync("user-a");
  await vi.advanceTimersByTimeAsync(0);

  expect(store.getState().entries.a.terminalRead).toBe(false);
});

const tick = (ms = 0) => vi.advanceTimersByTimeAsync(ms);
const readURL = "/api/core/conversations/a:readResult";
const readRequests = () => post.mock.calls.filter(([url]) => url === readURL);

function wireServer(acknowledge: (body: { terminal_version: string }) => Promise<unknown>) {
  let snapshot = result("completed", false);
  post.mockImplementation((url, body) => {
    if (url === "/api/core/conversations:batchStatus") return Promise.resolve(snapshot);
    if (url === readURL) return acknowledge(body);
    throw new Error(`Unexpected request: ${url}`);
  });
  return (version: string, read = false) => {
    snapshot = result("completed", read);
    snapshot.data.statuses[0].terminal_version = version;
  };
}

it("confirms the viewed version once and only marks it read after server success", async () => {
  let resolve!: (response: unknown) => void;
  wireServer(() => new Promise(done => { resolve = done; }));
  store.getState().watch("current-route", ["a"]);
  stop = startConversationRunningSync("user-a");
  await tick();
  expect(readRequests()).toHaveLength(1);
  expect(readRequests()[0][1]).toEqual({ terminal_version: "run-1" });
  expect(store.getState().entries.a.terminalRead).toBe(false);
  await tick(5100);
  expect(readRequests()).toHaveLength(1);
  resolve({ status: 204 });
  await tick();
  expect(store.getState().entries.a.terminalRead).toBe(true);
});

it("keeps a failed confirmation unread and retries during a later poll", async () => {
  const acknowledge = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ status: 204 });
  wireServer(acknowledge);
  store.getState().watch("current-route", ["a"]);
  stop = startConversationRunningSync("user-a");
  await tick();
  expect(acknowledge).toHaveBeenCalledTimes(1);
  expect(store.getState().entries.a.terminalRead).toBe(false);
  await tick(5100);
  expect(acknowledge).toHaveBeenCalledTimes(2);
  expect(store.getState().entries.a.terminalRead).toBe(true);
});

it("does not mark a newer background result read when an older confirmation completes", async () => {
  let resolve!: (response: unknown) => void;
  const setResult = wireServer(() => new Promise(done => { resolve = done; }));
  store.getState().watch("current-route", ["a"]);
  stop = startConversationRunningSync("user-a");
  await tick();
  expect(readRequests()).toHaveLength(1);
  store.getState().watch("current-route", []);
  setResult("run-2");
  await tick(5100);
  resolve({ status: 204 });
  await tick();
  expect(store.getState().entries.a.terminalVersion).toBe("run-2");
  expect(store.getState().entries.a.terminalRead).toBe(false);
  expect(readRequests()).toHaveLength(1);
});

it("ignores an old account's confirmation response after switching accounts", async () => {
  let resolve!: (response: unknown) => void;
  wireServer(() => new Promise(done => { resolve = done; }));
  store.getState().watch("current-route", ["a"]);
  stop = startConversationRunningSync("user-a");
  await tick();
  expect(readRequests()).toHaveLength(1);
  stop();
  store.getState().watch("current-route", []);
  stop = startConversationRunningSync("user-b");
  await tick();
  resolve({ status: 204 });
  await tick();
  expect(store.getState().entries.a.terminalRead).toBe(false);
});

it("does not confirm a result that arrives after the page becomes hidden", async () => {
  let resolve!: (response: unknown) => void;
  post.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  store.getState().watch("current-route", ["a"]);
  stop = startConversationRunningSync("user-a");
  await tick();
  visibility = "hidden";
  document.dispatchEvent(new Event("visibilitychange"));
  resolve(result("failed", false));
  await tick();
  expect(store.getState().entries.a.terminalRead).toBe(false);
  expect(readRequests()).toHaveLength(0);
  wireServer(async () => ({ status: 204 }));
  visibility = "visible";
  document.dispatchEvent(new Event("visibilitychange"));
  await tick(100);
  expect(readRequests()).toHaveLength(1);
});

it("keeps legacy backend terminal results quiet while retaining live activity states", async () => {
  post.mockResolvedValue({ data: { statuses: [
    { conversation_id: "a", status: "idle", terminal_status: "completed", terminal_version: "old" },
    { conversation_id: "b", status: "running" },
    { conversation_id: "c", status: "unknown" },
  ] } });
  store.getState().watch("sidebar", ["a", "b", "c"]);
  stop = startConversationRunningSync("user-a");
  await tick();
  const entries = store.getState().entries;
  expect(entries.a.terminalRead || !entries.a.terminalStatus).toBe(true);
  expect(entries.b.status).toBe("running");
  expect(entries.c.status).toBe("unknown");
  expect(readRequests()).toHaveLength(0);
});
