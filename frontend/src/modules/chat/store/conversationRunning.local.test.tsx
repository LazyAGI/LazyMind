import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import ConversationRunningIndicator from "../components/RecordList/ConversationRunningIndicator";
import { startConversationRunningSync, useConversationRunningStore as store } from "./conversationRunning";
import { requestConversationStatusRefresh } from "../utils/conversationStatusEvents";

const post = vi.hoisted(() => vi.fn());
const features = vi.hoisted(() => ({ useLocalGateway: true }));
const gateway = vi.hoisted(() => ({ url: "http://localhost:8090" }));
vi.mock("@/components/request", () => ({ axiosInstance: { post }, get BASE_URL() { return gateway.url; } }));
vi.mock("@/runtime/features", () => ({ runtimeFeatures: features }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

let stop: (() => void) | undefined;
let online = false;
let visibility: DocumentVisibilityState = "visible";
const tick = (ms = 0) => act(() => vi.advanceTimersByTimeAsync(ms));
const running = { data: { statuses: [{ conversation_id: "a", status: "running" }] } };

beforeEach(() => {
  vi.useFakeTimers();
  post.mockReset();
  // Docker serves the cloud-profile build on localhost without local auto-login.
  features.useLocalGateway = false;
  gateway.url = "http://localhost:8090";
  online = false;
  visibility = "visible";
  vi.spyOn(navigator, "onLine", "get").mockImplementation(() => online);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  store.setState({ entries: {}, watchers: {} });
  store.getState().watch("sidebar", ["a", "b"]);
  store.getState().watch("current-route", ["a"]);
});

afterEach(() => {
  act(() => stop?.());
  stop = undefined;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it.each(["completed", "failed", "canceled"])("shows running then background %s and acknowledges it on return when the local gateway is reachable but the browser reports offline", async (terminalStatus) => {
  let generating = true;
  let read = false;
  post.mockImplementation(async (url, body) => {
    if (url.endsWith(":readResult")) {
      expect(body).toEqual({ terminal_version: "result-1" });
      read = true;
      return { status: 204 };
    }
    return generating ? running : { data: { statuses: [{
      conversation_id: "a", status: "idle", terminal_status: terminalStatus,
      terminal_version: "result-1", terminal_read: read,
    }] } };
  });
  render(<ConversationRunningIndicator conversationId="a" />);
  stop = startConversationRunningSync("test-user");
  await tick();
  expect(screen.getByRole("img", { name: "chat.conversationRunning" })).toBeInTheDocument();

  act(() => store.getState().watch("current-route", ["b"]));
  await tick(100);
  expect(screen.getByRole("img", { name: "chat.conversationRunning" })).toBeInTheDocument();
  generating = false;
  await tick(5000);
  const label = `chat.conversation${terminalStatus[0].toUpperCase()}${terminalStatus.slice(1)}`;
  expect(screen.getByRole("img", { name: label })).toBeInTheDocument();
  expect(read).toBe(false);

  act(() => store.getState().watch("current-route", ["a"]));
  await tick();
  expect(read).toBe(true);
  expect(screen.queryByRole("img", { name: label })).not.toBeInTheDocument();
  act(() => {
    store.getState().watch("current-route", ["b"]);
    stop?.();
  });
  localStorage.clear();
  stop = startConversationRunningSync("test-user");
  await tick();
  expect(screen.queryByRole("img", { name: label })).not.toBeInTheDocument();
});

it("continues local polling on an offline event but still pauses when hidden", async () => {
  online = true;
  post.mockResolvedValue(running);
  stop = startConversationRunningSync("test-user");
  await tick();
  online = false;
  window.dispatchEvent(new Event("offline"));
  await tick(100);
  expect(post).toHaveBeenCalledTimes(2);
  expect(store.getState().entries.a.status).toBe("running");
  visibility = "hidden";
  document.dispatchEvent(new Event("visibilitychange"));
  await tick(60_000);
  expect(post).toHaveBeenCalledTimes(2);
  visibility = "visible";
  document.dispatchEvent(new Event("visibilitychange"));
  await tick(100);
  expect(post).toHaveBeenCalledTimes(3);
});

it("uses actual local request failures for unavailable state and recovers without an online event", async () => {
  post.mockResolvedValueOnce(running).mockRejectedValue(new Error("local gateway unavailable"));
  stop = startConversationRunningSync("test-user");
  await tick();
  await tick(30_000);
  expect(store.getState().entries.a.status).toBe("unknown");
  post.mockResolvedValue(running);
  requestConversationStatusRefresh("a");
  await tick(100);
  expect(store.getState().entries.a.status).toBe("running");
});

it("preserves offline pause for deployments without a local gateway", async () => {
  features.useLocalGateway = false;
  gateway.url = "https://cloud.example.test";
  post.mockResolvedValue(running);
  stop = startConversationRunningSync("test-user");
  await tick(60_000);
  expect(post).not.toHaveBeenCalled();
  online = true;
  window.dispatchEvent(new Event("online"));
  await tick(100);
  expect(post).toHaveBeenCalledTimes(1);
});
