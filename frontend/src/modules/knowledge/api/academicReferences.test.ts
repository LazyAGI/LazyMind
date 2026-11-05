import { beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import { addCitationToPaperLibrary, listAcademicImportBatches, previewPaperPDF, previewPaperImport, retryAcademicImport, startReferenceImport } from './academicReferences';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn(), get: vi.fn() } }));
beforeEach(() => vi.resetAllMocks());

it('passes the server preview receipt through selected items into asynchronous import', async () => {
  const candidate = { url: 'https://arxiv.org/pdf/2404.06654v2', source_provider: 'arxiv', version: 'v2' };
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'server-receipt', items: [{ work_id: 'a', reference_ids: [], fulltext_candidates: [candidate] }] } }).mockResolvedValueOnce({ data: { batch_id: 'batch' } });
  const items = await previewPaperImport(['a'], 'papers', 'expansion', 1, true);
  expect(axiosInstance.post).toHaveBeenCalledWith('/api/core/academic/imports:preview', { work_ids: ['a'], target_dataset_id: 'papers', expansion_id: 'expansion', expansion_version: 1, resolve_external: false }, { silentError: true });
  await startReferenceImport('papers', [], items, 'request', true);
  expect(axiosInstance.post).toHaveBeenLastCalledWith('/api/core/academic/imports', expect.objectContaining({ preview_token: 'server-receipt', target_dataset_id: 'papers', items: [{ work_id: 'a', reference_ids: [], candidate }] }), { silentError: true });
});

it('queries current imports on the server and sends the same retry request key', async () => {
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: { items: [], next_cursor: '', notification: { active: 0, needs_action: 0 } } });
  await listAcademicImportBatches('papers', 'current-cursor', 'current');
  expect(axiosInstance.get).toHaveBeenCalledWith('/api/core/academic/imports', { params: { target_dataset_id: 'papers', cursor: 'current-cursor', view: 'operations', filter: 'current' }, silentError: true });
  vi.mocked(axiosInstance.post).mockResolvedValue({ data: { status: 'accepted' } });
  await retryAcademicImport('batch', true, 'same-request');
  expect(axiosInstance.post).toHaveBeenCalledWith('/api/core/academic/imports/batch:retry', { idempotency_key: 'same-request' }, { silentError: true });
});

it('normalizes unavailable PDF candidates and preserves warnings and the metadata receipt', async () => {
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'metadata-only', items: [{ work_id: 'a', fulltext_candidates: null, warnings: ['Sci-Hub timeout'] }] } });
  const item = await previewPaperPDF('a', 'papers');
  expect(item.fulltext_candidates).toEqual([]);
  expect(item.preview_token).toBe('metadata-only');
  expect(item.warnings).toEqual(['Sci-Hub timeout']);
  expect(axiosInstance.post).toHaveBeenCalledWith('/api/core/academic/imports:preview', { work_ids: ['a'], target_dataset_id: 'papers', lookup_fulltext: true }, { silentError: true, timeout: 8000 });
});

it('verifies one cited work and imports the PDF into the collection root', async () => {
  const candidate = { url: 'https://arxiv.org/pdf/2404.06654v2', source_provider: 'arxiv', version: 'v2' };
  vi.mocked(axiosInstance.post)
    .mockResolvedValueOnce({ data: { preview_token: 'citation-receipt', items: [{ work_id: 'work-12', reference_ids: ['ref-12'], arxiv_id: '2404.06654', fulltext_candidates: [candidate] }] } })
    .mockResolvedValueOnce({ data: { batch_id: 'batch', status: 'pending' } });
  await expect(addCitationToPaperLibrary('papers', 'ref-12')).resolves.toEqual({ batch_id: 'batch', status: 'pending' });
  expect(axiosInstance.post).toHaveBeenNthCalledWith(1, '/api/core/academic/imports:preview', {
    reference_ids: ['ref-12'], target_dataset_id: 'papers', resolve_external: true, verify_reference: true, lookup_fulltext: true,
  }, expect.objectContaining({ silentError: true, timeout: 20000 }));
  expect(axiosInstance.post).toHaveBeenNthCalledWith(2, '/api/core/academic/imports', expect.objectContaining({
    entry_type: 'paper_link', target_dataset_id: 'papers', folder_id: '', preview_token: 'citation-receipt',
    items: [{ work_id: 'work-12', reference_ids: ['ref-12'], candidate }],
  }), { silentError: true });
});

it('does not create a metadata-only citation when no PDF source was found', async () => {
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'verified', items: [{ work_id: 'work-12', reference_ids: ['ref-12'], doi: '10.1234/paper', fulltext_candidates: [] }] } });
  await expect(addCitationToPaperLibrary('papers', 'ref-12')).rejects.toThrow('没有可导入的 PDF 来源');
  expect(axiosInstance.post).toHaveBeenCalledTimes(1);
});
