import { describe, expect, it } from 'vitest';
import { emptyLibraryV3 } from './libraryModel';
import { mergeLibraryDocuments, paperDocument, personalLibrary, type LibraryDocuments } from './libraryDocuments';

const source: LibraryDocuments = {
  documents: [
    { document_id: 'folder', display_name: '旧目录', type: 'FOLDER' },
    { document_id: 'pdf', display_name: 'paper.pdf', p_id: 'folder', document_stage: 'DOCUMENT_PARSE_SUCCESSFULLY' },
  ],
  states: [{ state: { document_id: 'pdf', kind: 'paper', status: 'succeeded', paper_id: 'work', manual: true },
    paper: { paper_id: 'work', title: 'Confirmed paper', doi: '10.1234/paper', abstract: null }, is_important: false }],
};

describe('existing PDF and Workflow bindings', () => {
  it('imports existing directories and keeps one paper and attachment across refreshes', () => {
    const first = mergeLibraryDocuments(emptyLibraryV3(), source);
    expect(first.papers).toHaveLength(1);
    expect(first.folders[0].name).toBe('旧目录');
    expect(first.attachments[0].contentRef).toBe('pdf');
    expect(first.papers[0]).toMatchObject({ title: 'Confirmed paper', academicWorkId: 'work', parse: 'ready' });
    expect(mergeLibraryDocuments(first, source)).toEqual(first);
    expect(paperDocument(first, first.papers[0].id, source.states)?.state.document_id).toBe('pdf');
  });

  it('keeps library reading preferences while using current document metadata and personal importance', () => {
    const library = mergeLibraryDocuments(emptyLibraryV3(), source);
    library.papers[0].important = true;
    library.papers[0].status = 'read';
    library.papers[0].tags = ['my tag'];
    const refreshed = mergeLibraryDocuments(library, source);
    expect(refreshed.papers[0]).toMatchObject({ status: 'read', tags: ['my tag'], important: true });
    expect(personalLibrary(refreshed, source.states).papers[0].important).toBe(false);
    expect(refreshed.papers[0].important).toBe(true);
    const otherUser = source.states.map(item => ({ ...item, is_important: true }));
    expect(personalLibrary(refreshed, otherUser).papers[0].important).toBe(true);
  });

  it('does not resurrect a removed PDF when an import or recognition refresh completes', () => {
    const library = { ...emptyLibraryV3(), hiddenDocumentIds: ['pdf'] };
    expect(mergeLibraryDocuments(library, source).papers).toEqual([]);
  });

  it('retains a confirmed identity when another attachment is still awaiting recognition', () => {
    const library = mergeLibraryDocuments(emptyLibraryV3(), source);
    library.attachments.push({ ...library.attachments[0], id: 'pending-attachment', contentRef: 'pending-pdf' });
    library.papers[0].attachmentIds.push('pending-attachment');
    const refreshed = mergeLibraryDocuments(library, { ...source, documents: [...source.documents,
      { document_id: 'pending-pdf', display_name: 'pending.pdf' }] });
    expect(refreshed.papers).toHaveLength(1);
    expect(refreshed.papers[0].academicWorkId).toBe('work');
    expect(paperDocument(refreshed, refreshed.papers[0].id, source.states)?.state.document_id).toBe('pdf');
  });

  it('keeps authoritative arXiv parsing failures when the ordinary document status is stale', () => {
    const library = mergeLibraryDocuments(emptyLibraryV3(), source);
    library.attachments[0].source = 'arxiv';
    library.papers[0].parse = 'failed';
    library.papers[0].processingError = 'parser unavailable';
    expect(mergeLibraryDocuments(library, source).papers[0]).toMatchObject({ parse: 'failed', processingError: 'parser unavailable' });
  });
});
