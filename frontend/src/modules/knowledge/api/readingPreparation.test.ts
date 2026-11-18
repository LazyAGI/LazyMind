import { beforeEach, expect, it, vi } from 'vitest';
import { getDocumentParseStatus } from './pdfArtifacts';
import { getReadingPreparation, prepareReading, saveReadingPreferences, presetModes } from './readingPreparation';
const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => { vi.clearAllMocks(); http.get.mockResolvedValue({ data: { status: 'not_requested' } }); http.post.mockResolvedValue({ data: { data: { status: 'pending' } } }); http.put.mockResolvedValue({ data: { data: {} } }); });
it('observes chat parsing using GET only, including raw response format', async () => {
  expect(await getDocumentParseStatus('ds/a', 'doc b')).toEqual({ status: 'not_requested' });
  expect(http.get).toHaveBeenCalledWith('/api/core/datasets/ds%2Fa/documents/doc%20b/parse-status', expect.anything());
  expect(http.post).not.toHaveBeenCalled();
});
it('keeps progress observation separate from explicit preparation', async () => {
  await getReadingPreparation('ds', 'doc'); expect(http.post).not.toHaveBeenCalled();
  await prepareReading('ds', 'doc', ['ai_study']);
  expect(http.post).toHaveBeenCalledWith('/api/core/datasets/ds/documents/doc:prepare-reading', { scopes: ['ai_study'] });
});
it('saves only editable policy fields and sends both automatic categories', async () => {
  const policy = { preset: 'full' as const, modes: presetModes('full'), limits: { daily_pdf_documents: 9, daily_study_documents: 5, automatic_concurrency: 1 } };
  await saveReadingPreferences({ ...policy, capabilities: [] } as typeof policy);
  expect(http.put).toHaveBeenCalledWith('/api/core/user/reading-preferences', policy);
  expect(policy.modes).toEqual({ pdf_parse: 'automatic', ai_study: 'automatic' });
});
it('persists source-scoped provider references and clears them to inherit', async () => {
  const policy = { preset: 'demand' as const, modes: presetModes('demand'), limits: { daily_pdf_documents: 9, daily_study_documents: 5, automatic_concurrency: 1 }, parser_ref: 'own:parser', study_model_ref: 'shared:study' };
  await saveReadingPreferences(policy);
  expect(http.put).toHaveBeenLastCalledWith('/api/core/user/reading-preferences', policy);
  await saveReadingPreferences({ ...policy, parser_ref: '', study_model_ref: '' });
  expect(http.put).toHaveBeenLastCalledWith('/api/core/user/reading-preferences', { ...policy, parser_ref: '', study_model_ref: '' });
});
