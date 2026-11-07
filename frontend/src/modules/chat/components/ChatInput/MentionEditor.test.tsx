import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRef } from "react";
import { message } from "antd";
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
  listShowcaseCases: vi.fn(),
}));

vi.mock("@/modules/showcase/api", () => ({ listShowcaseCases: mocks.listShowcaseCases }));

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
  const enterQuery = (text: string) => {
    const editor = screen.getByRole('textbox');
    editor.textContent = text;
    const range = document.createRange();
    range.setStart(editor.firstChild!, text.length);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);
  };

  it.each(["/", "/workflow:"])("waits for consent before inserting a workflow selected through %s", async (query) => {
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: "builtin:writer", name: "Writer", enabled: true }] } });
    let confirm!: (allowed: boolean) => void;
    const beforeWorkflowSelect = vi.fn(() => new Promise<boolean>(resolve => { confirm = resolve; }));
    const onMentionsChange = vi.fn();
    const onSend = vi.fn();
    const { container } = render(<MentionEditor value="" placeholder="message" workflowDisabled
      beforeWorkflowSelect={beforeWorkflowSelect} onChange={vi.fn()} onMentionsChange={onMentionsChange}
      onPaste={vi.fn()} onSend={onSend} onCompositionChange={vi.fn()} />);
    enterQuery(query);
    const option = await screen.findByRole("option", { name: /Writer/ });
    expect(option).toHaveTextContent("Writer");
    expect(option).not.toHaveTextContent("chat.workflowSelectionDisabledLabel");
    const disabledIcon = screen.getByRole("img", { name: "chat.conversationConfigWorkflowDisabled" });
    expect(disabledIcon.closest(".chat-mention-group-title")).toHaveTextContent("chat.mentionWorkflow");
    expect(container.querySelectorAll(".chat-mention-workflow-disabled")).toHaveLength(1);
    fireEvent.mouseDown(option);
    expect(beforeWorkflowSelect).toHaveBeenCalledTimes(1);
    expect(container.querySelector(".chat-mention-chip")).toBeNull();
    expect(screen.getByRole("textbox")).toHaveTextContent(query);
    confirm(true);
    await waitFor(() => expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ type: "workflow", resource_id: "builtin:writer" })]));
    expect(onSend).not.toHaveBeenCalled();
  });

  it("preserves the input when workflow consent is declined", async () => {
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: "builtin:writer", name: "Writer", enabled: true }] } });
    const beforeWorkflowSelect = vi.fn().mockResolvedValue(false);
    const { container } = render(<MentionEditor value="" placeholder="message" beforeWorkflowSelect={beforeWorkflowSelect}
      onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery("/workflow:");
    await screen.findByRole("option", { name: "Writer" });
    expect(screen.queryByRole("img", { name: "chat.conversationConfigWorkflowDisabled" })).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "ArrowDown" });
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await waitFor(() => expect(beforeWorkflowSelect).toHaveBeenCalledTimes(1));
    expect(container.querySelector(".chat-mention-chip")).toBeNull();
    expect(screen.getByRole("textbox")).toHaveTextContent("/workflow:");
  });

  it.each(['cancel-mode', 'decline-workflow', 'confirm', 'change-conversation'] as const)(
    'checks workflow consent after mode confirmation: %s', async action => {
      mocks.listShowcaseCases.mockResolvedValue({ cases: [{ id: 'ppt', type: 'workflow', workflow_ref: 'builtin:ppt' }] });
      mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: 'builtin:ppt', name: 'PPT' }] } });
      let resolveConsent!: (allowed: boolean) => void;
      const beforeWorkflowSelect = vi.fn(() => new Promise<boolean>(resolve => { resolveConsent = resolve; }));
      const props = { value: '', placeholder: 'message', beforeWorkflowSelect, onEntryModeChange: vi.fn(),
        onChange: vi.fn(), onMentionsChange: vi.fn(), onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
      const view = render(<MentionEditor {...props} conversationId='original' />);
      enterQuery('Draft /');
      fireEvent.click(await screen.findByRole('option', { name: 'PPT' }));
      const dialog = await screen.findByRole('dialog');
      expect(beforeWorkflowSelect).not.toHaveBeenCalled();
      if (action === 'cancel-mode') {
        fireEvent.click(within(dialog).getByRole('button', { name: 'common.cancel' }));
        expect(beforeWorkflowSelect).not.toHaveBeenCalled();
      } else {
        fireEvent.click(within(dialog).getByRole('button', { name: 'chat.capabilityModeSwitchConfirm' }));
        expect(beforeWorkflowSelect).toHaveBeenCalledOnce();
        expect(props.onEntryModeChange).not.toHaveBeenCalled();
        expect(view.container.querySelector('.chat-mention-chip')).toBeNull();
        fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
        expect(props.onSend).not.toHaveBeenCalled();
        if (action === 'change-conversation') view.rerender(<MentionEditor {...props} conversationId='new' />);
        await act(async () => resolveConsent(action !== 'decline-workflow'));
      }
      if (action === 'confirm') {
        expect(props.onEntryModeChange).toHaveBeenCalledWith('work', expect.objectContaining({
          text: 'Draft PPT', featuredSelection: { caseId: 'ppt' },
        }));
      } else {
        expect(props.onEntryModeChange).not.toHaveBeenCalled();
        expect(view.container.querySelector('.chat-mention-chip')).toBeNull();
      }
    },
  );

  const scrollMenu = (scrollTop = 600) => {
    const menu = screen.getByRole('listbox');
    Object.defineProperties(menu, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 400 },
      scrollTop: { configurable: true, value: scrollTop, writable: true },
    });
    fireEvent.scroll(menu);
  };

  it.each(['Chosen', 'Chosen Continue writing'])('places the caret after the complete restored draft: %s', async value => {
    const ref = createRef<MentionEditorRef>();
    const onChange = vi.fn();
    const mention = { mention_id: 'chosen', type: 'workflow' as const, resource_id: 'builtin:chosen', display_name: 'Chosen', start: 0, end: 6 };
    render(<MentionEditor ref={ref} value={value} initialMentions={[mention]} placeholder='message'
      onChange={onChange} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole('textbox');
    const start = document.createRange();
    start.selectNodeContents(editor);
    start.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(start);
    act(() => ref.current?.focusAtEnd());
    await waitFor(() => {
      expect(editor).toHaveFocus();
      const caret = window.getSelection()?.getRangeAt(0);
      expect(caret?.collapsed).toBe(true);
      expect(caret?.startContainer).toBe(editor);
      expect(caret?.startOffset).toBe(editor.childNodes.length);
    });
    expect(ref.current?.getMentions()).toEqual([mention]);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('captures a draft without selected capability bindings while retaining other references and offsets', () => {
    const ref = createRef<MentionEditorRef>();
    const value = 'Keep Chosen with Knowledge and Other';
    const mentions = [
      { mention_id: 'chosen', type: 'workflow' as const, resource_id: 'builtin:chosen', display_name: 'Chosen', start: 5, end: 11 },
      { mention_id: 'knowledge', type: 'knowledge_base' as const, resource_id: 'kb', display_name: 'Knowledge', start: 17, end: 26 },
      { mention_id: 'other', type: 'skill' as const, resource_id: 'other', display_name: 'Other', start: 31, end: 36 },
    ];
    render(<MentionEditor ref={ref} value={value} initialMentions={mentions} placeholder='message'
      onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    expect(ref.current?.getDraftWithoutMentions([mentions[0]])).toEqual({
      text: 'Keep  with Knowledge and Other',
      mentions: [{ ...mentions[1], start: 11, end: 20 }, { ...mentions[2], start: 25, end: 30 }],
    });
    expect(ref.current?.getMentions()).toEqual(mentions);
  });

  it.each([
    [false, "workflow", "workflow", "work"],
    [true, "skill", "chat", "chat"],
    [false, "skill", "work", "work"],
    [true, "workflow", "chat", "chat"],
  ] as const)('confirms a %s mode change for a %s classified as %s', async (runInBackground, type, classification, target) => {
    mocks.listShowcaseCases.mockResolvedValue({ cases: [{ id: 'chosen-case', type: classification,
      workflow_ref: type === 'workflow' ? 'builtin:chosen' : undefined,
      builtin_skill_uid: type === 'skill' ? 'chosen-skill' : undefined }] });
    mocks.listSkillAssetsPage.mockResolvedValue({ records: [{ id: 'installed-skill', name: 'Chosen', originBuiltinSkillUid: 'chosen-skill' }] });
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: 'builtin:chosen', name: 'Chosen' }] } });
    const onEntryModeChange = vi.fn();
    const onSend = vi.fn();
    const onChange = vi.fn();
    render(<MentionEditor value='' placeholder='message' runInBackground={runInBackground} onEntryModeChange={onEntryModeChange}
      onChange={onChange} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={onSend} onCompositionChange={vi.fn()} />);
    enterQuery(`Keep /${type}:`);
    const option = await screen.findByRole('option', { name: 'Chosen' });
    if (type === 'skill') {
      fireEvent.keyDown(screen.getByRole('textbox'), { key: 'ArrowDown' });
      fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    }
    else { fireEvent.mouseDown(option); fireEvent.click(option); }
    const dialog = await screen.findByRole('dialog');
    expect(onEntryModeChange).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox').textContent).toBe(`Keep /${type}:`);
    fireEvent.click(within(dialog).getByRole('button', { name: 'chat.capabilityModeSwitchConfirm' }));
    expect(onEntryModeChange).toHaveBeenCalledOnce();
    expect(onEntryModeChange).toHaveBeenCalledWith(target, {
      text: 'Keep Chosen', featuredSelection: { caseId: 'chosen-case' },
      mentions: [expect.objectContaining({ type, resource_id: type === 'skill' ? 'installed-skill' : 'builtin:chosen', display_name: 'Chosen', start: 5, end: 11 })],
    });
    expect(onSend).not.toHaveBeenCalled();
    expect(onChange).toHaveBeenLastCalledWith(`Keep /${type}:`);
    expect(mocks.listShowcaseCases).toHaveBeenCalledTimes(1);
  });

  it('leaves the draft and mode unchanged when cancelling a capability switch', async () => {
    mocks.listShowcaseCases.mockResolvedValue({ cases: [{ id: 'ppt', type: 'workflow', workflow_ref: 'builtin:ppt' }] });
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: 'builtin:ppt', name: 'PPT' }] } });
    const onEntryModeChange = vi.fn();
    render(<MentionEditor value='' placeholder='message' onEntryModeChange={onEntryModeChange}
      onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('Draft /');
    fireEvent.click(await screen.findByRole('option', { name: 'PPT' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'common.cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(onEntryModeChange).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox').textContent).toBe('Draft /');
  });

  it.each(['same-mode', 'custom'])('inserts a %s capability without a confirmation', async kind => {
    mocks.listShowcaseCases.mockResolvedValue({ cases: kind === 'custom' ? [] : [{ id: 'skill', type: 'chat', builtin_skill_uid: 'builtin-skill' }] });
    mocks.listSkillAssetsPage.mockResolvedValue({ records: [{ id: 'installed', name: 'Skill', originBuiltinSkillUid: 'builtin-skill' }] });
    const onMentionsChange = vi.fn();
    const onEntryModeChange = vi.fn();
    render(<MentionEditor value='' placeholder='message' onEntryModeChange={onEntryModeChange}
      onChange={vi.fn()} onMentionsChange={onMentionsChange} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('/');
    fireEvent.click(await screen.findByRole('option', { name: 'Skill' }));
    await waitFor(() => expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ resource_id: 'installed' })]));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onEntryModeChange).not.toHaveBeenCalled();
  });

  it('ignores a capability lookup that finishes after changing conversation', async () => {
    let resolve!: (value: unknown) => void;
    mocks.listShowcaseCases.mockReturnValue(new Promise(done => { resolve = done; }));
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: 'builtin:ppt', name: 'PPT' }] } });
    const props = { value: '', placeholder: 'message', onEntryModeChange: vi.fn(), onChange: vi.fn(), onMentionsChange: vi.fn(), onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
    const view = render(<MentionEditor {...props} conversationId='old' />);
    enterQuery('/');
    fireEvent.click(await screen.findByRole('option', { name: 'PPT' }));
    view.rerender(<MentionEditor {...props} conversationId='new' />);
    await act(async () => resolve({ cases: [{ id: 'ppt', type: 'workflow', workflow_ref: 'builtin:ppt' }] }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(props.onEntryModeChange).not.toHaveBeenCalled();
  });

  it('keeps the switch disabled until uploads complete and dismisses it on a mode change', async () => {
    mocks.listShowcaseCases.mockResolvedValue({ cases: [{ id: 'ppt', type: 'workflow', workflow_ref: 'builtin:ppt' }] });
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: 'builtin:ppt', name: 'PPT' }] } });
    const props = { value: '', placeholder: 'message', onEntryModeChange: vi.fn(), onChange: vi.fn(), onMentionsChange: vi.fn(), onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
    const view = render(<MentionEditor {...props} entryModeSwitchDisabled />);
    enterQuery('/');
    fireEvent.click(await screen.findByRole('option', { name: 'PPT' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('button', { name: 'chat.capabilityModeSwitchConfirm' })).toBeDisabled();
    view.rerender(<MentionEditor {...props} entryModeSwitchDisabled={false} />);
    expect(within(dialog).getByRole('button', { name: 'chat.capabilityModeSwitchConfirm' })).toBeEnabled();
    view.rerender(<MentionEditor {...props} runInBackground />);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(props.onEntryModeChange).not.toHaveBeenCalled();
  });

  it('keeps the selection retryable when capability classification cannot be loaded', async () => {
    const error = vi.spyOn(message, 'error').mockImplementation(vi.fn());
    mocks.listShowcaseCases.mockRejectedValueOnce(new Error('offline'));
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [{ workflow_ref: 'builtin:ppt', name: 'PPT' }] } });
    const onEntryModeChange = vi.fn();
    render(<MentionEditor value='' placeholder='message' onEntryModeChange={onEntryModeChange}
      onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('/');
    fireEvent.click(await screen.findByRole('option', { name: 'PPT' }));
    await waitFor(() => expect(error).toHaveBeenCalledWith('chat.mentionLoadFailed'));
    expect(screen.getByRole('textbox').textContent).toBe('/');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onEntryModeChange).not.toHaveBeenCalled();
    mocks.listShowcaseCases.mockResolvedValue({ cases: [{ id: 'ppt', type: 'workflow', workflow_ref: 'builtin:ppt' }] });
    fireEvent.click(screen.getByRole('option', { name: 'PPT' }));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  it('separates reference candidates from slash invocations and offers current files', async () => {
    mocks.listDatasets.mockResolvedValue({ data: { datasets: [{ dataset_id: 'split-kb', display_name: 'Reference knowledge' }] } });
    mocks.listSkillAssetsPage.mockResolvedValue({ records: [{ id: 'split-skill', name: 'Execute skill' }] });
    const view = render(<MentionEditor value='' placeholder='message' fileCandidates={[{ id: 'upload:file1', name: 'Reference.pdf' }]}
      onChange={vi.fn()} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('@');
    await screen.findByRole('option', { name: 'Reference knowledge' });
    expect(screen.getByRole('option', { name: 'Reference.pdf' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Execute skill' })).toBeNull();
    enterQuery('/');
    await screen.findByRole('option', { name: 'Execute skill' });
    expect(view.container.querySelector('.chat-mention-group.is-conversation')).toBeNull();
    expect(screen.queryByRole('option', { name: 'Reference.pdf' })).toBeNull();
  });

  it('keeps keyboard selection on the same file when another group loads late', async () => {
    let resolveKnowledge!: (value: unknown) => void;
    mocks.listDatasets.mockReturnValue(new Promise(resolve => { resolveKnowledge = resolve; }));
    const onMentionsChange = vi.fn();
    render(<MentionEditor value='' placeholder='message' fileCandidates={[{ id: 'file-stable', name: 'Stable.pdf' }]}
      onChange={vi.fn()} onMentionsChange={onMentionsChange} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('@');
    await screen.findByRole('option', { name: 'Stable.pdf' });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'ArrowDown' });
    resolveKnowledge({ data: { datasets: [{ dataset_id: 'late-kb', display_name: 'Late knowledge' }] } });
    await screen.findByRole('option', { name: 'Late knowledge' });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ type: 'file', resource_id: 'file-stable' })]);
  });

  it('closes on mode change, preserves the draft, and ignores the previous mode response', async () => {
    let resolveOld!: (value: unknown) => void;
    mocks.listConversations.mockImplementation((_params, options) => options?.params?.is_task_conv
      ? Promise.resolve({ data: { conversations: [{ conversation_id: 'task', display_name: 'Task history' }] } })
      : new Promise(resolve => { resolveOld = resolve; }));
    const props = { value: 'draft @', placeholder: 'message', onChange: vi.fn(), onMentionsChange: vi.fn(), onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
    const view = render(<MentionEditor {...props} runInBackground={false} />);
    enterQuery('draft @');
    await waitFor(() => expect(resolveOld).toBeDefined());
    view.rerender(<MentionEditor {...props} runInBackground />);
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByRole('textbox')).toHaveTextContent('draft @');
    resolveOld({ data: { conversations: [{ conversation_id: 'normal', display_name: 'Old history' }] } });
    enterQuery('draft @');
    await screen.findByRole('option', { name: 'Task history' });
    expect(screen.queryByRole('option', { name: 'Old history' })).toBeNull();
  });

  it.each([false, true])('loads older conversation history on reaching the bottom in task mode %s', async runInBackground => {
    const recent = Array.from({ length: 50 }, (_, index) => ({ conversation_id: `recent-${index}`, display_name: `Recent ${index}` }));
    mocks.listConversations.mockImplementation(({ pageToken }) => Promise.resolve({ data: pageToken
      ? { conversations: [recent[49], { conversation_id: 'older-51', display_name: 'Older conversation' }], next_page_token: '' }
      : { conversations: recent, next_page_token: '50' } }));
    const onMentionsChange = vi.fn();
    render(<MentionEditor value='' placeholder='message' runInBackground={runInBackground}
      onChange={vi.fn()} onMentionsChange={onMentionsChange} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('@chat:');
    await screen.findByRole('option', { name: 'Recent 0' });
    expect(screen.getAllByRole('option')).toHaveLength(50);
    expect(screen.queryByRole('button', { name: 'chat.mentionShowMore' })).toBeNull();
    scrollMenu(550);
    expect(mocks.listConversations.mock.calls.some(([params]) => params.pageToken === '50')).toBe(false);
    scrollMenu();
    const older = await screen.findByRole('option', { name: 'Older conversation' });
    expect(screen.getAllByRole('option')).toHaveLength(51);
    expect(mocks.listConversations).toHaveBeenCalledWith(
      { keyword: '', pageSize: 50, pageToken: '50' },
      expect.objectContaining({ params: { is_task_conv: runInBackground }, signal: expect.any(AbortSignal) }),
    );
    expect(screen.queryByRole('button', { name: 'chat.mentionLoadMoreConversations' })).toBeNull();
    scrollMenu();
    expect(mocks.listConversations.mock.calls.filter(([params]) => params.pageToken === '50')).toHaveLength(1);
    fireEvent.mouseDown(older);
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ type: 'conversation', resource_id: 'older-51' })]);
  });

  it('uses server search across history without discarding matches outside the display name', async () => {
    mocks.listConversations.mockImplementation(({ keyword }) => Promise.resolve({ data: {
      conversations: keyword === 'old decision' ? [{ conversation_id: 'old-meeting', display_name: 'Earlier meeting' }] : [],
    } }));
    render(<MentionEditor value='' placeholder='message' onChange={vi.fn()} onMentionsChange={vi.fn()}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('@chat:old decision');
    expect(await screen.findByRole('option', { name: 'Earlier meeting' })).toBeVisible();
    expect(mocks.listConversations).toHaveBeenCalledWith(
      { keyword: 'old decision', pageSize: 50, pageToken: '' }, expect.anything(),
    );
  });

  it('keeps loaded history and retries a failed next page without duplicate requests', async () => {
    const recent = Array.from({ length: 50 }, (_, index) => ({ conversation_id: `recent-${index}`, display_name: `Recent ${index}` }));
    let rejectPage!: (error: Error) => void;
    let attempts = 0;
    mocks.listConversations.mockImplementation(({ pageToken }) => {
      if (!pageToken) return Promise.resolve({ data: { conversations: recent, next_page_token: '50' } });
      attempts += 1;
      return attempts === 1 ? new Promise((_resolve, reject) => { rejectPage = reject; })
        : Promise.resolve({ data: { conversations: [{ conversation_id: 'older', display_name: 'Older conversation' }] } });
    });
    render(<MentionEditor value='' placeholder='message' onChange={vi.fn()} onMentionsChange={vi.fn()}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    enterQuery('@chat:');
    await screen.findByRole('option', { name: 'Recent 0' });
    scrollMenu();
    scrollMenu();
    expect(attempts).toBe(1);
    expect(screen.getByRole('status')).toHaveTextContent('common.loading');
    await act(async () => rejectPage(new Error('temporary failure')));
    expect(await screen.findByRole('alert')).toHaveTextContent('chat.mentionLoadFailed');
    expect(screen.getAllByRole('option')).toHaveLength(50);
    scrollMenu();
    expect(attempts).toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(await screen.findByRole('option', { name: 'Older conversation' })).toBeVisible();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(attempts).toBe(2);
  });

  it.each(['query', 'mode', 'close'])('cancels an in-flight next page on %s', async change => {
    const recent = Array.from({ length: 50 }, (_, index) => ({ conversation_id: `recent-${index}`, display_name: `Recent ${index}` }));
    let resolvePage!: (value: unknown) => void;
    let pageSignal!: AbortSignal;
    mocks.listConversations.mockImplementation(({ keyword, pageToken }, options) => {
      if (keyword || options.params.is_task_conv) return Promise.resolve({ data: {
        conversations: [{ conversation_id: 'fresh', display_name: 'Fresh history' }],
      } });
      if (!pageToken) return Promise.resolve({ data: { conversations: recent, next_page_token: '50' } });
      pageSignal = options.signal;
      return new Promise(resolve => { resolvePage = resolve; });
    });
    const props = { value: '', placeholder: 'message', onChange: vi.fn(), onMentionsChange: vi.fn(), onPaste: vi.fn(), onSend: vi.fn(), onCompositionChange: vi.fn() };
    const view = render(<MentionEditor {...props} runInBackground={false} />);
    enterQuery('@chat:');
    await screen.findByRole('option', { name: 'Recent 0' });
    scrollMenu();
    if (change === 'close') {
      fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' });
      expect(pageSignal.aborted).toBe(true);
      await act(async () => resolvePage({ data: { conversations: [{ conversation_id: 'late', display_name: 'Late old page' }] } }));
      expect(screen.queryByRole('listbox')).toBeNull();
      return;
    }
    if (change === 'mode') view.rerender(<MentionEditor {...props} runInBackground />);
    enterQuery(change === 'query' ? '@chat:Fresh' : '@chat:');
    await screen.findByRole('option', { name: 'Fresh history' });
    expect(pageSignal.aborted).toBe(true);
    await act(async () => resolvePage({ data: { conversations: [{ conversation_id: 'late', display_name: 'Late old page' }], next_page_token: '100' } }));
    expect(screen.queryByRole('option', { name: 'Late old page' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'chat.mentionLoadMoreConversations' })).toBeNull();
  });

  it('deletes one chip without deleting surrounding text or the remaining identity', () => {
    const mentions = [
      { mention_id: 'one', type: 'skill' as const, resource_id: 's1', display_name: 'First', start: 0, end: 5 },
      { mention_id: 'two', type: 'workflow' as const, resource_id: 'w1', display_name: 'Second', start: 6, end: 12 },
    ];
    const onMentionsChange = vi.fn();
    const onChange = vi.fn();
    render(<MentionEditor value='First Second text' initialMentions={mentions} placeholder='message'
      onChange={onChange} onMentionsChange={onMentionsChange} onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'chat.removeMention First' }));
    expect(onChange).toHaveBeenLastCalledWith(' Second text');
    expect(onMentionsChange.mock.lastCall?.[0]).toEqual([expect.objectContaining({ resource_id: 'w1', start: 1, end: 7 })]);
  });

  it('activates chip deletion with the keyboard without submitting the message', () => {
    const onSend = vi.fn();
    const onChange = vi.fn();
    render(<MentionEditor value='First text' initialMentions={[{ mention_id: 'one', type: 'skill', resource_id: 's1', display_name: 'First', start: 0, end: 5 }]} placeholder='message'
      onChange={onChange} onMentionsChange={vi.fn()} onPaste={vi.fn()} onSend={onSend} onCompositionChange={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'chat.removeMention First' }), { key: 'Enter' });
    expect(onChange).toHaveBeenLastCalledWith(' text');
    expect(onSend).not.toHaveBeenCalled();
  });

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
    mocks.listShowcaseCases.mockReset();
    mocks.listShowcaseCases.mockResolvedValue({ cases: [] });

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

  it.each(['@', '/skill:', '/workflow:', '@tool:', '@kb:', '@chat:', '@prompt:'])(
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

  it.each(["outside click", "Escape"])("dismisses the menu with %s without removing the query and allows reopening", async (action) => {
    const onChange = vi.fn();
    const onMentionsChange = vi.fn();
    mocks.listSkillAssetsPage.mockResolvedValue({ records: [{ id: "dismiss-skill", name: "测试技能" }] });
    render(<>
      <button type="button">outside</button>
      <MentionEditor value="" placeholder="message" onChange={onChange} onMentionsChange={onMentionsChange}
        onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />
    </>);
    const editor = screen.getByRole("textbox");
    editor.textContent = "请参考 /";
    const range = document.createRange();
    range.setStart(editor.firstChild!, editor.textContent.length);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);
    expect(await screen.findByRole("option", { name: "测试技能" })).toBeVisible();

    if (action === "outside click") {
      const outside = screen.getByRole("button", { name: "outside" });
      fireEvent.pointerDown(outside);
      fireEvent.mouseDown(outside);
      fireEvent.click(outside);
    } else {
      fireEvent.keyDown(editor, { key: "Escape" });
      fireEvent.keyUp(editor, { key: "Escape" });
    }

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(editor).toHaveTextContent("请参考 /");
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenLastCalledWith("请参考 /");
    fireEvent.click(editor);
    const option = await screen.findByRole("option", { name: "测试技能" });
    fireEvent.pointerDown(option);
    fireEvent.mouseDown(option);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(onChange).toHaveBeenLastCalledWith("请参考 测试技能");
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ resource_id: "dismiss-skill" })]);
  });

  it("shows all loaded skills and workflows without expansion controls and supports keyboard selection", async () => {
    mocks.listSkillAssetsPage.mockResolvedValue({ records: Array.from({ length: 10 }, (_, index) => ({
      id: `skill-${index}`, name: `技能 ${index + 1}`,
    })) });
    mocks.axiosGet.mockResolvedValue({ data: { workflows: Array.from({ length: 13 }, (_, index) => ({
      workflow_ref: `builtin:workflow-${index}`, workflow_id: `workflow-${index}`, name: `工作流 ${index + 1}`,
    })) } });
    const onMentionsChange = vi.fn();
    render(<MentionEditor value="" placeholder="message" onChange={vi.fn()} onMentionsChange={onMentionsChange}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole("textbox");
    enterQuery('/');
    expect(await screen.findByRole("option", { name: "技能 10" })).toBeVisible();
    expect(await screen.findByRole("option", { name: "工作流 13" })).toBeVisible();
    expect(screen.getAllByRole('option')).toHaveLength(23);
    expect(screen.queryByRole('button', { name: 'chat.mentionShowMore' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'chat.mentionCollapse' })).toBeNull();
    fireEvent.pointerDown(editor);
    fireEvent.click(editor);
    const menu = screen.getByRole("listbox");
    fireEvent.pointerDown(menu);
    fireEvent.mouseDown(menu);
    fireEvent.click(menu);
    expect(menu).toBeVisible();
    for (let index = 0; index < 23; index += 1) fireEvent.keyDown(editor, { key: 'ArrowDown' });
    fireEvent.keyDown(editor, { key: 'Enter' });
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ type: 'workflow', resource_id: 'builtin:workflow-12' })]);
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
    editor.textContent = "/skill:";
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
    editor.textContent = "/workflow:";
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

  it("marks paused workflows unavailable instead of inserting a mention that the server will reject", async () => {
    mocks.axiosGet.mockResolvedValue({
      data: {
        workflows: [
          {
            workflow_ref: "builtin:academic_research_pipeline",
            workflow_id: "academic_research_pipeline",
            name: "学术研究与论文写作",
            description: "",
            enabled: false,
            call_mode: "disabled",
          },
          {
            workflow_ref: "builtin:writer-workflow",
            workflow_id: "writer-workflow",
            name: "AI Writer",
            description: "",
            enabled: true,
            call_mode: "auto",
          },
        ],
      },
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
    editor.textContent = "/workflow:";
    const range = document.createRange();
    range.setStart(editor.firstChild!, editor.textContent.length);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: /学术研究与论文写作/ })).toBeDisabled();
    expect(screen.getByRole("option", { name: "AI Writer" })).toBeEnabled();
  });

  it("distinguishes workflows with the same name and selects their original resource identity", async () => {
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [
      { workflow_ref: "user:owner:research-a", workflow_id: "research-a", name: "Research" },
      { workflow_ref: "user:owner:research-b", workflow_id: "research-b", name: "Research" },
    ] } });
    const onMentionsChange = vi.fn();
    render(<MentionEditor value="" placeholder="message" onChange={vi.fn()} onMentionsChange={onMentionsChange}
      onPaste={vi.fn()} onSend={vi.fn()} onCompositionChange={vi.fn()} />);
    const editor = screen.getByRole("textbox");
    editor.textContent = "/workflow:";
    const range = document.createRange(); range.setStart(editor.firstChild!, editor.textContent.length); range.collapse(true);
    window.getSelection()?.removeAllRanges(); window.getSelection()?.addRange(range); fireEvent.input(editor);
    expect(await screen.findByRole("option", { name: "Research（research-a）" })).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole("option", { name: "Research（research-b）" }));
    expect(onMentionsChange).toHaveBeenLastCalledWith([expect.objectContaining({ resource_id: "user:owner:research-b" })]);
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
    editor.textContent = "/find";
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

    editor.textContent = "/find-skill";
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
    mocks.axiosGet.mockReturnValue(new Promise(() => undefined));

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
    editor.textContent = "/find";
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
