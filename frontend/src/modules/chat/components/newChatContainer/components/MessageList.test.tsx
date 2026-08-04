import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen, fireEvent } from "@/test/testUtils";
import MessageList from "./MessageList";
import { RoleTypes } from "@/modules/chat/constants/common";

vi.mock("../../AssistantMessage", () => ({
  default: ({ item }: { item: { delta?: string } }) => (
    <div data-testid="assistant-message">{item.delta}</div>
  ),
}));

function baseProps(overrides: Partial<React.ComponentProps<typeof MessageList>> = {}) {
  return {
    messageList: [],
    sendMessage: vi.fn(),
    regenerate: vi.fn(),
    stopGeneration: vi.fn(),
    renderText: (item: { delta?: string }) => <div data-testid="rendered-text">{item.delta}</div>,
    updateAssistantMessage: vi.fn(),
    ...overrides,
  };
}

describe("MessageList", () => {
  it("renders the initial card when there are no messages yet", () => {
    renderWithProviders(
      <MessageList {...baseProps({ initialCard: <div data-testid="initial-card" /> })} />,
    );
    expect(screen.getByTestId("initial-card")).toBeInTheDocument();
  });

  it("renders a user message and an assistant message from the list", () => {
    renderWithProviders(
      <MessageList
        {...baseProps({
          messageList: [
            { role: RoleTypes.USER, delta: "hi there", create_time: "2024-01-01T00:00:00Z" },
            { role: RoleTypes.ASSISTANT, delta: "hello back" },
          ],
        })}
      />,
    );
    expect(screen.getByTestId("rendered-text")).toHaveTextContent("hi there");
    expect(screen.getByTestId("assistant-message")).toHaveTextContent("hello back");
  });

  it("copies the user message when the copy button is clicked", () => {
    const onCopyUserMessage = vi.fn();
    renderWithProviders(
      <MessageList
        {...baseProps({
          messageList: [{ role: RoleTypes.USER, delta: "copy me" }],
          onCopyUserMessage,
        })}
      />,
    );
    fireEvent.click(document.querySelector(".anticon-copy")!.closest("button")!);
    expect(onCopyUserMessage).toHaveBeenCalledWith(
      expect.objectContaining({ delta: "copy me" }),
    );
  });

  it("shows the edit button only for the last user message and starts editing on click", () => {
    const onStartEditUserMessage = vi.fn();
    renderWithProviders(
      <MessageList
        {...baseProps({
          messageList: [
            { role: RoleTypes.USER, delta: "first" },
            { role: RoleTypes.ASSISTANT, delta: "reply" },
            { role: RoleTypes.USER, delta: "second" },
          ],
          onStartEditUserMessage,
        })}
      />,
    );
    const editButtons = document.querySelectorAll(".anticon-edit");
    expect(editButtons).toHaveLength(1);
    fireEvent.click(editButtons[0].closest("button")!);
    expect(onStartEditUserMessage).toHaveBeenCalledWith(
      expect.objectContaining({ delta: "second" }),
      2,
    );
  });

  it("renders the edit textarea and resends via Enter when editing the message", () => {
    const onResendEditedUserMessage = vi.fn();
    renderWithProviders(
      <MessageList
        {...baseProps({
          messageList: [{ role: RoleTypes.USER, delta: "editable" }],
          editingUserMessageIndex: 0,
          editingUserMessageText: "editable",
          onResendEditedUserMessage,
        })}
      />,
    );
    const textarea = screen.getByDisplayValue("editable");
    fireEvent.keyDown(textarea, { key: "Enter", shiftKey: false });
    expect(onResendEditedUserMessage).toHaveBeenCalledWith(0, "editable");
  });

  it("shows the citation preview icon when cite messages are present", () => {
    renderWithProviders(
      <MessageList
        {...baseProps({
          messageList: [
            { role: RoleTypes.USER, delta: "quoted", cite_messages: ["cited excerpt"] },
          ],
        })}
      />,
    );
    expect(document.querySelector(".chat-user-citation-preview")).not.toBeNull();
  });
});
