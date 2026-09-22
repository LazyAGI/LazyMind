import { useContext, useEffect, useState, type ReactNode } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { SlotEditingContext } from './slotEditingContext';
import { SlotRenderer } from './SlotComponents';

const api = vi.hoisted(() => ({ getSlotItemVersions: vi.fn(), rollbackSlotItem: vi.fn(), getSlots: vi.fn(), saveDocumentArtifact: vi.fn() }));
vi.mock('@/modules/chat/utils/request', async original => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(), WorkflowSessionApi: () => api,
}));
vi.mock('./useDocumentCopy', () => ({ useDocumentCopy: () => {} }));
vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => ({ states: {} }) }));
vi.mock('./ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null }));
vi.mock('./MarkdownArtifactEditor', () => ({ MarkdownArtifactEditor: (props: any) => <TestEditor {...props} /> }));
vi.mock('./WriterIRControl', () => ({ WriterIRControl: (props: any) => <TestEditor {...props} markdown={props.document.title}
  onContentChange={(text: string) => props.onDocumentChange({ ...props.document, title: text })}
  onSave={(text: string, revision: number) => props.onSave(props.document, { ...props.document, title: text }, revision, 'draft')} /> }));

function TestEditor({ markdown, sourceRevision, onContentChange, onSave, toolbarActions, editingKey, readOnly }: {
  markdown: string; sourceRevision: number; onContentChange: (text: string) => void;
  onSave: (text: string, revision: number, mode?: string) => Promise<unknown>; toolbarActions?: ReactNode; editingKey: string; readOnly: boolean;
}) {
  const [draft, setDraft] = useState(markdown);
  const { registerFlush } = useContext(SlotEditingContext);
  useEffect(() => setDraft(markdown), [markdown, sourceRevision]);
  useEffect(() => registerFlush(editingKey, async () => {
    if (draft !== markdown) await onSave(draft, sourceRevision, 'draft');
    return true;
  }), [draft, editingKey, markdown, onSave, registerFlush, sourceRevision]);
  return <><div className='writer-document-toolbar'>{toolbarActions}</div><textarea aria-label='Document draft' value={draft} readOnly={readOnly}
    onChange={event => { setDraft(event.target.value); onContentChange(event.target.value); }} />
    <button disabled={readOnly} onClick={() => void onSave(draft, sourceRevision, 'draft')}>Save test draft</button></>;
}

const ir = (title: string) => ({ document_id: 'fixture-document', stage: 'draft', title, blocks: [] });
const slotFor = (representation: 'markdown' | 'ir', revision = 3): SlotRevision => ({
  artifact_id: `artifact-${revision}`, slot_id: 'flat_draft_document', list_index: -1, revision, draft_version: revision + 1,
  selected: true, change_source: 'ai', artifact_value: { data: representation === 'ir' ? ir(`Version ${revision}`) : `Version ${revision}` },
  document: { representation, editable: true, capabilities: ['save'] },
} as SlotRevision);
beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  api.getSlotItemVersions.mockResolvedValue({ data: { data: { versions: [1, 2, 3].map(revision => ({
    revision, version: revision, change_source: 'ai', selected: revision === 3,
    created_at: '2026-09-16T00:00:00Z', content_snapshot: { text: `Version ${revision}` },
  })) } } });
  api.rollbackSlotItem.mockResolvedValue({ data: { data: { revision: 1 } } });
  api.getSlots.mockResolvedValue({ data: { data: { slots: [slotFor('markdown', 1)] } } });
  api.saveDocumentArtifact.mockImplementation(async (_id, body) => ({ data: { ok: true, result: {
    artifact_id: 'saved-draft', revision: body.base_revision + 1, draft_version: 1, value: body.value,
  } } }));
});
afterEach(cleanup);
const show = (slot: SlotRevision, readOnly = false) => render(<SlotRenderer slot={slot} sessionId='history-fixture'
  slotId='flat_draft_document' revisionCount={3} readOnly={readOnly} />);
async function openHistory() {
  fireEvent.click(await screen.findByRole('button', { name: '版本历史', exact: true }));
  return screen.findByRole('dialog');
}
async function applyFirstVersion() {
  const dialog = await openHistory();
  fireEvent.click(within(dialog).getAllByRole('option').at(-1)!);
  fireEvent.click(within(dialog).getByRole('button', { name: /应用.*1/ }));
}

