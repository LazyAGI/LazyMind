import { ConfigProvider } from "antd";
import type { ReactNode } from "react";
import { act, fireEvent, render as renderUI, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ConversationPreview from "./ConversationPreview";
import { useConversationRunningStore } from "../store/conversationRunning";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { time?: string }) => values?.time ? `Updated ${values.time}` : key }) }));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));

const render = (ui: ReactNode) => renderUI(ui, { wrapper: ({ children }) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider> });

afterEach(() => { act(() => useConversationRunningStore.setState({ entries: {} })); });

describe("ConversationPreview", () => {
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
