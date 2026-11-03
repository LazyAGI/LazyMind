import { beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import { previewPaperPDF, previewPaperImport, startReferenceImport } from './academicReferences';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn() } }));
beforeEach(() => vi.resetAllMocks());

it('passes the server preview receipt through selected items into asynchronous import', async () => {
  const candidate = { url: 'https://arxiv.org/pdf/2404.06654v2', source_provider: 'arxiv', version: 'v2' };
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'server-receipt', items: [{ work_id: 'a', reference_ids: [], fulltext_candidates: [candidate] }] } }).mockResolvedValueOnce({ data: { batch_id: 'batch' } });
  const items = await previewPaperImport(['a'], 'papers', 'expansion');
  await startReferenceImport('papers', [], items, 'request', true);
  expect(axiosInstance.post).toHaveBeenLastCalledWith('/api/core/academic/imports', expect.objectContaining({ preview_token: 'server-receipt', target_dataset_id: 'papers', items: [{ work_id: 'a', reference_ids: [], candidate }] }), { silentError: true });
});


it('normalizes unavailable PDF candidates and preserves warnings and the metadata receipt', async () => {
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'metadata-only', items: [{ work_id: 'a', fulltext_candidates: null, warnings: ['Sci-Hub timeout'] }] } });
  const item = await previewPaperPDF('a', 'papers');
  expect(item.fulltext_candidates).toEqual([]);
  expect(item.preview_token).toBe('metadata-only');
  expect(item.warnings).toEqual(['Sci-Hub timeout']);
  expect(axiosInstance.post).toHaveBeenCalledWith('/api/core/academic/imports:preview', { work_ids: ['a'], target_dataset_id: 'papers', lookup_fulltext: true }, { silentError: true, timeout: 8000 });
});
