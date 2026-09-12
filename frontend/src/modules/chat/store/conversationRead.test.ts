import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { startConversationRunningSync, useConversationRunningStore as store } from "./conversationRunning";
const post = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ axiosInstance: { post }, BASE_URL: "" }));
let stop: (() => void) | undefined;
let visibility: DocumentVisibilityState = "visible";
const tick = (ms = 0) => vi.advanceTimersByTimeAsync(ms);
const result = (version = "run-1", terminal_status = "completed") => ({ data: { statuses: [
  { conversation_id: "a", status: "idle", terminal_status, terminal_version: version },
] } });
beforeEach(() => {
  vi.useFakeTimers(); post.mockReset(); localStorage.clear(); visibility = "visible";
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  store.setState({ entries: {}, watchers: {} });
  store.getState().watch("sidebar", ["a"]);
});
afterEach(() => { stop?.(); stop = undefined; vi.restoreAllMocks(); vi.useRealTimers(); });

it.each(["completed", "failed", "canceled"])("acknowledges %s after viewing and preserves new unread results", async (status) => {
  post.mockResolvedValue(result("run-1", status));
  stop = startConversationRunningSync("user-a"); await tick();
  expect(store.getState().entries.a.terminalRead).toBe(false);
  store.getState().watch("current-route", ["a"]);
  expect(store.getState().entries.a.terminalRead).toBe(true);
  store.getState().watch("current-route", []); await tick(5100);
  expect(store.getState().entries.a.terminalRead).toBe(true);
  stop(); stop = startConversationRunningSync("user-a"); await tick();
  expect(store.getState().entries.a.terminalRead).toBe(true);
  post.mockResolvedValue(result("run-2", status)); await tick(5000);
  expect(store.getState().entries.a.terminalRead).toBe(false);
});
it("reads results arriving in the open conversation and leaves background results unread", async () => {
  store.getState().watch("sidebar", ["a", "b"]);
  store.getState().watch("current-route", ["a"]);
  post.mockResolvedValue({ data: { statuses: [...result().data.statuses,
    { conversation_id: "b", status: "idle", terminal_status: "completed", terminal_version: "b-1" },
  ] } });
  stop = startConversationRunningSync("user-a"); await tick();
  expect(store.getState().entries.a.terminalRead).toBe(true);
  expect(store.getState().entries.b.terminalRead).toBe(false);
});
it("does not acknowledge results while the page is hidden", async () => {
  let resolve!: (value: ReturnType<typeof result>) => void;
  post.mockImplementation(() => new Promise((done) => { resolve = done; }));
  stop = startConversationRunningSync("user-a"); await tick();
  visibility = "hidden"; document.dispatchEvent(new Event("visibilitychange"));
  resolve(result()); await tick();
  store.getState().watch("current-route", ["a"]);
  expect(store.getState().entries.a.terminalRead).toBe(false);
  visibility = "visible"; document.dispatchEvent(new Event("visibilitychange"));
  expect(store.getState().entries.a.terminalRead).toBe(true);
});
it("isolates accounts and retains receipts through temporary status errors", async () => {
  store.getState().watch("current-route", ["a"]);
  post.mockResolvedValue(result()); stop = startConversationRunningSync("user-a"); await tick();
  store.getState().watch("current-route", []);
  post.mockResolvedValue({ data: { statuses: [{ conversation_id: "a", status: "unknown" }] } }); await tick(5100);
  post.mockResolvedValue(result()); await tick(5000);
  expect(store.getState().entries.a.terminalRead).toBe(true);
  stop(); stop = startConversationRunningSync("user-b"); await tick();
  expect(store.getState().entries.a.terminalRead).toBe(false);
});
it("keeps working when browser storage is unavailable", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("unavailable"); });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("quota"); });
  store.getState().watch("current-route", ["a"]);
  post.mockResolvedValue(result()); stop = startConversationRunningSync("user-a"); await tick();
  store.getState().watch("current-route", []); await tick(5100);
  expect(store.getState().entries.a.terminalRead).toBe(true);
});
