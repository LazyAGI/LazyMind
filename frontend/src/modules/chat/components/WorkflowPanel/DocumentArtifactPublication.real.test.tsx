import { useEffect } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { MDXEditorMethods, MDXEditorProps } from '@mdxeditor/editor';
import i18n from '@/i18n';
import { DocumentArtifactEditor } from './DocumentArtifactEditor';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

const capture = vi.hoisted(() => ({ change: undefined as MDXEditorProps['onChange'], editor: null as MDXEditorMethods | null }));
const api = vi.hoisted(() => ({ listDocumentProviders: vi.fn(), publishDocument: vi.fn(), saveDocumentArtifact: vi.fn(), getPublicationTargets: vi.fn(), createPublicationGroup: vi.fn() }));
vi.mock('@mdxeditor/editor', async () => {
  const actual = await vi.importActual<typeof import('@mdxeditor/editor')>('@mdxeditor/editor');
  const React = await import('react');
  return { ...actual, MDXEditor: React.forwardRef<MDXEditorMethods, MDXEditorProps>((props, ref) => {
    capture.change = props.onChange;
    return <actual.MDXEditor {...props} ref={editor => { capture.editor = editor; if (typeof ref === 'function') ref(editor); else if (ref) ref.current = editor; }} />;
  }) };
});
vi.mock('@/modules/chat/utils/request', async original => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(), WorkflowSessionApi: () => api,
}));
vi.mock('./useDocumentCopy', () => ({ useDocumentCopy: () => {} }));
vi.mock('./ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null, ArtifactRewriteInlineDiff: () => null }));
const availability = vi.hoisted(() => ({ states: { feishu: 'ready' }, accounts: { feishu: [{ connection_id: 'fixture-account' }] }, refresh: vi.fn() }));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => availability }));
vi.mock('./DocumentPublicationRecoveryPanel', () => ({
  DocumentPublicationRecoveryPanel: ({ onAvailability }: { onAvailability: (allowed: boolean) => void }) => {
    useEffect(() => onAvailability(true), [onAvailability]); return null;
  },
}));
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals(); });

it.each(['before-receipt', 'after-receipt', 'after-autosave'])('autosaves newer edits when the publication refresh arrives %s', async refreshTiming => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  await i18n.changeLanguage('en-US');
  let finish!: (response: unknown) => void;
  api.createPublicationGroup.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  api.getPublicationTargets.mockResolvedValue({ data: { data: { current_hash: 'fixture', preferred_providers: ['feishu'], targets: [] } } });
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
  api.saveDocumentArtifact.mockImplementation(async (_id, body) => ({ data: { ok: true, result: {
    artifact_id: 'local-edits', revision: 3, draft_version: 1, value: body.value,
  } } }));
  const slot = { artifact_id: 'before-publication', slot_id: 'flat_draft_document', revision: 1, draft_version: 1,
    artifact_value: { text: '# Original\n\nExisting paragraph.\n' },
    document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] },
  } as SlotRevision;
  // Publication updates provider state without replacing the editable artifact.
  const publishedSlot = { ...slot, artifact_value: { ...slot.artifact_value }, write_back_state: 'synced_clean' } as SlotRevision;
  let action: SlotFooterAction | undefined;
  const context = { setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  } satisfies React.ContextType<typeof SlotEditingContext>;
  const editor = (current: SlotRevision) => <SlotEditingContext.Provider value={context}><DocumentArtifactEditor slot={current} sessionId='publication-fixture' /></SlotEditingContext.Provider>;
  const view = render(editor(slot));
  await waitFor(() => expect(action?.disabled).toBe(false));
  act(() => { void action!.onClick(); });
  fireEvent.click(await screen.findByRole('button', { name: /^Publish to / }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledTimes(1));
  const root = view.container.querySelector('.mdxeditor-root-contenteditable');
  await act(async () => capture.editor!.setMarkdown('# Original\n\nExisting paragraph.\n\nNew local edits.'));
  await act(async () => capture.change?.(capture.editor!.getMarkdown(), false));
  if (refreshTiming === 'before-receipt') view.rerender(editor(publishedSlot));
  await act(async () => finish({ data: { data: { current_hash: 'fixture', preferred_providers: ['feishu'], targets: [] } } }));
  if (refreshTiming === 'after-receipt') view.rerender(editor(publishedSlot));
  expect(view.container.querySelector('.mdxeditor-root-contenteditable')).toBe(root);
  expect(root).toHaveTextContent('New local edits.');
  await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenCalledTimes(1), { timeout: 2500 });
  const [id, body] = api.saveDocumentArtifact.mock.calls[0];
  expect(id).toBe('before-publication');
  expect(body).toMatchObject({ base_revision: 1, base_draft_version: 1, content_type: 'text/markdown', mode: 'draft' });
  expect(body.value.text).toContain('New local edits.');
  if (refreshTiming === 'after-autosave') view.rerender(editor(publishedSlot));
  expect(view.container.querySelector('.mdxeditor-root-contenteditable')).toBe(root);
  expect(root).toHaveTextContent('New local edits.');
  expect(view.container.querySelector('[role="alert"]')).toBeNull();
  expect(api.createPublicationGroup).toHaveBeenCalledTimes(1);
  expect(api.createPublicationGroup.mock.calls[0][1]).toMatchObject({ base_revision: 1, base_draft_version: 1 });
  expect(api.publishDocument).not.toHaveBeenCalled();
});
