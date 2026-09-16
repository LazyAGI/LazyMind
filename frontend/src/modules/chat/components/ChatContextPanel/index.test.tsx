import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import ChatContextPanel from "./index";

let sideProps: any;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("../SideChatPanel", () => ({ default: (props: any) => {
  sideProps = props;
  const [text, setText] = useState("");
  return <input aria-label="side draft" value={text} onChange={e => setText(e.target.value)} />;
} }));
vi.mock("../AssistantMessage", () => ({ ChatSourcePanel: () => <div>source list</div> }));

describe("shared chat context panel", () => {
  const props = { open: true, visible: true, parentConversationId: "parent", source: { selectedText: "quote" }, onClose: vi.fn() };

  it("preserves the draft while switching references, collapsing, and reopening", () => {
    const view = render(<ChatContextPanel sideChat={props} visible />);
    fireEvent.change(screen.getByLabelText("side draft"), { target: { value: "unfinished" } });
    view.rerender(<ChatContextPanel sideChat={props} visible sourceRequest={{ sources: [{} as any], origin: "main", summary: "answer" }} />);
    expect(screen.getByRole("tab", { name: /chat.references/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("side draft")).not.toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "chat.contextPanel.collapse" }));
    expect(screen.queryByRole("button", { name: "chat.contextPanel.reopen" })).not.toBeInTheDocument();
    view.rerender(<ChatContextPanel sideChat={props} visible resumeRequest={1} />);
    fireEvent.click(screen.getByRole("tab", { name: "chat.sideChat.title" }));
    expect(screen.getByLabelText("side draft")).toHaveValue("unfinished");
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
});
