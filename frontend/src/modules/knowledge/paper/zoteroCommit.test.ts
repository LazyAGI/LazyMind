import { describe, expect, it, vi } from 'vitest';
import { emptyLibraryV3 } from './libraryModel';
import { executeZoteroImport, storeZoteroAttachment } from './zoteroCommit';
import { axiosInstance } from '@/components/request';
import type { ZoteroPreview } from './zoteroImport';

vi.mock('@/components/request', () => ({ axiosInstance: { post: vi.fn() }, BASE_URL: '' }));

const preview: ZoteroPreview = { source_id: 'zotero:users:1', warnings: [], collections: [], items: [
  { key: 'PAPER123', data: { key: 'PAPER123', itemType: 'journalArticle', title: 'A paper' } },
  { key: 'FILE1234', data: { key: 'FILE1234', itemType: 'attachment', parentItem: 'PAPER123', contentType: 'application/pdf', filename: 'article.pdf' } },
] };
const config = { source: 'local' as const, library_type: 'users' as const, library_id: '0' };
const options = { includeAttachments: true, localFiles: [new File(['%PDF-1.7'], 'article.pdf', { type: 'application/pdf' })] };

describe('confirmed Zotero imports', () => {
  it('does not upload files when saving the confirmed metadata fails', async () => {
    const storeAttachment = vi.fn();
    await expect(executeZoteroImport({ datasetId: 'ds', preview, config, options, getLibrary: emptyLibraryV3,
      saveLibrary: async () => { throw new Error('revision conflict'); }, storeAttachment,
    })).rejects.toThrow('revision conflict');
    expect(storeAttachment).not.toHaveBeenCalled();
  });

  it('keeps metadata and reports attachment errors so the same import can be retried', async () => {
    let current = emptyLibraryV3();
    let reject = true;
    const storeAttachment = vi.fn(async (_file, candidate) => {
      if (reject) throw new Error('storage unavailable');
      current = { ...current, attachments: current.attachments.map(item => item.id === candidate.id ? { ...item, contentRef: 'durable-document' } : item) };
    });
    const input = { datasetId: 'ds', preview, config, options, getLibrary: () => current,
      saveLibrary: async (data: typeof current) => { current = data; }, storeAttachment };
    const first = await executeZoteroImport(input);
    expect(current.papers).toHaveLength(1);
    expect(current.attachments[0].contentRef).toBeUndefined();
    expect(first.failures).toEqual(['article.pdf：storage unavailable']);
    reject = false;
    expect((await executeZoteroImport(input)).failures).toEqual([]);
    expect(current.papers).toHaveLength(1);
    expect(current.attachments).toHaveLength(1);
    expect(current.attachments[0].contentRef).toBe('durable-document');
    await executeZoteroImport(input);
    expect(storeAttachment).toHaveBeenCalledTimes(2);
  });

  it('imports metadata only when attachment import is disabled', async () => {
    let current = emptyLibraryV3();
    const storeAttachment = vi.fn();
    const result = await executeZoteroImport({ datasetId: 'ds', preview, config, options: { ...options, includeAttachments: false },
      getLibrary: () => current, saveLibrary: async data => { current = data; }, storeAttachment });
    expect(result.imported).toBe(1);
    expect(result.attachments).toBe(0);
    expect(current.papers).toHaveLength(1);
    expect(storeAttachment).not.toHaveBeenCalled();
  });

  it('reports missing local attachments without silently downloading another source', async () => {
    const upload = vi.fn();
    const response = await executeZoteroImport({ datasetId: 'ds', preview, config,
      options: { includeAttachments: true, localFiles: [] }, getLibrary: emptyLibraryV3,
      saveLibrary: async () => {}, storeAttachment: upload });
    expect(response.failures?.[0]).toContain('未找到对应的本地 PDF');
    expect(upload).not.toHaveBeenCalled();
    expect(axiosInstance.post).not.toHaveBeenCalled();
  });

  it('stops before uploading if the user switched libraries after confirmation', async () => {
    let switched = false;
    const upload = vi.fn();
    await expect(executeZoteroImport({ datasetId: 'ds', preview, config, options,
      getLibrary: () => { if (switched) throw new Error('library switched'); return emptyLibraryV3(); },
      saveLibrary: async () => { switched = true; }, storeAttachment: upload,
    })).rejects.toThrow('library switched');
    expect(upload).not.toHaveBeenCalled();
  });
});

describe('durable Zotero attachment responses', () => {
  const candidate = { id: 'attachment', paperId: 'paper', key: 'FILE1234', name: 'article.pdf' };
  it('rejects invalid success payloads instead of claiming a completed upload', async () => {
    vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { success: false } });
    await expect(storeZoteroAttachment('ds', options.localFiles[0], candidate)).rejects.toThrow('响应格式无效');
  });
  it('keeps the saved snapshot while surfacing a parsing failure', async () => {
    const library = { data: emptyLibraryV3(), revision: 7, can_write: true };
    vi.mocked(axiosInstance.post).mockRejectedValueOnce({ response: { data: { message: 'parser unavailable', library } } });
    await expect(storeZoteroAttachment('ds', options.localFiles[0], candidate)).rejects.toMatchObject({
      message: 'parser unavailable', library: { data: library.data, revision: 7, canWrite: true },
    });
  });
});
