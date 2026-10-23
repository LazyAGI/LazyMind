import { beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import { previewPaperImport, startReferenceImport } from './academicReferences';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn() } }));
beforeEach(() => vi.resetAllMocks());

it('passes the server preview receipt through selected items into asynchronous import', async () => {
  const candidate = { url: 'https://arxiv.org/pdf/2404.06654v2', source_provider: 'arxiv', version: 'v2' };
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'server-receipt', items: [{ work_id: 'a', reference_ids: [], fulltext_candidates: [candidate] }] } }).mockResolvedValueOnce({ data: { batch_id: 'batch' } });
  const items = await previewPaperImport(['a'], 'papers', 'expansion');
  await startReferenceImport('papers', [], items, 'request');
  expect(axiosInstance.post).toHaveBeenLastCalledWith('/api/core/academic/imports', expect.objectContaining({ preview_token: 'server-receipt', target_dataset_id: 'papers', items: [{ work_id: 'a', reference_ids: [], candidate }] }));
});
