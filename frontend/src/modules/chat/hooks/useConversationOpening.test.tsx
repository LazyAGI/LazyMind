import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useConversationOpening } from "./useConversationOpening";
import { conversationOpeningState } from "../utils/request";
import { CHAT_CONVERSATION_ACTIVITY_EVENT } from "../constants/chat";

vi.mock("../utils/request", () => ({ conversationOpeningState: vi.fn() }));
const state = (revision: number, pending: number, status = "done") => ({
  batch: {status, scan_complete: true, scanned: 1}, revision, pending, completed: 1, failed: 0, skipped: 0, unprocessed: pending,
});

beforeEach(() => {
  vi.useFakeTimers();
  Object.defineProperty(document, "hidden", {configurable: true, value: false});
  vi.mocked(conversationOpeningState).mockReset();
});
afterEach(() => vi.useRealTimers());

it("refreshes changed titles and stops after pending work completes", async () => {
  vi.mocked(conversationOpeningState).mockResolvedValueOnce(state(0,1,"running")).mockResolvedValueOnce(state(1,0));
  const refresh = vi.fn();
  const {unmount} = renderHook(() => useConversationOpening("u1",refresh));
  await act(async () => {});
  expect(conversationOpeningState).toHaveBeenCalledWith("start",expect.any(AbortSignal));
  await act(async () => {await vi.advanceTimersByTimeAsync(5000);});
  expect(refresh).toHaveBeenCalledTimes(1);
  await act(async () => {await vi.advanceTimersByTimeAsync(15000);});
  expect(conversationOpeningState).toHaveBeenCalledTimes(2);
  unmount();
});

it("pauses while hidden and resumes for realtime work even with paused backfill", async () => {
  vi.mocked(conversationOpeningState).mockResolvedValue(state(0,1,"paused"));
  const refresh=vi.fn();
  const {unmount}=renderHook(() => useConversationOpening("u1",refresh));
  await act(async () => {});
  act(() => {
    Object.defineProperty(document,"hidden",{configurable:true,value:true});
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await act(async () => {await vi.advanceTimersByTimeAsync(15000);});
  expect(conversationOpeningState).toHaveBeenCalledTimes(1);
  await act(async () => {
    Object.defineProperty(document,"hidden",{configurable:true,value:false});
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await act(async () => {await vi.advanceTimersByTimeAsync(5000);});
  expect(conversationOpeningState).toHaveBeenCalledTimes(3);
  unmount();
});

it("checks again after a new conversation finishes, without restarting the batch", async () => {
  vi.mocked(conversationOpeningState).mockResolvedValue(state(1,0));
  const refresh=vi.fn();const {unmount}=renderHook(() => useConversationOpening("u1",refresh));
  await act(async () => {});
  expect(refresh).toHaveBeenCalledTimes(1);
  act(() => {window.dispatchEvent(new Event(CHAT_CONVERSATION_ACTIVITY_EVENT));});
  await act(async () => {await vi.advanceTimersByTimeAsync(5000);});
  expect(conversationOpeningState).toHaveBeenLastCalledWith(undefined,expect.any(AbortSignal));
  unmount();
});
