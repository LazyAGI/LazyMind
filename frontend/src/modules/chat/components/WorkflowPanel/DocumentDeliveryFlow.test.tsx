vi.mock('./writerListNumberingPlugin', () => ({ writerListNumberingPlugin: () => ({}) }));
vi.mock('./writerLocalSourcePlugin', () => ({ writerLocalSourcePlugin: () => ({}), writerLocalCodeEditor: {} }));
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SlotRenderer, SlotEditingContext } from './SlotComponents';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { WriterIRControl } from './WriterIRControl';
import type { SlotFooterAction } from './slotEditingContext';

// Only the third-party editing surfaces are substitutes. The real Markdown
// editor / WriterIRControl, registered flush functions and SlotRenderer save
// callbacks remain on the path under test.
vi.mock('@mdxeditor/editor', async () => {
  const R = await import('react');
  const plugin = () => ({}); const control = () => null;
  const MDXEditor = R.forwardRef((props: Record<string, unknown>, ref) => {
    const text = R.useRef(String(props.markdown ?? ''));
    const [value, setValue] = R.useState(text.current);
    R.useImperativeHandle(ref, () => ({ getMarkdown: () => text.current,
      setMarkdown: (next: string) => { text.current = next; setValue(next); } }));
    return <textarea aria-label='markdown editing surface' value={value} onChange={(event) => {
      text.current = event.target.value; setValue(text.current);
      (props.onChange as (next: string) => void)?.(text.current);
    }} />;
  });
  return { MDXEditor, BlockTypeSelect: control, BoldItalicUnderlineToggles: control, ListsToggle: control, GenericJsxEditor: control,
    realmPlugin: () => plugin,
    codeBlockPlugin: plugin, codeMirrorPlugin: plugin, frontmatterPlugin: plugin, headingsPlugin: plugin, imagePlugin: plugin,
    jsxPlugin: plugin, linkDialogPlugin: plugin, linkPlugin: plugin, listsPlugin: plugin, markdownShortcutPlugin: plugin,
    quotePlugin: plugin, tablePlugin: plugin, thematicBreakPlugin: plugin, toolbarPlugin: plugin };
});
vi.mock('./WriterIRDocumentEditor', async (original) => ({
  ...await original<typeof import('./WriterIRDocumentEditor')>(),
  WriterIRDocumentEditor: (props: { document: Record<string, unknown>; onChange: (next: Record<string, unknown>) => void }) =>
    <textarea aria-label='IR editing surface' value={JSON.stringify(props.document)} onChange={event => props.onChange({
      ...props.document, blocks: [{ node_id: 'p', type: 'paragraph', content: event.currentTarget.value }],
    })} />,
}));
vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('@/modules/chat/components/MarkdownViewer', () => ({ default: ({ children }: { children: string }) => <div>{children}</div> }));
const api = vi.hoisted(() => ({
  getPublicationForArtifact: vi.fn(), listDocumentProviders: vi.fn(), saveDocumentArtifact: vi.fn(), publishDocument: vi.fn(), getSlots: vi.fn(),
}));
const confirm = vi.hoisted(() => vi.fn());
vi.mock('@/modules/chat/utils/request', async (original) => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(), WorkflowSessionApi: () => api,
}));
vi.mock('antd', async (original) => {
  const actual = await original<typeof import('antd')>();
  return { ...actual, Modal: Object.assign(actual.Modal, { confirm }) };
});

afterEach(() => vi.useRealTimers());

beforeEach(() => {
  vi.clearAllMocks();
  api.getPublicationForArtifact.mockResolvedValue({ data: { data: {} } });
  api.getSlots.mockResolvedValue({ data: { data: { slots: [] } } });
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'future-provider', capabilities: ['create', 'replace', 'patch'] }] } } });
  api.saveDocumentArtifact.mockImplementation(async (_id, body) => ({ data: { contract_version: 'workflow.v1', ok: true, result: {
    artifact_id: 'after-save', session_id: 'unknown-session', slot_id: 'unknown-slot', slot: 'unknown-slot',
    revision: 4, draft_version: 1, selected: true, validity: 'effective', content_type: body.content_type, value: body.value,
  } } }));
  api.publishDocument.mockRejectedValue({ response: { status: 502, data: { data: { code: 'PUBLICATION_OUTCOME_UNKNOWN', retryable: false } } } });
});

