import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ForkCreateRequest, ForkPreview, ForkResult } from "@/api/generated/core-client";

const state = vi.hoisted(() => ({ user: "u1", location: { key: "A" }, navigate: vi.fn(), preview: vi.fn(), create: vi.fn() }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => state.user ? { userId: state.user } : null }, AUTH_USER_CHANGE_EVENT: "fork-test-auth" }));
vi.mock("react-router-dom", () => ({ useLocation: () => state.location, useNavigate: () => state.navigate }));
vi.mock("./api", () => ({ previewFork: state.preview, createFork: state.create }));
import { useForkConversation } from "./useForkConversation";

function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
const preview = (id = "h1") => ({ source_history_id: id, prefix_revision: "v1:prefix", excerpt: id, can_fork: true, config_issues: [] } as unknown as ForkPreview);
const created = (id = "branch") => ({ conversation: { conversation_id: id }, replayed: false } as ForkResult);
const request = (): ForkCreateRequest => ({ source_history_id: "h1", expected_prefix_revision: "v1:prefix", confirmed_fields: ["thinking_depth"], confirmed_values: { thinking_depth: "high" } });

beforeEach(() => { sessionStorage.clear(); state.user = "u1"; window.dispatchEvent(new Event("fork-test-auth")); state.location = { key: "A" }; vi.resetAllMocks(); state.preview.mockResolvedValue(preview()); state.create.mockResolvedValue(created()); });
afterEach(cleanup);