it.each(['markdown', 'ir'] as const)('shows history in the %s toolbar and previews version differences', async representation => {
  const view = show(slotFor(representation));
  const dialog = await openHistory();
  expect(view.container.querySelector('.writer-document-toolbar')).toContainElement(screen.getByRole('button', { name: '版本历史', exact: true }));
  expect(api.getSlotItemVersions).toHaveBeenCalledWith('history-fixture', 'flat_draft_document', -1);
  expect(within(dialog).getAllByRole('option')).toHaveLength(3);
  await waitFor(() => expect(dialog).toHaveTextContent('Version'));
  fireEvent.click(within(dialog).getByRole('button', { name: '关闭版本历史' }));
  expect(api.rollbackSlotItem).not.toHaveBeenCalled();
  expect(screen.getByLabelText('Document draft')).toHaveValue('Version 3');
});

it.each(['markdown', 'ir'] as const)('saves edits before rollback and uses the selected artifact for the next %s save', async representation => {
  api.getSlots.mockResolvedValue({ data: { data: { slots: [slotFor(representation, 1)] } } });
  show(slotFor(representation));
  fireEvent.change(await screen.findByLabelText('Document draft'), { target: { value: 'Unsaved edits' } });
  await applyFirstVersion();
  await waitFor(() => expect(screen.getByLabelText('Document draft')).toHaveValue('Version 1'));
  expect(api.saveDocumentArtifact.mock.invocationCallOrder[0]).toBeLessThan(api.rollbackSlotItem.mock.invocationCallOrder[0]);
  fireEvent.change(screen.getByLabelText('Document draft'), { target: { value: 'Edit restored version' } });
  await act(async () => { fireEvent.click(screen.getByText('Save test draft')); });
  await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenLastCalledWith('artifact-1', expect.objectContaining({
    base_revision: 1, base_draft_version: 2,
  }), expect.anything()));
});

it('keeps history readable but prevents rollback for a read-only document', async () => {
  show(slotFor('markdown'), true);
  const dialog = await openHistory();
  fireEvent.click(within(dialog).getAllByRole('option').at(-1)!);
  expect(within(dialog).getByRole('button', { name: /应用.*1/ })).toBeDisabled();
  expect(api.rollbackSlotItem).not.toHaveBeenCalled();
});

it('preserves the draft and does not roll back when saving fails', async () => {
  api.saveDocumentArtifact.mockRejectedValue(new Error('fixture save failure'));
  show(slotFor('markdown'));
  fireEvent.change(await screen.findByLabelText('Document draft'), { target: { value: 'Keep this draft' } });
  await applyFirstVersion();
  expect(await screen.findByRole('alert')).toBeInTheDocument();
  expect(api.rollbackSlotItem).not.toHaveBeenCalled();
  expect(screen.getByLabelText('Document draft')).toHaveValue('Keep this draft');
});

it('blocks stale editing until a failed restored-document load is retried', async () => {
  api.getSlots.mockRejectedValueOnce(new Error('fixture read failure'));
  show(slotFor('markdown'));
  await applyFirstVersion();
  await waitFor(() => expect(screen.getByText('Save test draft')).toBeDisabled());
  fireEvent.click(await screen.findByRole('button', { name: '关闭版本历史' }));
  const retry = await screen.findByRole('button', { name: '重试' });
  fireEvent.click(retry);
  fireEvent.click(retry);
  await waitFor(() => expect(screen.getByLabelText('Document draft')).toHaveValue('Version 1'));
  expect(screen.getByText('Save test draft')).not.toBeDisabled();
  expect(api.rollbackSlotItem).toHaveBeenCalledTimes(1);
  expect(api.getSlots).toHaveBeenCalledTimes(2);
});

it('ignores a late save refresh after a deliberate rollback', async () => {
  const view = show(slotFor('markdown'));
  fireEvent.change(await screen.findByLabelText('Document draft'), { target: { value: 'Saved before rollback' } });
  await applyFirstVersion();
  await waitFor(() => expect(screen.getByLabelText('Document draft')).toHaveValue('Version 1'));
  view.rerender(<SlotRenderer slot={{ ...slotFor('markdown', 4), artifact_id: 'saved-draft', draft_version: 1 }}
    sessionId='history-fixture' slotId='flat_draft_document' revisionCount={4} />);
  expect(screen.getByLabelText('Document draft')).toHaveValue('Version 1');
});
