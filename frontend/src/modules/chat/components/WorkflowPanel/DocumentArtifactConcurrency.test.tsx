import { useEffect, useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { DocumentArtifactEditor } from './DocumentArtifactEditor';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

const api = vi.hoisted(() => ({
  listDocumentProviders: vi.fn(), publishDocument: vi.fn(), previewDocumentAction: vi.fn(), saveDocumentArtifact: vi.fn(),
}));
vi.mock('@/modules/chat/utils/request', async original => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(), WorkflowSessionApi: () => api,
}));
vi.mock('./useDocumentCopy', () => ({ useDocumentCopy: () => {} }));
vi.mock('./ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null }));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => ({ states: { feishu: 'ready' }, refresh: vi.fn() }) }));
vi.mock('./DocumentPublicationRecoveryPanel', () => ({
  DocumentPublicationRecoveryPanel: ({ onAvailability }: { onAvailability: (allowed: boolean) => void }) => {
    useEffect(() => onAvailability(true), [onAvailability]); return null;
  },
}));
vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: ({ markdown, sourceRevision, savePaused, onContentChange, onSave }: {
    markdown: string; sourceRevision: number; savePaused: boolean;
    onContentChange: (text: string) => void;
    onSave: (text: string, revision: number, mode: 'draft') => Promise<unknown>;
  }) => {
    const [text, setText] = useState(markdown);
    return <><textarea aria-label='Markdown draft' value={text} onChange={event => { setText(event.target.value); onContentChange(event.target.value); }} />
      <button disabled={savePaused} onClick={() => void onSave(text, sourceRevision, 'draft')}>Save draft</button></>;
  },
}));
vi.mock('./WriterIRControl', () => ({ WriterIRControl: ({ document }: { document: unknown }) => <article aria-label='IR draft'>{JSON.stringify(document)}</article> }));

const baseSlot = { artifact_id: 'audit-artifact', slot_id: 'flat_draft_document', revision: 1, draft_version: 1,
  artifact_value: { text: '# Original' }, document: { representation: 'markdown', editable: true, capabilities: ['save'] } } as SlotRevision;
function pending<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
beforeEach(async () => {
  vi.clearAllMocks();
  localStorage.clear();
  await i18n.changeLanguage('zh-CN');
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it.each([false, true])('keeps the expected content after Markdown-to-IR publication, concurrent edits=%s', async (editDuringPublish) => {
  const publication = pending<unknown>();
  api.publishDocument.mockReturnValue(publication.promise);
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
  let action: SlotFooterAction | undefined;
  const slot = { ...baseSlot, document: { ...baseSlot.document!, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  const context = { setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  } satisfies React.ContextType<typeof SlotEditingContext>;
  const view = render(<SlotEditingContext.Provider value={context}><DocumentArtifactEditor slot={slot} sessionId='audit-session' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  act(() => { void action!.onClick(); });
  expect(api.publishDocument).toHaveBeenCalledTimes(1);
  if (editDuringPublish) {
    fireEvent.change(screen.getByLabelText('Markdown draft'), { target: { value: '# Original\n\nUNSAVED DURING PUBLISH' } });
    expect(screen.getByLabelText('Markdown draft')).toHaveValue('# Original\n\nUNSAVED DURING PUBLISH');
  }
  await act(async () => publication.resolve({ data: { data: { artifact_id: 'published-artifact', revision: 2, draft_version: 1,
    provider_synced: true, artifact_saved: true, document: { document_id: 'audit-doc', stage: 'draft', title: 'Original', blocks: [] },
  } } }));
  const shownContent = (screen.queryByLabelText('Markdown draft') as HTMLTextAreaElement | null)?.value
    ?? screen.queryByLabelText('IR draft')?.textContent;
  expect(shownContent).toContain(editDuringPublish ? 'UNSAVED DURING PUBLISH' : 'Original');
  if (editDuringPublish) {
    const published = { document_id: 'audit-doc', stage: 'draft', title: 'Original', blocks: [] };
    view.rerender(<SlotEditingContext.Provider value={context}><DocumentArtifactEditor slot={{ ...slot,
      artifact_id: 'published-artifact', revision: 2, draft_version: 1, artifact_value: { data: published },
      document: { ...slot.document!, representation: 'ir' },
    }} sessionId='audit-session' /></SlotEditingContext.Provider>);
    expect(screen.getByLabelText('Markdown draft')).toHaveValue('# Original\n\nUNSAVED DURING PUBLISH');
    api.saveDocumentArtifact.mockImplementation(async (_id, body) => ({ data: { ok: true, result: {
      artifact_id: 'saved-local', revision: 3, draft_version: 1, value: body.value,
    } } }));
    fireEvent.click(screen.getByRole('button', { name: 'Save draft' }));
    await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenCalledWith('published-artifact', expect.objectContaining({
      base_revision: 2, base_draft_version: 1, mode: 'draft', content_type: 'text/markdown',
      value: expect.objectContaining({ schema: 'text/markdown', text: '# Original\n\nUNSAVED DURING PUBLISH' }),
    }), expect.anything()));
    expect(screen.getByLabelText('Markdown draft')).toHaveValue('# Original\n\nUNSAVED DURING PUBLISH');
    expect(api.publishDocument).toHaveBeenCalledTimes(1);
  }
});

it.each([false, true])('finishes loading the file, metadata refresh during request=%s', async (refreshDuringLoad) => {
  const request = pending<Response>();
  const fetchMock = vi.fn(() => request.promise);
  vi.stubGlobal('fetch', fetchMock);
  const slot = { ...baseSlot, artifact_value: { path: '/static-files/audit.md' } } as SlotRevision;
  const view = render(<DocumentArtifactEditor slot={slot} sessionId='audit-session' />);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  if (refreshDuringLoad) view.rerender(<DocumentArtifactEditor slot={{ ...slot, artifact_value: { path: '/static-files/audit.md' } }} sessionId='audit-session' />);
  await act(async () => request.resolve({ ok: true, text: async () => '# Loaded document' } as Response));
  expect(screen.queryByLabelText('Markdown draft')).toHaveValue('# Loaded document');
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

it('ignores an old file response after switching to another revision', async () => {
  const old = pending<Response>();
  vi.stubGlobal('fetch', vi.fn().mockReturnValueOnce(old.promise)
    .mockResolvedValueOnce({ ok: true, text: async () => '# New revision' }));
  const slot = { ...baseSlot, artifact_value: { path: '/static-files/old.md' } } as SlotRevision;
  const view = render(<DocumentArtifactEditor slot={slot} sessionId='audit-session' />);
  view.rerender(<DocumentArtifactEditor slot={{ ...slot, artifact_id: 'new-file', revision: 2, artifact_value: { path: '/static-files/new.md' } }} sessionId='audit-session' />);
  await waitFor(() => expect(screen.getByLabelText('Markdown draft')).toHaveValue('# New revision'));
  await act(async () => old.resolve({ ok: true, text: async () => '# Old revision' } as Response));
  expect(screen.getByLabelText('Markdown draft')).toHaveValue('# New revision');
});
