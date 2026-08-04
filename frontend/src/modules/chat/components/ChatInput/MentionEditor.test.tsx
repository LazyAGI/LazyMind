import { fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import MentionEditor, { type MentionEditorRef } from "./MentionEditor";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("antd", () => ({ message: { warning: vi.fn() } }));

vi.mock("@ant-design/icons", () => ({
  AppstoreOutlined: () => null,
  BookOutlined: () => null,
  BulbOutlined: () => null,
  CommentOutlined: () => null,
  DatabaseOutlined: () => null,
  ThunderboltOutlined: () => null,
}));

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
    conversationServiceListConversations: vi.fn().mockResolvedValue({
      data: { conversations: [] },
    }),
  }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: vi.fn().mockResolvedValue({
      data: { datasets: [] },
    }),
  }),
  PromptServiceApi: () => ({
    listPrompts: vi.fn().mockResolvedValue({ data: { prompts: [] } }),
  }),
}));

function baseProps(overrides: Partial<React.ComponentProps<typeof MentionEditor>> = {}) {
  return {
    value: "",
    placeholder: "placeholder",
    onChange: vi.fn(),
    onMentionsChange: vi.fn(),
    onPaste: vi.fn(),
    onSend: vi.fn(),
    onCompositionChange: vi.fn(),
    ...overrides,
  };
}

function setCaretAfterAt(editor: HTMLElement) {
  editor.textContent = "@";
  const range = document.createRange();
  range.setStart(editor.firstChild!, 1);
  range.collapse(true);
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
}

describe("MentionEditor menu placement", () => {
  it("limits the upward menu to the space above the editor", () => {
    Object.defineProperty(window, "innerHeight", {
      configurable: true,
      value: 800,
    });
    render(<MentionEditor {...baseProps()} />);
    const editor = screen.getByRole("textbox");
    Object.defineProperty(editor, "getBoundingClientRect", {
      configurable: true,
      value: () => ({
        top: 320,
        right: 720,
        bottom: 368,
        left: 0,
        width: 720,
        height: 48,
        x: 0,
        y: 320,
        toJSON: () => ({}),
      }),
    });

    setCaretAfterAt(editor);
    fireEvent.input(editor);

    const menu = screen.getByRole("listbox");
    expect(menu.style.height).toBe("304px");
    expect(menu).not.toHaveClass("is-below");
  });

  it("opens below the editor when there is not enough space above", () => {
    Object.defineProperty(window, "innerHeight", {
      configurable: true,
      value: 800,
    });
    render(<MentionEditor {...baseProps()} />);
    const editor = screen.getByRole("textbox");
    Object.defineProperty(editor, "getBoundingClientRect", {
      configurable: true,
      value: () => ({
        top: 80,
        right: 720,
        bottom: 128,
        left: 0,
        width: 720,
        height: 48,
        x: 0,
        y: 80,
        toJSON: () => ({}),
      }),
    });

    setCaretAfterAt(editor);
    fireEvent.input(editor);

    const menu = screen.getByRole("listbox");
    expect(menu.style.height).toBe("420px");
    expect(menu).toHaveClass("is-below");
  });
});

describe("MentionEditor empty state", () => {
  it("keeps the placeholder outside the editable value after a residual br", () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <MentionEditor {...baseProps({ value: "hello", onChange })} />,
    );
    const editor = screen.getByRole("textbox", { name: "placeholder" });

    expect(editor).toHaveAttribute("data-empty", "false");
    editor.innerHTML = "<br>";
    fireEvent.input(editor);
    expect(onChange).toHaveBeenCalledWith("");

    rerender(<MentionEditor {...baseProps({ value: "" })} />);
    editor.focus();

    expect(editor).toHaveAttribute("data-empty", "true");
    expect(editor).toHaveFocus();
  });
});

describe("MentionEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders a contenteditable textbox with the placeholder attribute", () => {
    const { getByRole } = render(
      <MentionEditor {...baseProps({ placeholder: "chat.inputPlaceholder" })} />,
    );
    const editor = getByRole("textbox");
    expect(editor).toHaveAttribute("contenteditable", "true");
    expect(editor).toHaveAttribute("data-placeholder", "chat.inputPlaceholder");
    expect(editor).toHaveAttribute("data-empty", "true");
  });

  it("marks the editor as non-editable when disabled", () => {
    const { getByRole } = render(
      <MentionEditor {...baseProps({ disabled: true })} />,
    );
    expect(getByRole("textbox")).toHaveAttribute("contenteditable", "false");
  });

  it("emits changes on input", () => {
    const onChange = vi.fn();
    const { getByRole } = render(
      <MentionEditor {...baseProps({ onChange })} />,
    );
    const editor = getByRole("textbox");
    editor.textContent = "hello world";
    fireEvent.input(editor);
    expect(onChange).toHaveBeenCalledWith("hello world");
  });

  it("calls onSend and prevents default when Enter is pressed without shift", () => {
    const onSend = vi.fn();
    const { getByRole } = render(
      <MentionEditor {...baseProps({ onSend })} />,
    );
    const editor = getByRole("textbox");
    const event = fireEvent.keyDown(editor, {
      key: "Enter",
      shiftKey: false,
      cancelable: true,
    });
    expect(onSend).toHaveBeenCalled();
    expect(event).toBe(false);
  });

  it("does not call onSend when Shift+Enter is pressed", () => {
    const onSend = vi.fn();
    const { getByRole } = render(
      <MentionEditor {...baseProps({ onSend })} />,
    );
    const editor = getByRole("textbox");
    fireEvent.keyDown(editor, { key: "Enter", shiftKey: true });
    expect(onSend).not.toHaveBeenCalled();
  });

  it("forwards paste events to onPaste", () => {
    const onPaste = vi.fn();
    const { getByRole } = render(
      <MentionEditor {...baseProps({ onPaste })} />,
    );
    fireEvent.paste(getByRole("textbox"));
    expect(onPaste).toHaveBeenCalled();
  });

  it("notifies composition start and end", () => {
    const onCompositionChange = vi.fn();
    const { getByRole } = render(
      <MentionEditor {...baseProps({ onCompositionChange })} />,
    );
    const editor = getByRole("textbox");
    fireEvent.compositionStart(editor);
    expect(onCompositionChange).toHaveBeenCalledWith(true);
    fireEvent.compositionEnd(editor);
    expect(onCompositionChange).toHaveBeenCalledWith(false);
  });

  it("syncs external value changes into the editor content", () => {
    const { getByRole, rerender } = render(
      <MentionEditor {...baseProps({ value: "" })} />,
    );
    rerender(<MentionEditor {...baseProps({ value: "synced text" })} />);
    expect(getByRole("textbox").textContent).toBe("synced text");
  });

  it("exposes focus and getMentions via the imperative ref", () => {
    const ref = createRef<MentionEditorRef>();
    render(<MentionEditor ref={ref} {...baseProps()} />);
    expect(ref.current).not.toBeNull();
    expect(typeof ref.current?.focus).toBe("function");
    expect(ref.current?.getMentions()).toEqual([]);
  });
});
