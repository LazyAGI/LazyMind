import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AgentAppsAuth } from "@/components/auth";
import { useSourceImportState } from "./state";
import * as api from "./api";

vi.mock("./api", () => ({ listSourceImports: vi.fn(), inspectSources: vi.fn() }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: vi.fn() } }));
const context = { conversation_id: "chat", history_id: "answer" };
const pending: api.ImportItem = {
  item_id: "item", batch_id: "batch", title: "Paper", paper_key: "paper",
  target_dataset_id: "library", target_name: "Library", created_library: false,
  status: "pending", stage: "download", retryable: false,
};
beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({ userId: "alice" } as never);
  vi.mocked(api.listSourceImports).mockResolvedValue([pending]);
  vi.mocked(api.inspectSources).mockResolvedValue([]);
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.clearAllMocks(); });

it("stops polling when the last result view closes", async () => {
  const { rerender } = renderHook(({ open }) => useSourceImportState(context, open), { initialProps: { open: true } });
  await act(async () => {});
  expect(api.listSourceImports).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(api.listSourceImports).toHaveBeenCalledTimes(2);
  rerender({ open: false });
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(api.listSourceImports).toHaveBeenCalledTimes(2);
});

it("does not reuse another user's loaded source state", async () => {
  const { result, rerender } = renderHook(() => useSourceImportState(context, false));
  await act(async () => { await result.current.refresh(); });
  expect(result.current.items).toEqual([pending]);
  vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({ userId: "bob" } as never);
  rerender();
  expect(result.current.items).toEqual([]);
  expect(api.listSourceImports).toHaveBeenCalledTimes(1);
  await act(async () => { await result.current.refresh(); });
  expect(api.listSourceImports).toHaveBeenCalledTimes(2);
});
