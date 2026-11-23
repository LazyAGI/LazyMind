import { useContext } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { PptPageEditContext, PptPageEditor } from './PptPageEditor';

const { savePptPage, upload } = vi.hoisted(() => ({savePptPage: vi.fn(), upload: vi.fn()}));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({ savePptPage }) }));
vi.mock('@/modules/chat/utils/chunkUpload', () => ({ uploadFileInChunks: upload }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

function Fields() {
  const page = useContext(PptPageEditContext)!;
  return <>
    <button onClick={() => page.update('slide', 'edited ' + page.slide.revision, page.slide.artifact_value.text)}>
      Slide {page.slide.revision}
    </button>
    <button onClick={() => page.update('notes', 'notes update', 'original')}>Notes</button>
  </>;
}

const slide: SlotRevision = {
  slot_id: 'preview_html', slot: 'preview_html', revision: 1, list_index: 3,
  selected: true, created_at: '', artifact_value: {text: 'original'},
};
const notes = {...slide, slot_id: 'preview_notes', slot: 'preview_notes', list_index: 6,
  artifact_value: {type: 'text', path: 'files/notes.md'}};

it.each([false, true])('uses the committed baseline on the next save (refresh rejects: %s)', async rejects => {
  upload.mockResolvedValue('files/updated.md');
  savePptPage.mockImplementation(async (_session, _index, body) => {
    const revision = body.slide.revision + 1;
    return { data: {code: 0, data: {
      revision, notes_revision: 2, state_version: revision,
      changes: [slide, ...(body.notes.file ? [notes] : [])].map(slot => ({
        slot_id: slot.slot_id, revision, artifact_id: slot.slot_id + revision, draft_version: 1,
      })),
    }}};
  });
  const refresh = rejects ? vi.fn().mockRejectedValue(new Error('offline')) : undefined;
  render(<PptPageEditor sessionId="ppt" slide={slide} notes={notes} stateVersion={1}
    refresh={refresh} flushRef={{current: null}}><Fields /></PptPageEditor>);
  fireEvent.click(screen.getByText('Slide 1'));
  fireEvent.click(screen.getByText('Notes'));
  fireEvent.click(screen.getByText('保存本页'));
  await screen.findByText('Slide 2');
  expect(upload).toHaveBeenCalledTimes(1);
  expect(savePptPage.mock.calls[0][2].notes).toEqual({
    list_index: 6, revision: 1, file: {path: 'files/updated.md', size: 12},
  });
  fireEvent.click(screen.getByText('Slide 2'));
  fireEvent.click(screen.getByText('保存本页'));
  await waitFor(() => expect(screen.getByText('Slide 3')).toBeInTheDocument());
  expect(savePptPage.mock.calls[1][2]).toMatchObject({
    slide: {revision: 2, draft_version: 1}, notes: {revision: 2, draft_version: 1},
  });
  expect(upload).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole('alert')).toBeNull();
});
