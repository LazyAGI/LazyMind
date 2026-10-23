import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  reorder: vi.fn(),
  steer: vi.fn(),
  retry: vi.fn(),
  claimNext: vi.fn(),
  complete: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  info: vi.fn(),
  warning: vi.fn(),
}));

vi.mock("../../../utils/request", () => ({
  ConversationFollowUpApi: {
    list: mocks.list,
    create: mocks.create,
    update: mocks.update,
    remove: mocks.remove,
    reorder: mocks.reorder,
    steer: mocks.steer,
    retry: mocks.retry,
    claimNext: mocks.claimNext,
    complete: mocks.complete,
  },
}));
vi.mock("antd", () => ({
  message: {
    success: mocks.success,
    error: mocks.error,
    info: mocks.info,
    warning: mocks.warning,
  },
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

import type { ConversationFollowUp } from "../../../utils/request";
import { useConversationFollowUps } from "./useConversationFollowUps";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function item(conversationId: string, overrides: Partial<ConversationFollowUp> = {}): ConversationFollowUp {
  return {
    id: `${conversationId}-follow-up`,
    conversation_id: conversationId,
    content: { text: "queued" },
    text: "queued",
    mode: "queued",
    status: "pending",
    sort_order: 1024,
    revision: 1,
    created_at: "2026-10-22T00:00:00Z",
    updated_at: "2026-10-22T00:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.list.mockResolvedValue({ items: [], active_run_id: "", supports_steering: false });
  mocks.claimNext.mockResolvedValue({ item: null });
  mocks.complete.mockResolvedValue(undefined);
});
afterEach(cleanup);

describe("useConversationFollowUps conversation isolation", () => {
  it("keeps an accepted steer in the visible conversation", async () => {
    const pending = item("A");
    const accepted = item("A", {
      mode: "steer",
      status: "accepted",
      target_run_id: "run-A",
      target_history_id: "history-A",
      accepted_at: "2026-10-22T00:00:01Z",
    });
    mocks.list.mockResolvedValue({
      items: [pending],
      active_run_id: "run-A",
      supports_steering: true,
    });
    mocks.steer.mockResolvedValue({ item: accepted, delivery_mode: "steer" });
    const hook = renderHook(() => useConversationFollowUps({
      conversationId: "A",
      isStreaming: true,
      sendNow: vi.fn().mockResolvedValue(true),
    }));

    await waitFor(() => expect(hook.result.current.activeRunId).toBe("run-A"));
    await act(async () => hook.result.current.steer(pending));

    expect(hook.result.current.items[0]).toMatchObject({ status: "accepted" });
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.info).not.toHaveBeenCalled();
  });

  it("ignores a stale queue response after switching conversations", async () => {
    const listA = deferred<{ items: ConversationFollowUp[]; active_run_id: string; supports_steering: boolean }>();
    mocks.list.mockImplementation((conversationId: string) => conversationId === "A"
      ? listA.promise
      : Promise.resolve({ items: [item("B")], active_run_id: "run-B", supports_steering: true }));
    const sendNow = vi.fn().mockResolvedValue(true);
    const hook = renderHook(
      ({ conversationId }) => useConversationFollowUps({ conversationId, isStreaming: true, sendNow }),
      { initialProps: { conversationId: "A" } },
    );

    hook.rerender({ conversationId: "B" });
    await waitFor(() => expect(hook.result.current.items.map((entry) => entry.conversation_id)).toEqual(["B"]));
    await act(async () => listA.resolve({ items: [item("A")], active_run_id: "run-A", supports_steering: true }));

    expect(hook.result.current.items.map((entry) => entry.conversation_id)).toEqual(["B"]);
    expect(hook.result.current.activeRunId).toBe("run-B");
  });

  it("releases an old claim instead of sending it into the newly selected conversation", async () => {
    const claimedA = deferred<{ item: ConversationFollowUp | null }>();
    mocks.list.mockImplementation((conversationId: string) => Promise.resolve({
      items: conversationId === "A" ? [item("A")] : [],
      active_run_id: "",
      supports_steering: false,
    }));
    mocks.claimNext.mockImplementation((conversationId: string) => (
      conversationId === "A" ? claimedA.promise : Promise.resolve({ item: null })
    ));
    const sendNow = vi.fn().mockResolvedValue(true);
    const hook = renderHook(
      ({ conversationId }) => useConversationFollowUps({ conversationId, isStreaming: false, sendNow }),
      { initialProps: { conversationId: "A" } },
    );

    await waitFor(() => expect(mocks.claimNext).toHaveBeenCalledWith("A"));
    hook.rerender({ conversationId: "B" });
    await act(async () => claimedA.resolve({ item: item("A", { status: "dispatching" }) }));

    await waitFor(() => expect(mocks.complete).toHaveBeenCalledWith(
      "A",
      "A-follow-up",
      false,
      "conversation changed before dispatch",
      true,
    ));
    expect(sendNow).not.toHaveBeenCalled();
  });

  it("restores a failed steer to an ordinary pending queue when retrying after the run ended", async () => {
    const failed = item("A", { mode: "steer", status: "failed" });
    const queued = item("A", { mode: "queued", status: "pending", revision: 2 });
    mocks.list.mockResolvedValue({ items: [failed], active_run_id: "", supports_steering: false });
    mocks.retry.mockResolvedValue(queued);
    const hook = renderHook(() => useConversationFollowUps({
      conversationId: "A",
      isStreaming: false,
      sendNow: vi.fn().mockResolvedValue(true),
    }));

    await waitFor(() => expect(hook.result.current.items[0]?.status).toBe("failed"));
    await act(async () => hook.result.current.retry(failed));

    expect(hook.result.current.items[0]).toMatchObject({ mode: "queued", status: "pending" });
  });

  it("silently leaves guidance in the ordinary queue when the active run already ended", async () => {
    const pending = item("A");
    const downgraded = item("A", { mode: "queued", status: "pending", revision: 2 });
    mocks.list.mockResolvedValue({
      items: [pending],
      active_run_id: "run-A",
      supports_steering: true,
    });
    mocks.steer.mockResolvedValue({
      item: downgraded,
      delivery_mode: "queued",
      reason: "active_run_finished",
    });
    const hook = renderHook(() => useConversationFollowUps({
      conversationId: "A",
      isStreaming: true,
      sendNow: vi.fn().mockResolvedValue(true),
    }));

    await waitFor(() => expect(hook.result.current.activeRunId).toBe("run-A"));
    await act(async () => hook.result.current.steer(pending));

    expect(hook.result.current.items[0]).toMatchObject({ mode: "queued", status: "pending" });
    expect(mocks.info).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });
});

describe("useConversationFollowUps enqueue idempotency", () => {
  it("queues against the resolved conversation id before the route prop catches up", async () => {
    const created = item("resolved-conversation");
    mocks.create.mockResolvedValue(created);
    const hook = renderHook(() => useConversationFollowUps({
      conversationId: "",
      isStreaming: false,
      sendNow: vi.fn().mockResolvedValue(true),
    }));

    let accepted = false;
    await act(async () => {
      accepted = await hook.result.current.enqueue(
        { text: "very early guidance" },
        "resolved-conversation",
      );
    });

    expect(accepted).toBe(true);
    expect(mocks.create).toHaveBeenCalledWith(
      "resolved-conversation",
      expect.objectContaining({
        text: "very early guidance",
        content: expect.objectContaining({ text: "very early guidance" }),
      }),
    );
  });

  it("coalesces repeated submissions of the same draft while creation is in flight", async () => {
    const creation = deferred<ConversationFollowUp>();
    mocks.create.mockReturnValue(creation.promise);
    const hook = renderHook(() => useConversationFollowUps({
      conversationId: "A",
      isStreaming: true,
      sendNow: vi.fn().mockResolvedValue(true),
    }));

    let first!: Promise<boolean>;
    let second!: Promise<boolean>;
    act(() => {
      first = hook.result.current.enqueue({ text: "same guidance", create_time: "first" });
      second = hook.result.current.enqueue({ text: "same guidance", create_time: "first" });
    });

    expect(mocks.create).toHaveBeenCalledTimes(1);
    await act(async () => {
      creation.resolve(item("A", { text: "same guidance" }));
      await Promise.all([first, second]);
    });
    await expect(first).resolves.toBe(true);
    await expect(second).resolves.toBe(true);
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("reuses the idempotency key when retrying the same draft after a failure", async () => {
    mocks.create
      .mockRejectedValueOnce(new Error("connection lost"))
      .mockResolvedValueOnce(item("A", { text: "retry guidance" }));
    const hook = renderHook(() => useConversationFollowUps({
      conversationId: "A",
      isStreaming: true,
      sendNow: vi.fn().mockResolvedValue(true),
    }));

    let firstResult = true;
    await act(async () => {
      firstResult = await hook.result.current.enqueue({ text: "retry guidance", create_time: "first" });
    });
    let secondResult = false;
    await act(async () => {
      secondResult = await hook.result.current.enqueue({ text: "retry guidance", create_time: "second" });
    });

    expect(firstResult).toBe(false);
    expect(secondResult).toBe(true);
    expect(mocks.create).toHaveBeenCalledTimes(2);
    expect(mocks.create.mock.calls[0][1].idempotency_key)
      .toBe(mocks.create.mock.calls[1][1].idempotency_key);
  });
});
