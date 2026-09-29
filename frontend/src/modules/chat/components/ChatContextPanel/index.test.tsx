import { act, fireEvent, render, screen } from "@testing-library/react";
import { forwardRef, useImperativeHandle, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import ChatContextPanel from "./index";

let sideProps: any;
const requestSideChatClose = vi.fn();
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("../SideChatPanel", () => ({ default: forwardRef(function MockSideChat(props: any, ref) {
  sideProps = props;
  useImperativeHandle(ref, () => ({ requestClose: requestSideChatClose }));
  const [text, setText] = useState("");
  return <input aria-label="side draft" value={text} onChange={e => setText(e.target.value)} />;
}) }));
vi.mock("../AssistantMessage", () => ({ ChatSourcePanel: () => <div>source list</div> }));

describe("shared chat context panel", () => {
  const props = { open: true, visible: true, parentConversationId: "parent", source: { selectedText: "quote" }, onClose: vi.fn() };

  it("keeps keyboard navigation on side chat until references are requested", () => {
    render(<ChatContextPanel sideChat={props} visible />);
    const sideTab = screen.getByRole("tab", { name: "chat.sideChat.title" });
    sideTab.focus();
    for (const key of ["ArrowRight", "ArrowLeft", "Home", "End"]) {
      fireEvent.keyDown(sideTab, { key });
      expect(sideTab).toHaveAttribute("aria-selected", "true");
      expect(sideTab).toHaveFocus();
      expect(screen.getByLabelText("side draft")).toBeVisible();
    }
    expect(screen.queryByRole("tab", { name: /chat.references/ })).not.toBeInTheDocument();
  });

  it("preserves the draft while switching references, hiding, and reopening", () => {
    const view = render(<ChatContextPanel sideChat={props} visible />);
    fireEvent.change(screen.getByLabelText("side draft"), { target: { value: "unfinished" } });
    view.rerender(<ChatContextPanel sideChat={props} visible sourceRequest={{ sources: [{} as any], origin: "main", summary: "answer" }} />);
    expect(screen.getByRole("tab", { name: /chat.references/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("side draft")).not.toBeVisible();
    view.rerender(<ChatContextPanel sideChat={props} visible={false} />);
    expect(screen.queryByRole("button", { name: "chat.contextPanel.reopen" })).not.toBeInTheDocument();
    view.rerender(<ChatContextPanel sideChat={props} visible resumeRequest={1} />);
    fireEvent.click(screen.getByRole("tab", { name: "chat.sideChat.title" }));
    expect(screen.getByLabelText("side draft")).toHaveValue("unfinished");
    expect(props.onClose).not.toHaveBeenCalled();
  });

  it("requests confirmation from the mounted side chat when closing references", () => {
    requestSideChatClose.mockClear();
    render(<ChatContextPanel sideChat={props} visible sourceRequest={{ sources: [{} as any], origin: "main" }} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.contextPanel.collapse" }));
    expect(requestSideChatClose).toHaveBeenCalledTimes(1);
    expect(sideProps.closeConfirmationVisible).toBe(true);
    expect(screen.getByText("source list")).toBeVisible();
    expect(props.onClose).not.toHaveBeenCalled();
  });

  it("announces background completion without switching away from sources", () => {
    render(<ChatContextPanel sideChat={props} visible sourceRequest={{ sources: [{} as any], origin: "main" }} />);
    act(() => sideProps.onStreamingChange(true));
    act(() => sideProps.onStreamingChange(false));
    expect(screen.getByRole("tab", { name: /chat.references/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("chat.contextPanel.newReply")).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("tab", { name: /chat.references/ }), { key: "ArrowLeft" });
    expect(screen.getByRole("tab", { name: "chat.sideChat.title" })).toHaveFocus();
    expect(screen.queryByLabelText("chat.contextPanel.newReply")).not.toBeInTheDocument();
  });

  it("keeps hidden conversation panels inaccessible and identifies sidechat sources", () => {
    const view = render(<ChatContextPanel sideChat={props} visible />);
    act(() => sideProps.onOpenSources([{}], "side answer"));
    expect(screen.getByText("chat.contextPanel.sideSources")).toBeVisible();
    expect(screen.getByText("side answer")).toBeVisible();
    view.rerender(<ChatContextPanel sideChat={props} visible={false} />);
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
  });

  it("closes references directly when there is no side chat and reopens on a new request", () => {
    const view = render(<ChatContextPanel visible sourceRequest={{ sources: [{} as any], origin: "main" }} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.contextPanel.collapse" }));
    expect(screen.getByText("source list")).not.toBeVisible();
    view.rerender(<ChatContextPanel visible sourceRequest={{ sources: [{} as any], origin: "main", summary: "another answer" }} />);
    expect(screen.getByText("source list")).toBeVisible();
    fireEvent.keyDown(screen.getByRole("complementary"), { key: "Escape" });
    expect(screen.getByText("source list")).not.toBeVisible();
  });
});
