import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRef } from "react";
import MentionEditor, { type MentionEditorRef } from "./MentionEditor";

type ScrollablePrototype = typeof HTMLElement.prototype & {
  scrollTo?: (...args: unknown[]) => void;
};

const scrollablePrototype = HTMLElement.prototype as ScrollablePrototype;
const originalScrollTo = scrollablePrototype.scrollTo;

const mocks = vi.hoisted(() => ({
  listSkillAssetsPage: vi.fn(),
  listToolAssetsPage: vi.fn(),
  listDatasets: vi.fn(),
  listPrompts: vi.fn(),
  listConversations: vi.fn(),
  axiosGet: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/memory/skillApi", () => ({
  listSkillAssetsPage: mocks.listSkillAssetsPage,
}));

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssetsPage: mocks.listToolAssetsPage,
}));

vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: { get: mocks.axiosGet },
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: mocks.listConversations,
  }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: mocks.listDatasets,
  }),
  PromptServiceApi: () => ({
    listPrompts: mocks.listPrompts,
  }),
}));

describe("MentionEditor", () => {
  it('restores a saved mention chip with its resource identity', () => {
    const onMentionsChange = vi.fn();
    const mention = { mention_id: 'm1', type: 'skill' as const, resource_id: 'test-skill', display_name: '测试技能', start: 0, end: 4 };
    render(<MentionEditor value="测试技能 请总结" initialMentions={[mention]} placeholder="message"
      onChange={vi.fn()} onMentionsChange={onMentionsChange} onPaste={vi.fn()}
      onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const chip = screen.getByText('测试技能');
    expect(chip).toHaveAttribute('data-resource-id', 'test-skill');
    expect(chip).toHaveAttribute('contenteditable', 'false');
    expect(onMentionsChange).toHaveBeenLastCalledWith([mention]);
  });
  it('keeps saved labels as text and discards stale mention offsets', () => {
    const label = '<img src=x onerror=alert(1)>';
    const onMentionsChange = vi.fn();
    const mention = { mention_id: 'safe', type: 'skill' as const, resource_id: 'test-skill', display_name: label, start: 0, end: label.length };
    const { container } = render(<MentionEditor value={label} initialMentions={[mention, { ...mention, mention_id: 'stale', start: 50, end: 55 }]}
      placeholder="message" onChange={vi.fn()} onMentionsChange={onMentionsChange} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByRole('textbox')).toHaveTextContent(label);
    expect(onMentionsChange).toHaveBeenLastCalledWith([mention]);
  });

  it.each([false, true])('downgrades forbidden knowledge-base drafts while keeping other mentions (runtime=%s)', (runtime) => {
    const knowledge = { mention_id: 'kb', type: 'knowledge_base' as const, resource_id: 'test-kb', display_name: '知识库', start: 0, end: 3 };
    const tool = { mention_id: 'tool', type: 'tool' as const, resource_id: 'test-tool', display_name: '工具', start: 4, end: 6 };
    const onMentionsChange = vi.fn();
    const props = { value: '知识库 工具 请总结', initialMentions: [knowledge, tool], placeholder: 'message',
      onChange: vi.fn(), onMentionsChange, onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
    const view = render(<MentionEditor {...props} allowKnowledgeBaseSelection={runtime} />);
    if (runtime) {
      expect(view.container.querySelector('[data-resource-id="test-kb"]')).not.toBeNull();
      view.rerender(<MentionEditor {...props} allowKnowledgeBaseSelection={false} />);
    }
    expect(view.container.querySelector('[data-resource-id="test-kb"]')).toBeNull();
    expect(view.container.querySelector('[data-resource-id="test-tool"]')).not.toBeNull();
    expect(screen.getByRole('textbox').textContent?.replace(/\u200b/g, '')).toBe(props.value);
    expect(onMentionsChange).toHaveBeenLastCalledWith([tool]);
  });

  it('replaces generated text without retaining mentions even when its text is unchanged', () => {
    const ref = createRef<MentionEditorRef>();
    const mention = { mention_id: 'tool', type: 'tool' as const, resource_id: 'test-tool', display_name: '工具', start: 0, end: 2 };
    const onMentionsChange = vi.fn();
    const value = '工具 请总结';
    const view = render(<MentionEditor ref={ref} value={value} initialMentions={[mention]} placeholder="message"
      onChange={vi.fn()} onMentionsChange={onMentionsChange} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    ref.current!.setPlainText(value);
    expect(view.container.querySelector('.chat-mention-chip')).toBeNull();
    expect(screen.getByRole('textbox')).toHaveTextContent(value);
    expect(ref.current!.getMentions()).toEqual([]);
    expect(onMentionsChange).toHaveBeenLastCalledWith([]);
  });

  beforeEach(() => {
    Object.defineProperty(scrollablePrototype, "scrollTo", {
      configurable: true,
      value: vi.fn(),
    });

    mocks.listSkillAssetsPage.mockReset();
    mocks.listToolAssetsPage.mockReset();
    mocks.listDatasets.mockReset();
    mocks.listPrompts.mockReset();
    mocks.listConversations.mockReset();
    mocks.axiosGet.mockReset();

    mocks.listSkillAssetsPage.mockResolvedValue({ records: [] });
    mocks.listToolAssetsPage.mockResolvedValue({ records: [] });
    mocks.listDatasets.mockResolvedValue({ data: { datasets: [] } });
    mocks.listPrompts.mockResolvedValue({ data: { prompts: [] } });
    mocks.listConversations.mockResolvedValue({ data: { conversations: [] } });
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [] } });
  });

  afterEach(() => {
    window.getSelection()?.removeAllRanges();
    if (originalScrollTo) {
      Object.defineProperty(scrollablePrototype, "scrollTo", {
        configurable: true,
        value: originalScrollTo,
      });
    } else {
      Reflect.deleteProperty(scrollablePrototype, "scrollTo");
    }
    vi.restoreAllMocks();
  });

  it('omits knowledge bases when the composer does not allow selecting them', async () => {
    render(<MentionEditor value="" placeholder="message" allowKnowledgeBaseSelection={false}
      onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()}
      onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    await waitFor(() => expect(mocks.listSkillAssetsPage).toHaveBeenCalled());
    expect(mocks.listDatasets).not.toHaveBeenCalled();
    const editor = screen.getByRole('textbox');
    editor.textContent = '@kb:p2-side-chat';
    const range = document.createRange();
    range.setStart(editor.firstChild!, editor.textContent.length);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);
    expect(await screen.findByText('chat.mentionNoResults')).toBeVisible();
    expect(screen.queryByText('chat.mentionKnowledgeBase')).not.toBeInTheDocument();
    expect(mocks.listDatasets).not.toHaveBeenCalled();
  });

  it.each(['@', '@skill:', '@workflow:', '@tool:', '@kb:', '@chat:', '@prompt:'])(
    'keeps %s as plain text without loading resources when mentions are unavailable',
    (text) => {
      const onSend = vi.fn();
      render(<MentionEditor value="" placeholder="message" allowMentions={false}
        onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()}
        onSend={onSend} onCompositionChange={vi.fn()} />);
      const editor = screen.getByRole('textbox');
      editor.textContent = text;
      const range = document.createRange();
      range.setStart(editor.firstChild!, text.length);
      range.collapse(true);
      window.getSelection()?.removeAllRanges();
      window.getSelection()?.addRange(range);
      fireEvent.input(editor);
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(editor).toHaveTextContent(text);
      for (const load of Object.values(mocks)) expect(load).not.toHaveBeenCalled();
      fireEvent.keyDown(editor, { key: 'Enter' });
      expect(onSend).toHaveBeenCalledOnce();
    },
  );

  it('preserves saved labels without restoring executable mentions in side chat', () => {
    const onMentionsChange = vi.fn();
    const mention = { mention_id: 'old-skill', type: 'skill' as const, resource_id: 'test-skill', display_name: '测试技能', start: 0, end: 4 };
    const props = { value: '测试技能 请总结', initialMentions: [mention], placeholder: 'message',
      onChange: vi.fn(), onMentionsChange, onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
    const view = render(<MentionEditor {...props} />);
    expect(view.container.querySelector('.chat-mention-chip')).not.toBeNull();
    view.rerender(<MentionEditor {...props} allowMentions={false} />);
    expect(view.container.querySelector('.chat-mention-chip')).toBeNull();
    expect(screen.getByRole('textbox')).toHaveTextContent(props.value);
    expect(onMentionsChange).toHaveBeenLastCalledWith([]);
  });

  it("reloads skills after a previously cached empty list", async () => {
    render(
      <MentionEditor
        value=""
        placeholder="message"
        onChange={vi.fn()}
        onMentionsChange={vi.fn()}
        onPaste={vi.fn()}
        onSend={vi.fn()}
        onCompositionChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mocks.listSkillAssetsPage).toHaveBeenCalledTimes(1);
    });

    mocks.listSkillAssetsPage.mockResolvedValue({
      records: [
        {
          id: "skill-new",
          name: "新增技能",
          description: "刚刚添加的技能",
        },
      ],
    });

    const editor = screen.getByRole("textbox");
    editor.textContent = "@skill:";
    const range = document.createRange();
    const textNode = editor.firstChild;
    if (!textNode) {
      throw new Error("Mention editor did not create a text node");
    }
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: "新增技能" })).toBeInTheDocument();
    expect(mocks.listSkillAssetsPage).toHaveBeenCalledTimes(2);
  });

  it("reloads workflows so newly published workflows are shown", async () => {
    render(
      <MentionEditor
        value=""
        placeholder="message"
        onChange={vi.fn()}
        onMentionsChange={vi.fn()}
        onPaste={vi.fn()}
        onSend={vi.fn()}
        onCompositionChange={vi.fn()}
      />,
    );

    await waitFor(() => expect(mocks.axiosGet).toHaveBeenCalledTimes(1));
    mocks.axiosGet.mockResolvedValue({
      data: {
        workflows: [{
          workflow_ref: "user:user-1:ppt-workflow-copy",
          workflow_id: "ppt-workflow-copy",
          name: "AI PPT 规划 副本",
          description: "",
        }],
      },
    });

    const editor = screen.getByRole("textbox");
    editor.textContent = "@workflow:";
    const textNode = editor.firstChild;
    if (!textNode) throw new Error("Mention editor did not create a text node");
    const range = document.createRange();
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: "AI PPT 规划 副本" })).toBeInTheDocument();
    expect(mocks.axiosGet).toHaveBeenCalledTimes(2);
  });

  it("resets the mention menu scroll when the query changes", async () => {
    mocks.listSkillAssetsPage.mockResolvedValue({
      records: [{ id: "skill-find", name: "find-skill-skillhub" }],
    });

    render(
      <MentionEditor
        value=""
        placeholder="message"
        onChange={vi.fn()}
        onMentionsChange={vi.fn()}
        onPaste={vi.fn()}
        onSend={vi.fn()}
        onCompositionChange={vi.fn()}
      />,
    );

    const editor = screen.getByRole("textbox");
    editor.textContent = "@find";
    let textNode = editor.firstChild;
    if (!textNode) throw new Error("Mention editor did not create a text node");
    let range = document.createRange();
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: "find-skill-skillhub" })).toBeInTheDocument();
    await waitFor(() => expect(scrollablePrototype.scrollTo).toHaveBeenCalledWith({ top: 0 }));
    vi.mocked(scrollablePrototype.scrollTo).mockClear();

    editor.textContent = "@find-skill";
    textNode = editor.firstChild;
    if (!textNode) throw new Error("Mention editor did not create a text node");
    range = document.createRange();
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    await waitFor(() => expect(scrollablePrototype.scrollTo).toHaveBeenCalledWith({ top: 0 }));
  });

  it("renders each mention group as soon as that group finishes loading", async () => {
    mocks.listSkillAssetsPage.mockResolvedValue({
      records: [{ id: "skill-find", name: "find-skill-skillhub" }],
    });
    mocks.listConversations.mockReturnValue(new Promise(() => undefined));

    render(
      <MentionEditor
        value=""
        placeholder="message"
        onChange={vi.fn()}
        onMentionsChange={vi.fn()}
        onPaste={vi.fn()}
        onSend={vi.fn()}
        onCompositionChange={vi.fn()}
      />,
    );

    const editor = screen.getByRole("textbox");
    editor.textContent = "@find";
    const textNode = editor.firstChild;
    if (!textNode) throw new Error("Mention editor did not create a text node");
    const range = document.createRange();
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: "find-skill-skillhub" })).toBeInTheDocument();
  });

