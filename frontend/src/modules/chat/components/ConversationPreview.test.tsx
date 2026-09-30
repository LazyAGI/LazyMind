import { ConfigProvider } from "antd";
import type { ReactNode } from "react";
import { act, fireEvent, render as renderUI, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ConversationPreview from "./ConversationPreview";
import { useConversationRunningStore } from "../store/conversationRunning";
const detail = vi.hoisted(() => vi.fn());
vi.mock("../utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceGetConversationDetail: detail }) }));

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { time?: string; group?: string; parent?: string }) => values?.time ? `Updated ${values.time}` : values?.group ? `Group: ${values.group}` : values?.parent ? `Branch from: ${values.parent}` : key }) }));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));

const render = (ui: ReactNode) => renderUI(ui, { wrapper: ({ children }) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider> });

afterEach(() => { act(() => useConversationRunningStore.setState({ entries: {} })); });
beforeEach(() => detail.mockReset());

describe("ConversationPreview", () => {
  it("shows group membership and loads the branch origin for grouped conversations", async () => {
    detail.mockResolvedValue({ data: { conversation: { fork_origin: { source_conversation_id: "source", source_title_snapshot: "Original conversation" } } } });
    render(<ConversationPreview conversationId="branch" title="Branch" groupName="My group"><button>Grouped row</button></ConversationPreview>);
    expect(detail).not.toHaveBeenCalled();
    fireEvent.mouseOver(screen.getByRole("button", { name: "Grouped row" }));
    const card = await screen.findByRole("region", { name: "chat.conversationPreview" });
    await waitFor(() => expect(within(card).getByText("Branch from: Original conversation")).toBeVisible());
    expect(within(card).getByText("Group: My group")).toBeVisible();
    expect(detail).toHaveBeenCalledWith({ conversation: "branch" }, { signal: expect.any(AbortSignal) });
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("region")).not.toBeInTheDocument());
    fireEvent.mouseOver(screen.getByRole("button", { name: "Grouped row" }));
    await screen.findByRole("region");
    expect(detail).toHaveBeenCalledTimes(1);
  });

  it("cancels a pending grouped preview and ignores a late response for another row", async () => {
    let resolve!: (value: unknown) => void;
    detail.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    const view = render(<ConversationPreview conversationId="old" title="Old" groupName="Group"><button>Row</button></ConversationPreview>);
    fireEvent.mouseOver(screen.getByRole("button", { name: "Row" }));
    await waitFor(() => expect(detail).toHaveBeenCalledTimes(1));
    const signal = detail.mock.calls[0][1].signal as AbortSignal;
    detail.mockResolvedValue({ data: { conversation: {} } });
    view.rerender(<ConversationPreview conversationId="new" title="New" groupName="Group"><button>Row</button></ConversationPreview>);
    await waitFor(() => expect(detail).toHaveBeenCalledTimes(2));
    expect(signal.aborted).toBe(true);
    await act(async () => resolve({ data: { conversation: { fork_origin: { source_conversation_id: "source", source_title_snapshot: "Stale source" } } } }));
    expect(screen.queryByText("Branch from: Stale source")).not.toBeInTheDocument();
    view.unmount();
  });

  it("previews the hovered row, summary, timestamp and current task status without navigating", async () => {
    useConversationRunningStore.setState({ entries: { task: { status: "running", confirmedAt: Date.now() } } });
    const select = vi.fn();
    render(<ConversationPreview conversationId="task" title="My task" summary="A saved summary" updateTime="2026-09-17T10:30:00" isTask>
      <button onClick={select}>Row</button>
    </ConversationPreview>);
    fireEvent.mouseOver(screen.getByRole("button", { name: "Row" }));
    const card = await screen.findByRole("region", { name: "chat.conversationPreview" });
    await waitFor(() => expect(within(card).getByText("My task")).toBeVisible());
    expect(within(card).getByText("A saved summary")).toBeVisible();
    expect(within(card).getByText("Updated 2026/09/17 10:30")).toBeVisible();
    expect(within(card).getByText("chat.conversationPreviewWork")).toBeVisible();
    expect(within(card).getByText("chat.conversationRunning")).toBeVisible();
    fireEvent.click(card);
    expect(select).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Row" }));
    expect(select).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.queryByRole("region")).not.toBeInTheDocument());
  });

  it("omits absent summaries and invalid dates and closes on Escape from keyboard preview", async () => {
    render(<ConversationPreview conversationId="empty" title="Name only" summary="  " updateTime="invalid">
      <button>Row</button>
    </ConversationPreview>);
    fireEvent.focus(screen.getByRole("button"));
    const card = await screen.findByRole("region");
    expect(card).toHaveTextContent("Name only");
    expect(card.querySelector(".record-preview-summary")).toBeNull();
    expect(card.querySelector("time")).toBeNull();
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("region")).not.toBeInTheDocument());
  });

  it("updates when hovering another row and keeps the card open while entering its area", async () => {
    render(<>{["First", "Second"].map(title => <ConversationPreview key={title} conversationId={title} title={title} summary={`${title} summary`}>
      <button>{title}</button>
    </ConversationPreview>)}</>);
    fireEvent.mouseOver(screen.getByRole("button", { name: "First" }));
    const first = await screen.findByRole("region");
    fireEvent.mouseOut(screen.getByRole("button", { name: "First" }));
    fireEvent.mouseOver(first.closest(".ant-popover")!);
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 200)); });
    expect(first).toBeVisible();
    fireEvent.mouseOut(first.closest(".ant-popover")!);
    fireEvent.mouseOver(screen.getByRole("button", { name: "Second" }));
    await screen.findByText("Second summary");
    await waitFor(() => expect(screen.queryByText("First summary")).not.toBeInTheDocument());
    fireEvent.mouseOut(screen.getByRole("button", { name: "Second" }));
    await waitFor(() => expect(screen.queryByRole("region")).not.toBeInTheDocument());
  });

  it("hides stale previews when resizing and does not open while renaming", async () => {
    const { rerender } = render(<ConversationPreview conversationId="a" title="Name"><button>Row</button></ConversationPreview>);
    fireEvent.focus(screen.getByRole("button"));
    await screen.findByRole("region");
    fireEvent.resize(window);
    await waitFor(() => expect(screen.queryByRole("region")).not.toBeInTheDocument());
    rerender(<ConversationPreview conversationId="a" title="Name" disabled><button>Row</button></ConversationPreview>);
    fireEvent.focus(screen.getByRole("button"));
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
  });
});
