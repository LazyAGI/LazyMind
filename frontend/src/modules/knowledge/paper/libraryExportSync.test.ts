import { beforeEach, describe, expect, it, vi } from 'vitest';
import { loadPaperLibraryForExport } from './exportLibrary';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { loadPersistedPaperLibrary } from './store';
vi.mock('./store', () => ({ loadPersistedPaperLibrary: vi.fn() }));
beforeEach(() => vi.clearAllMocks());
describe('exporting the authoritative paper view', () => {
    it('exports the server view with its saved organization and current personal marks', async () => {
        const data = libraryFixture({ folders: [{ id: 'topic', name: 'Topic', parentId: null }], papers: [paperFixture({ id: 'paper', important:true, folderId: 'topic', tags: ['RAG'], metadata: {
                        title: 'My edited title'
                    } })] });
        vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data, canWrite: true,paperCount:1,pdfAttachmentCount:0,active:false });
        const result = await loadPaperLibraryForExport('collection');
        expect(result.papers[0]).toMatchObject({ metadata:{title:'My edited title'}, folderId: 'topic', tags: ['RAG'], important: true });
        expect(result.folders).toEqual(data.folders);
    });
    it('reports server failures instead of exporting stale data', async () => {
        vi.mocked(loadPersistedPaperLibrary).mockRejectedValue(new Error('文献库读取失败'));
        await expect(loadPaperLibraryForExport('collection')).rejects.toThrow('文献库读取失败');
    });
    it('exports metadata-only records without PDF or write permission', async () => {
        const data = libraryFixture({ papers: [paperFixture({ metadata: {
                        title: 'Metadata only'
                    } })] });
        data.attachments = [];
        data.papers[0].attachmentIds = [];
        vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data, canWrite: false,paperCount:1,pdfAttachmentCount:0,active:false });
        expect((await loadPaperLibraryForExport('collection')).papers[0].metadata.title).toBe('Metadata only');
    });
});