for (const representation of ['markdown', 'ir'] as const) {
  for (const conflict of [false, true]) {
    it(`${representation}: real editor flush ${conflict ? 'preserves conflicting draft and stops' : 'uses newly saved identity and baselines'}`, async () => {
      if (conflict) api.saveDocumentArtifact.mockRejectedValue({ response: { status: 409, data: {
        contract_version: 'workflow.v1', ok: false, error: { code: 'DRAFT_VERSION_CONFLICT', retryable: false },
      } } });
      const flushers = new Map<string, () => Promise<boolean>>();
      let action: SlotFooterAction | undefined;
      const ir = { document_id: 'local-ir', title: 'Fixture', stage: 'draft', ui_editable: true,
        blocks: [{ node_id: 'p', type: 'paragraph', content: 'Original IR' }] };
      const slot = { artifact_id: 'before-save', slot_id: 'unknown-slot', slot: 'unknown-slot',
        revision: 3, draft_version: 7, selected: true, created_at: '2026-09-12T00:00:00Z',
        content_type: representation === 'markdown' ? 'text/markdown' : 'json',
        artifact_value: representation === 'markdown' ? { text: '# Original' } : { schema: 'application/vnd.lazymind.writer+json', data: ir },
        document: { representation, schema: representation === 'markdown' ? 'text/markdown' : 'application/vnd.lazymind.writer+json',
          editable: true, capabilities: ['save', 'publish_document'] } };
      render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(),
        registerFlush: (key, flush) => { flushers.set(key, flush); return () => { flushers.delete(key); }; },
        registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
      }}><SlotRenderer slot={slot} sessionId='unknown-session' onRefresh={vi.fn()} /></SlotEditingContext.Provider>);
      const editButton = screen.queryAllByRole('button', { name: /^(编辑|Edit)$/i })[0];
      if (editButton) fireEvent.click(editButton);
      const editor = await screen.findByRole('textbox', { name: representation === 'markdown' ? 'markdown editing surface' : 'IR editing surface' });
      fireEvent.change(editor, { target: { value: representation === 'markdown' ? '# Edited markdown' : 'Edited IR' } });
      await waitFor(() => expect(action?.flushKey).toBeTruthy());
      const beforeFlushAction = action!;
      expect(beforeFlushAction.flushBeforeAction).toBe(true); // The real panel captures onClick before awaiting flush.
      const flush = flushers.get(beforeFlushAction.flushKey!);
      expect(flush).toBeTypeOf('function');
      let saved = false;
      await act(async () => { saved = await flush!(); });
      expect(api.saveDocumentArtifact).toHaveBeenCalledTimes(1);
      expect(api.saveDocumentArtifact.mock.calls[0][0]).toBe('before-save');
      expect(JSON.stringify(api.saveDocumentArtifact.mock.calls[0][1].value)).toContain(representation === 'markdown' ? 'Edited markdown' : 'Edited IR');
      expect(api.saveDocumentArtifact.mock.calls[0][1]).toEqual(expect.objectContaining({ base_revision: 3, base_draft_version: 7 }));
      if (conflict) {
        expect(saved).toBe(false);
        expect((editor as HTMLTextAreaElement).value).toContain(representation === 'markdown' ? 'Edited markdown' : 'Edited IR');
        expect(api.publishDocument).not.toHaveBeenCalled();
        expect(confirm).not.toHaveBeenCalled();
        return;
      }
      expect(saved).toBe(true);
      vi.useFakeTimers();
      act(() => beforeFlushAction.onClick());
      expect(confirm).not.toHaveBeenCalled();
      await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
      expect(api.publishDocument).toHaveBeenCalledTimes(1);
      const [id, body] = api.publishDocument.mock.calls[0];
      expect(id).toBe('after-save');
      expect(body).toEqual(expect.objectContaining({ base_revision: 4, base_draft_version: 1,
        input: expect.objectContaining({ provider: 'future-provider', idempotency_key: expect.any(String) }) }));
      const key = body.input.idempotency_key;
      // An explicit retry can only replay the same operation; never auto-write.
      expect(api.publishDocument).toHaveBeenCalledTimes(1);
      await act(async () => { beforeFlushAction.onClick(); });
      if (api.publishDocument.mock.calls.length > 1) {
        expect(api.publishDocument.mock.calls[1][1].input.idempotency_key).toBe(key);
      }
    });
  }
}