describe("Fork creation state", () => {
  it("creates and navigates with one click, automatically accepting exact suggested legacy values", async () => {
    state.preview.mockResolvedValue({ ...preview(), config_issues: [
      { field: "thinking_depth", suggested_value: "high", requires_confirmation: true },
      { field: "resource_bindings", suggested_value: { kb_id: ["kb-readable"] }, requires_confirmation: true },
    ] });
    const { result } = renderHook(() => useForkConversation("source"));
    await act(async () => result.current.begin("h1"));
    expect(state.create).toHaveBeenCalledTimes(1);
    expect(state.create.mock.calls[0][2]).toEqual({
      source_history_id: "h1", expected_prefix_revision: "v1:prefix",
      confirmed_fields: ["thinking_depth", "resource_bindings"],
      confirmed_values: { thinking_depth: "high", resource_bindings: { kb_id: ["kb-readable"] } },
    });
    expect(state.navigate).toHaveBeenCalledWith(expect.stringContaining("branch"));
    expect(result.current.pending).toBe(false);
  });

  it("locks clicks through both preview and creation", async () => {
    const reading = deferred<ForkPreview>(); const creating = deferred<ForkResult>();
    state.preview.mockReturnValue(reading.promise); state.create.mockReturnValue(creating.promise);
    const { result } = renderHook(() => useForkConversation("source"));
    act(() => { void result.current.begin("h1"); void result.current.begin("h1"); });
    expect(state.preview).toHaveBeenCalledTimes(1);
    expect(result.current.pending).toBe(true);
    await act(async () => reading.resolve(preview()));
    act(() => { void result.current.begin("h1"); void result.current.begin("h2"); });
    expect(state.create).toHaveBeenCalledTimes(1);
    expect(state.preview).toHaveBeenCalledTimes(1);
    await act(async () => creating.resolve(created()));
    expect(state.navigate).toHaveBeenCalledTimes(1);
  });

  it("does not create from a stale preview after navigation", async () => {
    const reading = deferred<ForkPreview>(); state.preview.mockReturnValue(reading.promise);
    const { result, rerender } = renderHook(({ source }) => useForkConversation(source), { initialProps: { source: "A" } });
    act(() => { void result.current.begin("h1"); });
    state.location = { key: "B" }; rerender({ source: "B" });
    await act(async () => reading.resolve(preview()));
    expect(state.create).not.toHaveBeenCalled();
    expect(result.current.pending).toBe(false);
    expect(result.current.phase).toBe("idle");
  });

  it("locks double clicks and replays the frozen request with the same key after timeout", async () => {
    const pending = deferred<ForkResult>(); state.create.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(created());
    const { result } = renderHook(() => useForkConversation("source"));
    const body = request();
    act(() => { void result.current.submit(body); void result.current.submit(body); });
    body.expected_prefix_revision = "changed"; body.confirmed_values!.thinking_depth = "low";
    expect(state.create).toHaveBeenCalledTimes(1);
    await act(async () => pending.reject(new Error("network timeout")));
    expect(result.current.phase).toBe("unknown");
    await act(async () => result.current.submit(body));
    expect(state.create).toHaveBeenCalledTimes(2);
    expect(state.create.mock.calls[1]).toEqual(state.create.mock.calls[0]);
    expect(state.create.mock.calls[1][2]).toEqual(request());
    expect(state.navigate).toHaveBeenCalledTimes(1);
  });

  it("recovers a lost response on another Fork click after remount without creating a new operation", async () => {
    state.create.mockRejectedValueOnce(new Error("lost response"));
    const first = renderHook(() => useForkConversation("source"));
    await act(async () => first.result.current.submit(request()));
    const saved = first.result.current.recoverable[0]; first.unmount();
    sessionStorage.setItem("lazymind:fork:u1", JSON.stringify([
      { ...saved, id: "previous-success", resultId: "previous-branch" }, saved,
    ]));
    const second = renderHook(() => useForkConversation("source"));
    expect(second.result.current.recoverable).toEqual([saved]);
    await act(async () => second.result.current.begin("h2"));
    expect(second.result.current.error).toBe("PENDING_FORK");
    expect(state.preview).not.toHaveBeenCalled();
    expect(second.result.current.recoverable[0].id).toBe(saved.id);
    state.create.mockResolvedValueOnce(created());
    await act(async () => second.result.current.begin("h1"));
    expect(state.create.mock.calls[1]).toEqual(state.create.mock.calls[0]);
    expect(state.preview).not.toHaveBeenCalled();
  });

  it("clears a late success without navigating after A to B to A", async () => {
    const pending = deferred<ForkResult>(); state.create.mockReturnValue(pending.promise);
    const { result, rerender } = renderHook(({ source }) => useForkConversation(source), { initialProps: { source: "A" } });
    act(() => { void result.current.submit(request()); });
    state.location = { key: "B-visit" }; rerender({ source: "B" });
    state.location = { key: "A-new-visit" }; rerender({ source: "A" });
    await act(async () => pending.resolve(created()));
    expect(state.navigate).not.toHaveBeenCalled();
    expect(result.current.recoverable).toHaveLength(0);
    expect(JSON.parse(sessionStorage.getItem("lazymind:fork:u1") || "[]")).toEqual([]);
    expect(state.create).toHaveBeenCalledTimes(1);
  });

  it("removes confirmed operations while retaining the in-memory duplicate guard", async () => {
    const { result } = renderHook(() => useForkConversation("source"));
    await act(async () => result.current.submit(request()));
    expect(result.current.recoverable).toHaveLength(0);
    expect(JSON.parse(sessionStorage.getItem("lazymind:fork:u1") || "[]")).toEqual([]);
    await act(async () => result.current.submit(request()));
    expect(state.create).toHaveBeenCalledTimes(1);
    expect(state.navigate).toHaveBeenLastCalledWith(expect.stringContaining("branch"));
  });

  it("clears a successful request after unmount so it is not restored as pending", async () => {
    const pending = deferred<ForkResult>(); state.create.mockReturnValue(pending.promise);
    const first = renderHook(() => useForkConversation("source"));
    act(() => { void first.result.current.submit(request()); });
    expect(JSON.parse(sessionStorage.getItem("lazymind:fork:u1") || "[]")).toHaveLength(1);
    first.unmount();
    await act(async () => pending.resolve(created()));
    expect(JSON.parse(sessionStorage.getItem("lazymind:fork:u1") || "[]")).toEqual([]);
    const second = renderHook(() => useForkConversation("source"));
    expect(second.result.current.recoverable).toHaveLength(0);
    expect(state.navigate).not.toHaveBeenCalled();
  });

  it("does not write a result after logout and login as the same user while unmounted", async () => {
    const pending = deferred<ForkResult>(); state.create.mockReturnValue(pending.promise);
    const { result, unmount } = renderHook(() => useForkConversation("source"));
    act(() => { void result.current.submit(request()); }); unmount();
    state.user = ""; window.dispatchEvent(new Event("fork-test-auth"));
    state.user = "u1"; window.dispatchEvent(new Event("fork-test-auth"));
    await act(async () => pending.resolve(created()));
    expect(sessionStorage.getItem("lazymind:fork:u1")).toBeNull();
    expect(state.navigate).not.toHaveBeenCalled();
  });

  it("a definite failure offers an inline retry and does not leave an unknown operation", async () => {
    state.create.mockRejectedValue({ response: { status: 409, data: { code: "SOURCE_CHANGED" } } });
    const { result } = renderHook(() => useForkConversation("source"));
    await act(async () => result.current.begin("h1"));
    expect(result.current.phase).toBe("failed");
    expect(result.current.error).toBe("SOURCE_CHANGED");
    expect(result.current.recoverable).toHaveLength(0);
    expect(state.navigate).not.toHaveBeenCalled();
    state.create.mockResolvedValueOnce(created());
    await act(async () => result.current.retry());
    expect(state.preview).toHaveBeenCalledTimes(2);
    expect(state.create.mock.calls[1][1]).not.toBe(state.create.mock.calls[0][1]);
    expect(state.navigate).toHaveBeenCalledTimes(1);
  });

  it("requests a replacement model inline only when the historical model is unavailable", async () => {
    state.preview.mockResolvedValue({ ...preview(), config_issues: [
      { field: "model", reason: "MODEL_UNAVAILABLE", suggested_value: null },
      { field: "thinking_depth", suggested_value: "high" },
    ] });
    const { result } = renderHook(() => useForkConversation("source"));
    await act(async () => result.current.begin("h1"));
    expect(state.create).not.toHaveBeenCalled();
    expect(result.current.phase).toBe("ready");
    expect(result.current.pending).toBe(false);
    await act(async () => result.current.selectModel("replacement"));
    expect(state.create.mock.calls[0][2]).toEqual({ ...request(), replacement_model: { mode: "fixed", model_id: "replacement" } });
    expect(state.navigate).toHaveBeenCalledTimes(1);
  });

  it("does not create a fork when the server rejects its source", async () => {
    state.preview.mockResolvedValue({ ...preview(), can_fork: false, reason_code: "SOURCE_NOT_SETTLED" });
    const { result } = renderHook(() => useForkConversation("source"));
    await act(async () => result.current.begin("h1"));
    expect(state.create).not.toHaveBeenCalled();
    expect(result.current.error).toBe("SOURCE_NOT_SETTLED");
    expect(result.current.pending).toBe(false);
  });

  it("retries after a preview failure without leaving a pending creation", async () => {
    state.preview.mockRejectedValueOnce(new Error("offline"));
    const { result } = renderHook(() => useForkConversation("source"));
    await act(async () => result.current.begin("h1"));
    expect(result.current.phase).toBe("preview_error");
    expect(result.current.pending).toBe(false);
    expect(result.current.recoverable).toHaveLength(0);
    await act(async () => result.current.retry());
    await waitFor(() => expect(state.navigate).toHaveBeenCalledTimes(1));
  });
});
