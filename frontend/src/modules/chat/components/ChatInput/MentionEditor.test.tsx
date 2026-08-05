import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import MentionEditor from "./MentionEditor";

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

const baseProps = {
  value: "",
  placeholder: "placeholder",
  onChange: vi.fn(),
  onMentionsChange: vi.fn(),
  onPaste: vi.fn(),
  onSend: vi.fn(),
  onCompositionChange: vi.fn(),
};

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
    render(<MentionEditor {...baseProps} />);
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
    render(<MentionEditor {...baseProps} />);
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
      <MentionEditor {...baseProps} value="hello" onChange={onChange} />,
    );
    const editor = screen.getByRole("textbox", { name: "placeholder" });

    editor.innerHTML = "<br>";
    fireEvent.input(editor);
    expect(onChange).toHaveBeenCalledWith("");

    rerender(<MentionEditor {...baseProps} value="" />);
    editor.focus();

    expect(editor).toHaveAttribute("data-empty", "true");
    expect(editor).toHaveFocus();
  });
});