it.each(['markdown', 'ir'])('%s editing surface control uses the real registered editor flush', async (kind) => {
  let flush: (() => Promise<boolean>) | undefined;
  const save = vi.fn();
  save.mockImplementation(async (...args) => kind === 'markdown' ? 4 : { document: args[1], sourceRevision: 4 });
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFooterAction: () => () => {},
    registerFlush: (_key, fn) => { flush = fn; return () => {}; },
  }}>{kind === 'markdown'
    ? <MarkdownArtifactEditor markdown='# Original' sourceRevision={3} editingKey='control' onSave={save} />
    : <WriterIRControl document={{ document_id: 'ir', title: 'IR', stage: 'draft', ui_editable: true,
      blocks: [{ node_id: 'p', type: 'paragraph', content: 'Original IR' }] }} editingKey='control' sourceRevision={3} onSave={save} />
  }</SlotEditingContext.Provider>);
  const editor = await screen.findByRole('textbox', { name: kind === 'markdown' ? 'markdown editing surface' : 'IR editing surface' });
  fireEvent.change(editor, { target: { value: kind === 'markdown' ? '# Edited markdown' : 'Edited IR' } });
  await waitFor(() => expect(flush).toBeTypeOf('function'));
  await act(async () => { expect(await flush!()).toBe(true); });
  expect(save).toHaveBeenCalledTimes(1);
  expect(JSON.stringify(save.mock.calls[0])).toContain(kind === 'markdown' ? 'Edited markdown' : 'Edited IR');
});

it.each(['markdown', 'ir'] as const)('%s: keeps new edits while a fixed version is publishing and resumes autosave on the returned identity', async representation => {
  let finish!: (value: unknown) => void;
  api.publishDocument.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  const ir = { document_id: 'ir', title: 'Fixture', stage: 'draft', ui_editable: true, blocks: [{ node_id: 'p', type: 'paragraph', content: 'Original' }] };
  const original = representation === 'markdown' ? '# Original' : ir;
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'before-publish', slot_id: 'unknown-slot', slot: 'unknown-slot', revision: 3, draft_version: 7,
    selected: true, created_at: '2026-09-12T00:00:00Z', content_type: representation === 'markdown' ? 'text/markdown' : 'json', artifact_value: { data: original },
    document: { representation, schema: '', editable: true, capabilities: ['save', 'publish_document'] } };
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><SlotRenderer slot={slot} sessionId='unknown-session' /></SlotEditingContext.Provider>);
  const editor = await screen.findByRole('textbox', { name: representation === 'markdown' ? 'markdown editing surface' : 'IR editing surface' });
  await waitFor(() => expect(action?.disabled).toBe(false));
  act(() => action!.onClick());
  await waitFor(() => expect(api.publishDocument).toHaveBeenCalledTimes(1));
  fireEvent.change(editor, { target: { value: '发布期间新增中文' } });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(api.saveDocumentArtifact).not.toHaveBeenCalled();
  expect(editor).not.toBeDisabled();
  await act(async () => finish({ data: { data: { artifact_id: 'published-identity', revision: 4, draft_version: 1,
    document: original, provider_synced: true, artifact_saved: true } } }));
  expect((editor as HTMLTextAreaElement).value).toContain('发布期间新增中文');
  await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenCalledTimes(1), { timeout: 2500 });
  expect(api.saveDocumentArtifact.mock.calls[0][0]).toBe('published-identity');
  expect(api.saveDocumentArtifact.mock.calls[0][1]).toMatchObject({ base_revision: 4, base_draft_version: 1 });
  expect(JSON.stringify(api.saveDocumentArtifact.mock.calls[0][1].value)).toContain('发布期间新增中文');
  expect(action?.statusText).toMatch(/新修改尚未发布|new edits are not published/i);
});
