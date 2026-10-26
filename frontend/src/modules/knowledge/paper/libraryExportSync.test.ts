import { beforeEach, describe, expect, it, vi } from 'vitest';
import { loadPaperLibraryForExport } from './exportLibrary';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { loadPersistedPaperLibrary } from './store';
vi.mock('./store', () => ({ loadPersistedPaperLibrary: vi.fn() }));
beforeEach(() => vi.clearAllMocks());

describe('exporting the authoritative paper view', () => {
  it('exports the server view with its saved organization and current personal marks', async () => {
    const data = libraryFixture({ folders: [{ id: 'topic', name: 'Topic', parentId: null }], papers: [paperFixture({ id: 'paper', title: 'My edited title', folderId: 'topic', tags: ['RAG'] })] });
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data, revision: 4, canWrite: true, paperImportance: { paper: true } });
    const result = await loadPaperLibraryForExport('collection');
    expect(result.papers[0]).toMatchObject({ title: 'My edited title', folderId: 'topic', tags: ['RAG'], important: true });
    expect(result.folders).toEqual(data.folders);
  });
  it('reports server failures instead of exporting stale data', async () => {
    vi.mocked(loadPersistedPaperLibrary).mockRejectedValue(new Error('文献库读取失败'));
    await expect(loadPaperLibraryForExport('collection')).rejects.toThrow('文献库读取失败');
  });
  it('exports metadata-only records without PDF or write permission', async () => {
    const data = libraryFixture({ papers: [paperFixture({ title: 'Metadata only' })] });
    data.attachments = []; data.papers[0].attachmentIds = [];
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValue({ data, revision: 2, canWrite: false, paperImportance: {} });
    expect((await loadPaperLibraryForExport('collection')).papers[0].title).toBe('Metadata only');
  });
});
