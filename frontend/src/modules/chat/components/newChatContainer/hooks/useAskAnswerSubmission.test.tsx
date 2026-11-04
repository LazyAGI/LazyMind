import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { SendMessageParams } from "../../ChatInput/types";
import { useAskAnswerSubmission } from "./useAskAnswerSubmission";

afterEach(cleanup);

function fixture() {
  const params: SendMessageParams = {
    text: "输出格式：PPTX",
    ask_answers_structured: { ask_id: "format-card", questions: [
      { id: "format", text: "输出格式", type: "single", choices: ["PPTX"], custom_choices: ["PPTX"],
        answer: { type: "single", value: "PPTX" } },
    ] },
    files: [Object.assign(new File(["template"], "template.pptx"), { uid: "template", uri: "/template.pptx" })] as SendMessageParams["files"],
  };
  const activeStreamRef = { current: false };
  const messageListRef = { current: [{ ask_pending: { ask_id: "format-card" }, ask_answered: false }] as
    Array<{ ask_pending: { ask_id: string }; ask_answered: boolean; run_status?: string }> };
  const sendNow = vi.fn().mockResolvedValue(true);
  const hook = renderHook(({ busy, conversationId }) => useAskAnswerSubmission({
    busy, conversationId, activeStreamRef, messageListRef, sendNow,
  }), { initialProps: { busy: true, conversationId: "A" } });
  return { ...hook, params, activeStreamRef, messageListRef, sendNow };
}

it("waits for the old run and submits the complete answer and attachment only once", async () => {
  const f = fixture();
  let complete!: (sent: boolean) => void;
  f.sendNow.mockImplementation(() => new Promise<boolean>((resolve) => { complete = resolve; }));
  let request!: Promise<boolean>;
  let duplicate!: Promise<boolean>;
  act(() => {
    request = f.result.current.submit(f.params);
    duplicate = f.result.current.submit(f.params);
  });
  expect(duplicate).toBe(request);
  expect(f.result.current.pending).toBe(true);
  expect(f.sendNow).not.toHaveBeenCalled();
  f.rerender({ busy: false, conversationId: "A" });
  expect(f.sendNow).toHaveBeenCalledTimes(1);
  expect(f.sendNow.mock.calls[0][0]).toBe(f.params);
  expect(f.sendNow.mock.calls[0][0].files[0]).toBe(f.params.files![0]);
  expect(f.result.current.pending).toBe(true);
  await act(async () => { complete(true); expect(await request).toBe(true); });
  expect(f.result.current.pending).toBe(false);
});

it("also waits for the active stream ref to be cleared", async () => {
  const f = fixture();
  f.activeStreamRef.current = true;
  let request!: Promise<boolean>;
  act(() => { request = f.result.current.submit(f.params); });
  f.rerender({ busy: false, conversationId: "A" });
  expect(f.sendNow).not.toHaveBeenCalled();
  await act(async () => {
    f.activeStreamRef.current = false;
    f.rerender({ busy: false, conversationId: "A" });
  });
  expect(await request).toBe(true);
  expect(f.sendNow).toHaveBeenCalledOnce();
});

it.each(["conversation", "card", "answered", "unmount", "cancelled"])("does not send a pending answer after %s changes", async (change) => {
  const f = fixture();
  let request!: Promise<boolean>;
  act(() => { request = f.result.current.submit(f.params); });
  await act(async () => {
    if (change === "card") f.messageListRef.current = [{ ask_pending: { ask_id: "replacement" }, ask_answered: false }];
    if (change === "answered") f.messageListRef.current = [{ ask_pending: { ask_id: "format-card" }, ask_answered: true }];
    if (change === "cancelled") f.messageListRef.current = [{ ask_pending: { ask_id: "format-card" }, ask_answered: false, run_status: "cancelled" }];
    if (change === "unmount") f.unmount();
    else f.rerender({ busy: true, conversationId: change === "conversation" ? "B" : "A" });
  });
  expect(await request).toBe(false);
  expect(f.sendNow).not.toHaveBeenCalled();
});

it("submits an idle answer normally and allows retry after a refused send", async () => {
  const f = fixture();
  f.rerender({ busy: false, conversationId: "A" });
  f.sendNow.mockResolvedValueOnce(false);
  let request!: Promise<boolean>;
  await act(async () => { request = f.result.current.submit(f.params); });
  expect(await request).toBe(false);
  expect(f.result.current.pending).toBe(false);
  await act(async () => { request = f.result.current.submit(f.params); });
  expect(await request).toBe(true);
  expect(f.sendNow).toHaveBeenCalledTimes(2);
});

it("preserves submission errors for the card's existing error handling", async () => {
  const f = fixture();
  const error = new Error("submission failed");
  f.sendNow.mockRejectedValue(error);
  let failure!: Promise<unknown>;
  act(() => { failure = f.result.current.submit(f.params).catch((cause) => cause); });
  await act(async () => { f.rerender({ busy: false, conversationId: "A" }); });
  expect(await failure).toBe(error);
  expect(f.result.current.pending).toBe(false);
});
