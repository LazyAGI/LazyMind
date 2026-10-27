import { beforeEach, describe, expect, it, vi } from 'vitest';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { emptyLibrary } from './libraryModel';
import { loadPersistedPaperLibrary, patchLibraryPaper, mergeLibraryChanges, confirmLibraryPdf, importLibraryPapers } from './store';
const api = vi.hoisted(() => ({ get: vi.fn(), request: vi.fn(), post: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: api }));
beforeEach(() => { vi.resetAllMocks(); localStorage.clear(); api.request.mockResolvedValue({ data: { data: emptyLibrary() } }); });
describe('entity-based paper storage', () => {
    it('loads authoritative metadata and read permission', async () => { const data = libraryFixture(); api.get.mockResolvedValue({ data: { data, can_write: false, paper_count: 1, pdf_attachment_count: 0, active: false } }); expect(await loadPersistedPaperLibrary('shared')).toMatchObject({ data, canWrite: false }); expect(api.request).not.toHaveBeenCalled(); });
    it('patches only one revision and preserves server conflicts', async () => { const paper = paperFixture(); await patchLibraryPaper('shared', paper, { status: 'read' }); expect(api.request.mock.calls[0][0]).toMatchObject({ method: 'patch', url: '/api/core/academic/library/papers/paper', data: { revision: 1, status: 'read' } }); const error = new Error('conflict'); api.request.mockRejectedValue(error); await expect(patchLibraryPaper('shared', paper, { status: 'read' })).rejects.toBe(error); expect(localStorage.length).toBe(0); });
    it('merges an updated entry in place without losing other entries', () => { const data = libraryFixture({ papers: [paperFixture({ id: 'a' }), paperFixture({ id: 'b' })] }); const fresh = { ...emptyLibrary(), papers: [{ ...data.papers[0], status: 'read' as const, revision: 2 }] }; const result = mergeLibraryChanges(data, { data: fresh }); expect(result.papers.map(p => p.id)).toEqual(['a', 'b']); expect(result.papers[0].revision).toBe(2); expect(result.papers[1]).toEqual(data.papers[1]); });
    it('uses the same import endpoint and key for PDF confirmation and bibliography', async () => { const paper = paperFixture(); await confirmLibraryPdf('shared', paper, 'doc', 'upload-key'); expect(api.request.mock.calls[0][0]).toMatchObject({ url: '/api/core/academic/imports', data: { entry_type: 'pdf_upload', idempotency_key: 'upload-key', items: [{ document_id: 'doc', revision: 1, confirmed: true }] } }); await importLibraryPapers('shared', [paper], [], 'bib-key', 'bib'); expect(api.request.mock.calls[1][0]).toMatchObject({ url: '/api/core/academic/imports', data: { entry_type: 'bibliography', idempotency_key: 'bib-key' } }); });
    it('rejects corrupt server data and propagates read failures', async () => { api.get.mockResolvedValue({ data: { can_write: true, data: { papers: [] } } }); await expect(loadPersistedPaperLibrary('shared')).rejects.toThrow('格式'); const error = new Error('database unavailable'); api.get.mockRejectedValue(error); await expect(loadPersistedPaperLibrary('shared')).rejects.toBe(error); });
});

it('keeps annotations, memberships and archival data through partial paper writes', () => {
    const data = libraryFixture();
    data.annotations = [{ id: 'a', paperId: data.papers[0].id, comment: 'Keep', color: 'yellow', createdAt: '' }];
    data.legacy = { original: 'preserved' };
    data.papers[0].collectionIds = ['collection'];
    const changed = { ...data.papers[0], revision: 2, status: 'read' as const };
    const result = mergeLibraryChanges(data, { data: { ...emptyLibrary(), papers: [changed] } });
    expect(result.annotations).toEqual(data.annotations);
    expect(result.legacy).toEqual(data.legacy);
    expect(result.papers[0].collectionIds).toEqual(['collection']);
});

it('migrates a browser-only legacy graph through the server without erasing its original copy', async () => {
    const legacy = { schemaVersion: 3, folders: [], papers: [{ id: 'old', title: 'Browser record', authors: 'Ada', year: 2024, venue: 'Journal', metadata: { identifiers: {} }, attachmentIds: [], noteIds: [], tags: [] }], attachments: [], notes: [], annotations: [{ id: 'a', paperId: 'old', comment: 'Keep' }] };
    const raw = JSON.stringify(legacy);
    localStorage.setItem('lazymind.paper-library.data.v3.shared', raw);
    api.get.mockResolvedValue({ data: { data: emptyLibrary(), can_write: true, paper_count: 0, pdf_attachment_count: 0, active: false } });
    api.post.mockImplementation(async (_url, input) => ({ data: { data: { ...input.data, papers: input.data.papers.map((paper: object) => ({ ...paper, revision: 1 })) }, can_write: true, paper_count: 1, pdf_attachment_count: 0, active: false } }));
    const result = await loadPersistedPaperLibrary('shared');
    expect(result.data.papers[0]?.metadata.title).toBe('Browser record');
    expect(result.data.annotations).toEqual(legacy.annotations);
    expect(api.post).toHaveBeenCalledOnce();
    expect(localStorage.getItem('lazymind.paper-library.data.v3.shared')).toBe(raw);
});
