import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, message } from "antd";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import zhCN from "@/i18n/locales/zh-CN";
import { CHAT_CONVERSATION_LIST_REFRESH_EVENT } from "@/modules/chat/constants/chat";
import { showBatchArchiveFeedback } from "./BatchArchiveFeedback";

const mocks = vi.hoisted(() => ({ unarchive: vi.fn(), groupsChanged: vi.fn() }));
vi.mock("@/modules/settings/recoveryApi", () => ({ unarchiveConversation: mocks.unarchive }));
vi.mock("@/modules/chat/conversationOrganizer/api", () => ({ emitConversationGroupsChanged: mocks.groupsChanged }));
vi.mock("react-i18next", async (importOriginal) => ({ ...await importOriginal<typeof import("react-i18next")>(), useTranslation: () => ({
  t: (key: string, values?: Record<string, unknown>) => {
    const label = key.split(".").reduce<unknown>((result, name) => (result as Record<string, unknown>)?.[name], zhCN);
    return String(label ?? key).replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(values?.[name] ?? ""));
  },
}) }));

const version = "2026-10-19T08:00:00.123456Z";
const receipt = (id: string) => ({ conversation_id: id, archived_at: version });
const flush = async () => { await act(async () => { await Promise.resolve(); }); };

beforeAll(() => ConfigProvider.config({ holderRender: (children) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider> }));
afterAll(() => ConfigProvider.config({ holderRender: undefined }));
beforeEach(() => { vi.clearAllMocks(); mocks.unarchive.mockResolvedValue(undefined); });
afterEach(async () => {
  act(() => message.destroy());
  if (vi.isFakeTimers()) await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
  vi.useRealTimers();
  await flush();
  await waitFor(() => expect(document.querySelector(".batch-archive-feedback")).toBeNull());
});

describe("batch archive undo feedback", () => {
  it("survives viewing archived, guards duplicate clicks, and refreshes lists after versioned undo", async () => {
    let finish!: () => void;
    mocks.unarchive.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    const onViewArchived = vi.fn();
    const refreshed = vi.fn();
    window.addEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, refreshed);
    act(() => showBatchArchiveFeedback({ count: 2, receipts: [receipt("a"), receipt("b")], onViewArchived }));
    fireEvent.click(await screen.findByRole("button", { name: "查看已归档" }));
    expect(onViewArchived).toHaveBeenCalledOnce();
    const undo = screen.getByRole("button", { name: "撤销" });
    fireEvent.click(undo);
    fireEvent.click(undo);
    expect(mocks.unarchive).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "关闭" })).toBeDisabled();
    finish();
    await waitFor(() => expect(mocks.unarchive).toHaveBeenCalledTimes(2));
    expect(mocks.unarchive.mock.calls).toEqual([["a", version], ["b", version]]);
    await screen.findAllByText("已撤销本次归档，恢复 2 条会话");
    expect(refreshed).toHaveBeenCalledOnce();
    expect(mocks.groupsChanged).toHaveBeenCalledOnce();
    window.removeEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, refreshed);
  });

  it("keeps failures visible and retries only the remaining ids", async () => {
    mocks.unarchive.mockImplementation(async (id: string) => { if (id === "b") throw new Error("offline"); });
    act(() => showBatchArchiveFeedback({ count: 2, receipts: [receipt("a"), receipt("b")], onViewArchived: vi.fn() }));
    fireEvent.click(await screen.findByRole("button", { name: "撤销" }));
    await screen.findByText("已恢复 1 条会话，1 条撤销失败。");
    mocks.unarchive.mockResolvedValue(undefined);
    fireEvent.click(screen.getByRole("button", { name: "重试撤销" }));
    await screen.findAllByText("已撤销本次归档，恢复 2 条会话");
    expect(mocks.unarchive.mock.calls).toEqual([["a", version], ["b", version], ["b", version]]);
  });

  it("does not retry newer archive versions or conversations no longer available", async () => {
    mocks.unarchive.mockImplementation(async (id: string) => {
      throw { response: { status: id === "newer" ? 409 : 404, data: { code: id === "newer" ? 2004815 : 2000004 } } };
    });
    act(() => showBatchArchiveFeedback({ count: 2, receipts: [receipt("newer"), receipt("trashed")], onViewArchived: vi.fn() }));
    fireEvent.click(await screen.findByRole("button", { name: "撤销" }));
    await screen.findByText("2 条会话状态已变化，请在已归档列表中查看。");
    expect(screen.queryByRole("button", { name: "重试撤销" })).not.toBeInTheDocument();
    expect(mocks.unarchive).toHaveBeenCalledTimes(2);
  });

  it("isolates successive batches and offers no unsafe undo for missing versions", async () => {
    act(() => {
      showBatchArchiveFeedback({ count: 1, receipts: [receipt("first")], onViewArchived: vi.fn() });
      showBatchArchiveFeedback({ count: 2, receipts: [receipt("second")], onViewArchived: vi.fn() });
    });
    await screen.findByText("已归档 2 条会话");
    expect(screen.getAllByRole("button", { name: "撤销" })).toHaveLength(1);
    const second = screen.getByText("已归档 2 条会话").closest(".batch-archive-feedback")! as HTMLElement;
    expect(within(second).queryByRole("button", { name: "撤销" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(mocks.unarchive).toHaveBeenCalledWith("first", version));
    expect(screen.getByText("已归档 2 条会话")).toBeInTheDocument();
  });

  it("expires after eight seconds, pauses during interaction, and stays open on failure", async () => {
    vi.useFakeTimers();
    act(() => showBatchArchiveFeedback({ count: 1, receipts: [receipt("a")], onViewArchived: vi.fn() }));
    await flush();
    act(() => vi.advanceTimersByTime(1));
    await flush();
    const notice = screen.getByText("已归档 1 条会话").closest(".batch-archive-feedback")!;
    fireEvent.mouseEnter(notice);
    act(() => vi.advanceTimersByTime(9_000));
    expect(screen.getByRole("button", { name: "撤销" })).toBeInTheDocument();
    mocks.unarchive.mockRejectedValueOnce(new Error("offline"));
    fireEvent.click(screen.getByRole("button", { name: "撤销" }));
    await flush();
    fireEvent.mouseLeave(notice);
    act(() => vi.advanceTimersByTime(9_000));
    expect(screen.getByRole("button", { name: "重试撤销" })).toBeInTheDocument();
    act(() => message.destroy());
    act(() => showBatchArchiveFeedback({ count: 1, receipts: [], onViewArchived: vi.fn() }));
    await flush();
    act(() => vi.advanceTimersByTime(1));
    await flush();
    act(() => vi.advanceTimersByTime(8_000));
    await flush();
    act(() => vi.advanceTimersByTime(1_000));
    expect(screen.queryByText("已归档 1 条会话")).not.toBeInTheDocument();
  });
});
