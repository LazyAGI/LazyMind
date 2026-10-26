import { describe, expect, it } from 'vitest';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { paperDocument, restoreLibraryPaper } from './libraryDocuments';
import type { DocumentAcademic } from '../api/paperWorkflows';

describe('paper document selection and explicit restoration', () => {
  it('uses a confirmed available attachment when another is pending', () => {
    const data = libraryFixture();
    data.attachments[0].contentRef = 'pending';
    data.attachments.push({ ...data.attachments[0], id: 'confirmed', contentRef: 'pdf' });
    data.papers[0].attachmentIds.push('confirmed');
    const states: DocumentAcademic[] = [
      { state: { document_id: 'pending', kind: 'unknown', status: 'pending', paper_id: '', manual: false }, is_important: false },
      { state: { document_id: 'pdf', kind: 'paper', status: 'succeeded', paper_id: 'work', manual: true }, is_important: false },
    ];
    expect(paperDocument(data, data.papers[0].id, states)?.state.document_id).toBe('pdf');
    data.attachments[1].available = false;
    expect(paperDocument(data, data.papers[0].id, states)?.state.document_id).toBe('pending');
  });
  it('restores a removed paper once while preserving its notes and attachment references', () => {
    const data = libraryFixture({ papers: [paperFixture({ academicWorkId: 'work' })] });
    data.attachments[0].contentRef = 'pdf';
    data.papers[0].noteIds = ['note'];
    data.notes = [{ id: 'note', paperId: 'paper', title: 'Notes', content: 'Keep this result', kind: 'note', createdAt: '', updatedAt: '' }];
    const removed = { ...data, papers: [], hiddenArxivWorkIds: ['work'], hiddenArxivPapers: [data.papers[0]], hiddenDocumentIds: ['pdf'] };
    const restored = restoreLibraryPaper(removed, 'work');
    expect(restored.papers[0].noteIds).toEqual(['note']);
    expect(restored.notes).toEqual(data.notes);
    expect(restored.hiddenDocumentIds).toEqual([]);
    expect(restoreLibraryPaper(restored, 'work').papers).toHaveLength(1);
  });
});