describe("mention text boundaries", () => {
  it.each([
    ["workflow", "Research Workflow"],
    ["skill", "Search Skill"],
    ["knowledge_base", "Project Knowledge"],
    ["tool", "Local Tool"],
    ["conversation", "Earlier Chat"],
  ])("separates %s labels from text sent to the backend", (type, name) => {
    const onChange = vi.fn();
    const onMentionsChange = vi.fn();
    render(<MentionEditor value="" placeholder="message" onChange={onChange} onMentionsChange={onMentionsChange}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole("textbox");
    const chip = document.createElement("span");
    chip.contentEditable = "false";
    chip.dataset.mentionId = "fixture";
    chip.dataset.mentionType = type;
    chip.dataset.resourceId = "fixture-resource";
    chip.dataset.displayName = name;
    chip.textContent = name;
    editor.append(chip, document.createTextNode("\u200bhttps://example.test/document"));
    fireEvent.input(editor);
    expect(onChange).toHaveBeenLastCalledWith(name + " https://example.test/document");
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({
      type, resource_id: "fixture-resource", display_name: name, start: 0, end: name.length,
    })]);
  });
  it("preserves ordinary text without mention chips", () => {
    const onChange = vi.fn();
    render(<MentionEditor value="" placeholder="message" onChange={onChange} onMentionsChange={vi.fn()}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole("textbox");
    const plain = "Please read obsidian://open?vault=Fixture&file=Note";
    editor.textContent = plain;
    fireEvent.input(editor);
    expect(onChange).toHaveBeenLastCalledWith(plain);
  });
  it.each(["", "\u200b", " ", "\n"])("keeps an Obsidian link separate after a chip with separator %j", separator => {
    const onChange = vi.fn();
    const onMentionsChange = vi.fn();
    render(<MentionEditor value="" placeholder="message" onChange={onChange} onMentionsChange={onMentionsChange}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole("textbox");
    const locator = "obsidian://open?vault=Fixture%20Vault&file=Note";
    editor.innerHTML = '<span contenteditable="false" data-mention-id="workflow" data-mention-type="workflow" data-resource-id="writer-workflow" data-display-name="AI Writer">AI Writer</span>';
    editor.append(document.createTextNode(separator + locator));
    fireEvent.input(editor);
    const expected = "AI Writer" + (separator === "\n" ? "\n" : " ") + locator;
    expect(onChange).toHaveBeenLastCalledWith(expected);
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ start: 0, end: 9, display_name: "AI Writer" })]);
  });
  it("keeps offsets correct for adjacent chips and nested text", () => {
    const onChange = vi.fn();
    const onMentionsChange = vi.fn();
    render(<MentionEditor value="" placeholder="message" onChange={onChange} onMentionsChange={onMentionsChange}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole("textbox");
    editor.innerHTML = '<span data-mention-id="one" data-mention-type="workflow" data-resource-id="one" data-display-name="AI Writer">AI Writer</span><span data-mention-id="two" data-mention-type="skill" data-resource-id="two" data-display-name="Skill">Skill</span><b>obsidian://open?vault=Fixture&amp;file=Note</b>';
    fireEvent.input(editor);
    expect(onChange).toHaveBeenLastCalledWith("AI Writer Skill obsidian://open?vault=Fixture&file=Note");
    const mentions = onMentionsChange.mock.calls[onMentionsChange.mock.calls.length - 1][0];
    expect(mentions.map(({ start, end }: { start: number; end: number }) => [start, end])).toEqual([[0, 9], [10, 15]]);
  });
});

});
