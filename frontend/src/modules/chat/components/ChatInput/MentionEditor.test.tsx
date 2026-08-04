import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, fireEvent } from "@/test/testUtils";
import MentionEditor, { type MentionEditorRef } from "./MentionEditor";

vi.mock("@/components/request", () => ({
  axiosInstance: { get: vi.fn().mockResolvedValue({ data: { plugins: [] } }) },
  BASE_URL: "",
}));

vi.mock("@/modules/memory/skillApi", () => ({
  listSkillAssetsPage: vi.fn().mockResolvedValue({ records: [] }),
}));

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssetsPage: vi.fn().mockResolvedValue({ records: [] }),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: vi.fn().mockResolvedValue({ data: { conversations: [] } }),
  }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: vi.fn().mockResolvedValue({ data: { datasets: [] } }),
  }),
  PromptServiceApi: () => ({
    listPrompts: vi.fn().mockResolvedValue({ data: { prompts: [] } }),
  }),
}));

function baseProps(overrides: Partial<React.ComponentProps<typeof MentionEditor>> = {}) {
  return {
    value: "",
    placeholder: "chat.inputPlaceholder",
    onChange: vi.fn(),
    onMentionsChange: vi.fn(),
    onPaste: vi.fn(),
    onSend: vi.fn(),
    onCompositionChange: vi.fn(),
    ...overrides,
  };
}

describe("MentionEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders a contenteditable textbox with the placeholder attribute", () => {
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps()} />);
    const editor = getByRole("textbox");
    expect(editor).toHaveAttribute("contenteditable", "true");
    expect(editor).toHaveAttribute("data-placeholder", "chat.inputPlaceholder");
    expect(editor).toHaveAttribute("data-empty", "true");
  });

  it("keeps the empty state after the browser leaves a br node when text is cleared", () => {
    const onChange = vi.fn();
    const { getByRole, rerender } = renderWithProviders(
      <MentionEditor {...baseProps({ value: "hello", onChange })} />,
    );
    const editor = getByRole("textbox");

    expect(editor).toHaveAttribute("data-empty", "false");
    editor.innerHTML = "<br>";
    fireEvent.input(editor);
    expect(onChange).toHaveBeenCalledWith("");

    rerender(<MentionEditor {...baseProps({ value: "" })} />);
    expect(editor).toHaveAttribute("data-empty", "true");
  });

  it("marks the editor as non-editable when disabled", () => {
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps({ disabled: true })} />);
    expect(getByRole("textbox")).toHaveAttribute("contenteditable", "false");
  });

  it("emits changes on input", () => {
    const onChange = vi.fn();
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps({ onChange })} />);
    const editor = getByRole("textbox");
    editor.textContent = "hello world";
    fireEvent.input(editor);
    expect(onChange).toHaveBeenCalledWith("hello world");
  });

  it("calls onSend and prevents default when Enter is pressed without shift", () => {
    const onSend = vi.fn();
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps({ onSend })} />);
    const editor = getByRole("textbox");
    const event = fireEvent.keyDown(editor, { key: "Enter", shiftKey: false, cancelable: true });
    expect(onSend).toHaveBeenCalled();
    expect(event).toBe(false);
  });

  it("does not call onSend when Shift+Enter is pressed", () => {
    const onSend = vi.fn();
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps({ onSend })} />);
    const editor = getByRole("textbox");
    fireEvent.keyDown(editor, { key: "Enter", shiftKey: true });
    expect(onSend).not.toHaveBeenCalled();
  });

  it("forwards paste events to onPaste", () => {
    const onPaste = vi.fn();
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps({ onPaste })} />);
    fireEvent.paste(getByRole("textbox"));
    expect(onPaste).toHaveBeenCalled();
  });

  it("notifies composition start and end", () => {
    const onCompositionChange = vi.fn();
    const { getByRole } = renderWithProviders(<MentionEditor {...baseProps({ onCompositionChange })} />);
    const editor = getByRole("textbox");
    fireEvent.compositionStart(editor);
    expect(onCompositionChange).toHaveBeenCalledWith(true);
    fireEvent.compositionEnd(editor);
    expect(onCompositionChange).toHaveBeenCalledWith(false);
  });

  it("syncs external value changes into the editor content", () => {
    const { getByRole, rerender } = renderWithProviders(<MentionEditor {...baseProps({ value: "" })} />);
    rerender(<MentionEditor {...baseProps({ value: "synced text" })} />);
    expect(getByRole("textbox").textContent).toBe("synced text");
  });

  it("exposes focus and getMentions via the imperative ref", () => {
    const ref = createRef<MentionEditorRef>();
    renderWithProviders(<MentionEditor ref={ref} {...baseProps()} />);
    expect(ref.current).not.toBeNull();
    expect(typeof ref.current?.focus).toBe("function");
    expect(ref.current?.getMentions()).toEqual([]);
  });
});
