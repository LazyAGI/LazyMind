vi.mock('./writerListNumberingPlugin', () => ({ writerListNumberingPlugin: () => ({}) }));
vi.mock('./writerLocalSourcePlugin', () => ({ writerLocalSourcePlugin: () => ({}), writerLocalCodeEditor: {} }));
vi.mock('./writerImageDownloadPlugin', () => ({ writerImageDownloadPlugin: () => ({}) }));
const availability = vi.hoisted(() => ({
  states: { 'future-provider': 'ready' }, accounts: { 'future-provider': [{ connection_id: 'fixture-account' }] }, refresh: vi.fn(),
}));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => availability }));
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SlotRenderer, SlotEditingContext } from './SlotComponents';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { WriterIRControl } from './WriterIRControl';
import type { SlotFooterAction } from './slotEditingContext';
import i18n from '@/i18n';

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
  getPublicationTargets: vi.fn(), createPublicationGroup: vi.fn(),
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

beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('en-US');
  api.getPublicationForArtifact.mockResolvedValue({ data: { data: {} } });
  api.getPublicationTargets.mockResolvedValue({ data: { data: { current_hash: 'fixture', preferred_providers: [], targets: [] } } });
  api.createPublicationGroup.mockReset().mockRejectedValue(new Error('Acceptance response lost'));
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
      expect(beforeFlushAction.flushBeforeAction).toBe(false); // Saving occurs only after publication confirmation.
      const flush = flushers.get(beforeFlushAction.flushKey!);
      expect(flush).toBeTypeOf('function');
      act(() => beforeFlushAction.onClick());
      fireEvent.click(await screen.findByRole('checkbox', { name: 'future-provider' }));
      expect(api.saveDocumentArtifact).not.toHaveBeenCalled();
      expect(api.createPublicationGroup).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button', { name: 'Publish to future-provider' }));
      await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenCalledTimes(1));
      expect(api.saveDocumentArtifact.mock.calls[0][0]).toBe('before-save');
      expect(JSON.stringify(api.saveDocumentArtifact.mock.calls[0][1].value)).toContain(representation === 'markdown' ? 'Edited markdown' : 'Edited IR');
      expect(api.saveDocumentArtifact.mock.calls[0][1]).toEqual(expect.objectContaining({ base_revision: 3, base_draft_version: 7 }));
      if (conflict) {
        expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent('Save failed');
        expect((editor as HTMLTextAreaElement).value).toContain(representation === 'markdown' ? 'Edited markdown' : 'Edited IR');
        expect(api.publishDocument).not.toHaveBeenCalled();
        expect(api.createPublicationGroup).not.toHaveBeenCalled();
        expect(confirm).not.toHaveBeenCalled();
        return;
      }
      await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledTimes(1));
      const retry = await screen.findByRole('button', { name: /Check this publication request/ });
      vi.useFakeTimers();
      expect(confirm).not.toHaveBeenCalled();
      await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
      expect(api.createPublicationGroup).toHaveBeenCalledTimes(1);
      const [id, body] = api.createPublicationGroup.mock.calls[0];
      expect(id).toBe('after-save');
      expect(body).toEqual(expect.objectContaining({ base_revision: 4, base_draft_version: 1,
        idempotency_key: expect.any(String), targets: [expect.objectContaining({ provider: 'future-provider', connection_id: 'fixture-account' })] }));
      // An explicit retry can only replay the same operation; never auto-write.
      await act(async () => { fireEvent.click(retry); });
      expect(api.createPublicationGroup).toHaveBeenCalledTimes(2);
      expect(api.createPublicationGroup.mock.calls[1]).toEqual(api.createPublicationGroup.mock.calls[0]);
      expect(api.saveDocumentArtifact).toHaveBeenCalledTimes(1);
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

it.each(['markdown', 'ir'] as const)('%s: keeps autosaved edits separate from the fixed publication snapshot', async representation => {
  let finish!: (value: unknown) => void;
  api.createPublicationGroup.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
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
  fireEvent.click(await screen.findByRole('checkbox', { name: 'future-provider' }));
  fireEvent.click(screen.getByRole('button', { name: 'Publish to future-provider' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledTimes(1));
  const request = api.createPublicationGroup.mock.calls[0];
  expect(request[0]).toBe('before-publish');
  expect(request[1]).toMatchObject({ base_revision: 3, base_draft_version: 7 });
  fireEvent.change(editor, { target: { value: '发布期间新增中文' } });
  await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenCalledTimes(1), { timeout: 2500 });
  expect(api.saveDocumentArtifact.mock.calls[0][0]).toBe('before-publish');
  expect(api.saveDocumentArtifact.mock.calls[0][1]).toMatchObject({ base_revision: 3, base_draft_version: 7 });
  expect(JSON.stringify(api.saveDocumentArtifact.mock.calls[0][1].value)).toContain('发布期间新增中文');
  expect(editor).not.toBeDisabled();
  await act(async () => finish({ data: { data: { current_hash: 'fixture', preferred_providers: ['future-provider'], targets: [] } } }));
  expect((editor as HTMLTextAreaElement).value).toContain('发布期间新增中文');
  expect(api.createPublicationGroup).toHaveBeenCalledTimes(1);
  expect(request[1]).toMatchObject({ base_revision: 3, base_draft_version: 7 });
  expect(api.publishDocument).not.toHaveBeenCalled();
});
