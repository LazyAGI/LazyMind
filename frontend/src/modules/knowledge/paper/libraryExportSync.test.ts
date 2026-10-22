import { beforeEach, describe, expect, it, vi } from 'vitest';
import { loadPaperLibraryForExport } from './exportLibrary';
import { emptyLibraryV3 } from './libraryModel';
import { loadLibraryDocuments, mergeLibraryDocuments, type LibraryDocuments } from './libraryDocuments';
import { loadPersistedPaperLibrary } from './store';
vi.mock('./libraryDocuments', async original => ({ ...await original<typeof import('./libraryDocuments')>(), loadLibraryDocuments: vi.fn() }));
vi.mock('./store', () => ({ loadPersistedPaperLibrary: vi.fn() }));

function documents(ids = ['2404.06654']): LibraryDocuments {
  return { documents: ids.map(id => ({ document_id: `doc-${id}`, display_name: `${id}.pdf`, type: 'FILE' })),
    states: ids.map(id => ({ state: { document_id: `doc-${id}`, paper_id: `work-${id}`, kind: 'paper', status: 'succeeded', manual: true },
      paper: { paper_id: `work-${id}`, title: `Server Paper ${id}`, authors: ['A. Author'], arxiv_id: id, abstract: 'Original abstract' },
      source_provider: 'arxiv', source_version: 'v1', processing_status: 'ready', is_important: false })) };
}
beforeEach(() => {
  vi.mocked(loadPersistedPaperLibrary).mockReset().mockResolvedValue({ data: emptyLibraryV3(), revision: 1, canWrite: true });
  vi.mocked(loadLibraryDocuments).mockReset().mockResolvedValue(documents());
});

describe('shared document synchronization before export', () => {
  it('includes persisted PDFs in a clean browser without a separate arXiv listing', async () => {
    const library = await loadPaperLibraryForExport('collection');
    expect(library.papers).toHaveLength(1);
    expect(library.papers[0]).toMatchObject({ title: 'Server Paper 2404.06654', arxiv: '2404.06654' });
    expect(loadLibraryDocuments).toHaveBeenCalledWith('collection');
  });
  it('uses current document metadata and personal importance while preserving organization', async () => {
    const cached = mergeLibraryDocuments(emptyLibraryV3(), documents());
    cached.folders = [{ id: 'reading', name: 'My Reading' }];
    Object.assign(cached.papers[0], { title: 'Stale snapshot title', folderId: 'reading', tags: ['RAG'], important: true });
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data: cached, revision: 2, canWrite: true });
    vi.mocked(loadLibraryDocuments).mockResolvedValue(documents(['2404.06654', '2404.06655']));
    const library = await loadPaperLibraryForExport('collection');
    expect(library.papers).toHaveLength(2);
    expect(library.papers[0]).toMatchObject({ title: 'Server Paper 2404.06654', folderId: 'reading', tags: ['RAG'], important: false });
    expect(library.folders).toEqual(cached.folders);
  });
  it('honors removal markers when the same PDF remains in document storage', async () => {
    const cached = { ...emptyLibraryV3(), hiddenDocumentIds: ['doc-2404.06654'] };
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data: cached, revision: 2, canWrite: true });
    expect((await loadPaperLibraryForExport('collection')).papers).toEqual([]);
  });
  it('reports document synchronization failure rather than exporting stale metadata', async () => {
    const cached = mergeLibraryDocuments(emptyLibraryV3(), documents());
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data: cached, revision: 2, canWrite: true });
    vi.mocked(loadLibraryDocuments).mockRejectedValue(new Error('附件同步不可用'));
    await expect(loadPaperLibraryForExport('collection')).rejects.toThrow('附件同步不可用');
    expect(cached.papers).toHaveLength(1);
  });
  it('rejects malformed document results with an attachment-specific error', async () => {
    vi.mocked(loadLibraryDocuments).mockResolvedValue({} as LibraryDocuments);
    await expect(loadPaperLibraryForExport('collection')).rejects.toThrow('附件同步返回的数据不完整');
  });
  it('stops on library storage failure before reading attachments', async () => {
    vi.mocked(loadPersistedPaperLibrary).mockRejectedValue(new Error('文献库读取失败'));
    await expect(loadPaperLibraryForExport('collection')).rejects.toThrow('文献库读取失败');
    expect(loadLibraryDocuments).not.toHaveBeenCalled();
  });
  it('exports metadata-only entries without requiring a PDF or write permission', async () => {
    const data = mergeLibraryDocuments(emptyLibraryV3(), documents());
    data.papers[0].attachmentIds = []; data.attachments = [];
    vi.mocked(loadLibraryDocuments).mockResolvedValue({ documents: [], states: [] });
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data, revision: 1, canWrite: false });
    const library = await loadPaperLibraryForExport('collection');
    expect(library.papers).toHaveLength(1);
    expect(library.papers[0].title).toBe('Server Paper 2404.06654');
  });
});
