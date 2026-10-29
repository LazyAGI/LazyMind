import { describe, expect, it, vi } from 'vitest';
import { emptyLibrary } from './libraryModel';
import { executeZoteroImport, importZoteroSnapshot, storeZoteroAttachment } from './zoteroCommit';
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
    await expect(executeZoteroImport({ datasetId: 'ds', preview, config, options, getLibrary: emptyLibrary,
      saveLibrary: async () => { throw new Error('revision conflict'); }, storeAttachment,
    })).rejects.toThrow('revision conflict');
    expect(storeAttachment).not.toHaveBeenCalled();
  });

  it('keeps metadata and reports attachment errors so the same import can be retried', async () => {
    let current = emptyLibrary();
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
    let current = emptyLibrary();
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
      options: { includeAttachments: true, localFiles: [] }, getLibrary: emptyLibrary,
      saveLibrary: async () => {}, storeAttachment: upload });
    expect(response.failures?.[0]).toContain('未找到对应的本地 PDF');
    expect(upload).not.toHaveBeenCalled();
    expect(axiosInstance.post).not.toHaveBeenCalled();
  });

  it('stops before uploading if the user switched libraries after confirmation', async () => {
    let switched = false;
    const upload = vi.fn();
    await expect(executeZoteroImport({ datasetId: 'ds', preview, config, options,
      getLibrary: () => { if (switched) throw new Error('library switched'); return emptyLibrary(); },
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
    const library = { data: emptyLibrary(), paper_count: 0, pdf_attachment_count: 0, active: false, can_write: true };
    vi.mocked(axiosInstance.post).mockRejectedValueOnce({ response: { data: { message: 'parser unavailable', library } } });
    await expect(storeZoteroAttachment('ds', options.localFiles[0], candidate)).rejects.toMatchObject({
      message: 'parser unavailable', library: { data: library.data, paperCount: 0, pdfAttachmentCount: 0, active: false, canWrite: true },
    });
  });
});

it('submits normalized graph data atomically and decodes the authoritative snapshot', async () => {
  const data = emptyLibrary();
  const reply = { data, can_write: true, paper_count: 0, pdf_attachment_count: 0, active: false };
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: reply });
  expect(await importZoteroSnapshot('ds', data, 'users:42', 'stable-key')).toEqual({ data, canWrite: true, paperCount: 0, pdfAttachmentCount: 0, active: false });
  expect(axiosInstance.post).toHaveBeenLastCalledWith('/api/core/academic/zotero/import', { data, source_id: 'users:42', idempotency_key: 'stable-key' }, expect.objectContaining({ params: { target_dataset_id: 'ds' } }));
});

it('uses the authoritative paper identity returned after a concurrent metadata merge', async () => {
  let current = emptyLibrary();
  const uploaded: string[] = [];
  await executeZoteroImport({ datasetId: 'ds', preview, config, options, getLibrary: () => current,
    saveLibrary: async data => { current = { ...data, papers: data.papers.map(paper => ({ ...paper, id: 'existing' })), attachments: data.attachments.map(attachment => ({ ...attachment, paperId: 'existing' })) }; },
    storeAttachment: async (_file, candidate) => { uploaded.push(candidate.paperId); },
  });
  expect(uploaded).toEqual(['existing']);
});

it('transfers database PDFs on the server without downloading or uploading browser blobs', async () => {
  vi.mocked(axiosInstance.post).mockReset();
  vi.mocked(axiosInstance.post).mockImplementation(async (_url, body: any) => ({ data: { attachment_id: body.attachment_id, document_id: 'saved-pdf' } }));
  let current = emptyLibrary();
  const storeAttachment = vi.fn();
  const result = await executeZoteroImport({ datasetId: 'ds', preview, config: { source: 'database', library_type: 'users', library_id: '0' },
    options: { includeAttachments: true, localFiles: [] }, getLibrary: () => current,
    saveLibrary: async data => { current = data; }, storeAttachment,
  });
  expect(result).toMatchObject({ imported: 1, attachments: 1, failures: [] });
  expect(storeAttachment).not.toHaveBeenCalled();
  expect(axiosInstance.post).toHaveBeenCalledOnce();
  expect(axiosInstance.post).toHaveBeenCalledWith('/api/core/academic/zotero/attachment/transfer', expect.objectContaining({ source: 'database', item_key: 'FILE1234', paper_id: current.papers[0].id }), expect.not.objectContaining({ responseType: 'blob' }));
});

it('bounds concurrent transfers to three, drains failures and reports actual completions', async () => {
  vi.mocked(axiosInstance.post).mockReset();
  let active = 0, peak = 0;
  vi.mocked(axiosInstance.post).mockImplementation(async (_url, body: any) => {
    active++; peak = Math.max(peak, active);
    await new Promise(resolve => setTimeout(resolve, 10));
    active--;
    if (body.item_key === 'FILE0002') throw new Error('source missing');
    return { data: { attachment_id: body.attachment_id, document_id: body.item_key } };
  });
  const many = { ...preview, items: [preview.items[0], ...Array.from({ length: 8 }, (_, i) => ({ key: `FILE000${i}`, data: { key: `FILE000${i}`, itemType: 'attachment', parentItem: 'PAPER123', contentType: 'application/pdf', filename: `article-${i}.pdf` } }))] };
  let current = emptyLibrary();
  const progress = vi.fn();
  const result = await executeZoteroImport({ datasetId: 'ds', preview: many, config: { source: 'database', library_type: 'users', library_id: '0' },
    options: { includeAttachments: true, localFiles: [], onProgress: progress }, getLibrary: () => current,
    saveLibrary: async data => { current = data; }, storeAttachment: vi.fn(),
  });
  expect(peak).toBe(3); expect(active).toBe(0);
  expect(result.attachments).toBe(7); expect(result.failures).toEqual(['article-2.pdf：source missing']);
  expect(progress).toHaveBeenLastCalledWith('正在保存附件 8/8');
});
