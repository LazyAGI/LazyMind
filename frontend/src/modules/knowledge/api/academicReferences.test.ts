import { beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import { listAcademicImportBatches, previewPaperImport, retryAcademicImport, startReferenceImport } from './academicReferences';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn(), get: vi.fn() } }));
beforeEach(() => vi.resetAllMocks());

it('passes the server preview receipt through selected items into asynchronous import', async () => {
  const candidate = { url: 'https://arxiv.org/pdf/2404.06654v2', source_provider: 'arxiv', version: 'v2' };
  vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { preview_token: 'server-receipt', items: [{ work_id: 'a', reference_ids: [], fulltext_candidates: [candidate] }] } }).mockResolvedValueOnce({ data: { batch_id: 'batch' } });
  const items = await previewPaperImport(['a'], 'papers', 'expansion');
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
