import { createRef, forwardRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, fireEvent, within } from "@/test/testUtils";
import ChatInput, { type ChatInputImperativeProps } from "./index";

vi.mock("../ImageUpload", async () => {
  const actual = await vi.importActual<typeof import("../ImageUpload")>("../ImageUpload");
  return {
    ...actual,
    default: forwardRef(() => <div data-testid="image-upload-stub" />),
  };
});

vi.mock("./MentionEditor", () => ({
  default: forwardRef(({ value, onChange, onSend, disabled }: any, ref: any) => (
    <textarea
      ref={ref}
      data-testid="mention-editor-stub"
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") onSend();
      }}
    />
  )),
}));

vi.mock("./ChatConfigModal", () => ({
  default: () => <div data-testid="chat-config-modal-stub" />,
}));

vi.mock("./ContextUsageButton", () => ({
  default: () => <div data-testid="context-usage-stub" />,
}));

vi.mock("../ChatSelector", () => ({
  default: forwardRef(() => <div data-testid="chat-selector-stub" />),
}));

vi.mock("../PromptModal", () => ({
  default: forwardRef(() => <div data-testid="prompt-modal-stub" />),
}));

vi.mock("../BatchChat", () => ({
  default: forwardRef(() => <div data-testid="batch-chat-stub" />),
}));

vi.mock("../ShowChatFileList", () => ({
  default: () => <div data-testid="show-file-list-stub" />,
}));

describe("ChatInput", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  function baseProps(overrides: Partial<React.ComponentProps<typeof ChatInput>> = {}) {
    return {
      value: "",
      onChange: vi.fn(),
      onSend: vi.fn(),
      isChatContent: true,
      ...overrides,
    };
  }

  it("renders the mention editor and send button", () => {
    renderWithProviders(<ChatInput {...baseProps()} />);
    expect(screen.getByTestId("mention-editor-stub")).toBeInTheDocument();
    expect(screen.getByLabelText("chat.send")).toBeInTheDocument();
  });

  it("disables the send button while the input is empty", () => {
    renderWithProviders(<ChatInput {...baseProps({ value: "" })} />);
    expect(screen.getByLabelText("chat.send")).toBeDisabled();
  });

  it("enables the send button once text is present and calls onSend", () => {
    const onSend = vi.fn();
    renderWithProviders(<ChatInput {...baseProps({ value: "hello there", onSend })} />);
    const sendButton = screen.getByLabelText("chat.send");
    expect(sendButton).not.toBeDisabled();
    fireEvent.click(sendButton);
    expect(onSend).toHaveBeenCalledWith(
      expect.objectContaining({ text: "hello there" }),
    );
  });

  it("shows the stop button and calls onStopGeneration while streaming", () => {
    const onStopGeneration = vi.fn();
    renderWithProviders(
      <ChatInput {...baseProps({ isStreaming: true, onStopGeneration })} />,
    );
    const stopButton = screen.getByLabelText("chat.stopGenerate");
    fireEvent.click(stopButton);
    expect(onStopGeneration).toHaveBeenCalled();
  });

  it("shows the disabled notice with the provided reason", () => {
    renderWithProviders(
      <ChatInput {...baseProps({ disabled: true, disabledReason: "chat.embeddingNotReady" })} />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("chat.embeddingNotReady");
  });

  it("renders cite message previews and clears them via the close button", () => {
    const onClearCiteMessage = vi.fn();
    renderWithProviders(
      <ChatInput
        {...baseProps({ citeMessages: ["quoted text"], onClearCiteMessage })}
      />,
    );
    expect(screen.getByText("quoted text")).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("chat.clearCitation"));
    expect(onClearCiteMessage).toHaveBeenCalled();
  });

  it("shows the secondary case category only when options are configured", () => {
    const onSecondaryChange = vi.fn();
    const selection = {
      primaryValue: "product-design",
      primaryLabel: "产品设计与PRD生成",
      primaryAriaLabel: "一级分类",
      secondaryValue: "full",
      secondaryAriaLabel: "二级分类",
      secondaryOptions: [
        { value: "full", label: "完整功能" },
        { value: "outline", label: "快速生成" },
      ],
      onSecondaryChange,
    };

    const { rerender } = renderWithProviders(
      <ChatInput {...baseProps({ showcaseSelection: selection })} />,
    );
    const showcaseSelection = screen.getByTestId("showcase-selection");
    expect(within(showcaseSelection).getAllByRole("combobox")).toHaveLength(2);
    expect(showcaseSelection).toHaveTextContent("完整功能");
    expect(
      showcaseSelection.querySelector(".chat-showcase-primary-control"),
    ).toBeInTheDocument();
    expect(
      showcaseSelection.querySelector(".chat-showcase-scene-control"),
    ).toBeInTheDocument();

    rerender(
      <ChatInput
        {...baseProps({
          showcaseSelection: { ...selection, secondaryOptions: [] },
        })}
      />,
    );
    expect(within(screen.getByTestId("showcase-selection")).getAllByRole("combobox")).toHaveLength(1);
    expect(
      screen
        .getByTestId("showcase-selection")
        .querySelector(".chat-showcase-scene-control"),
    ).not.toBeInTheDocument();
  });

  it("exposes focus via the imperative ref", () => {
    const ref = createRef<ChatInputImperativeProps>();
    renderWithProviders(<ChatInput ref={ref} {...baseProps()} />);
    expect(ref.current).not.toBeNull();
    expect(typeof ref.current?.focus).toBe("function");
  });
});
