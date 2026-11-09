import { beforeEach, expect, it, vi } from 'vitest';
import { getDocumentParseStatus } from './pdfArtifacts';
import { getReadingPreparation, prepareReading, saveReadingPreferences, presetModes, readingDependencyError } from './readingPreparation';
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
  await prepareReading('ds', 'doc', ['formula']);
  expect(http.post).toHaveBeenCalledWith('/api/core/datasets/ds/documents/doc:prepare-reading', { scopes: ['formula'] });
});
it('saves only editable policy fields and keeps chat independent from full preset', async () => {
  const policy = { preset: 'full' as const, modes: presetModes('full'), limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 } };
  await saveReadingPreferences({ ...policy, capabilities: [] } as typeof policy);
  expect(http.put).toHaveBeenCalledWith('/api/core/user/reading-preferences', policy);
  expect(policy.modes.chat_prepare).toBe('on_demand');
  expect(readingDependencyError(policy)).toBeUndefined();
  expect(readingDependencyError({ ...policy, modes: { ...policy.modes, pdf_parse: 'disabled' } })).toContain('结构解析');
});
it('persists source-scoped provider references and clears them to inherit', async () => {
  const policy = { preset: 'demand' as const, modes: presetModes('demand'), limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 }, parser_ref: 'own:parser', study_model_ref: 'shared:study' };
  await saveReadingPreferences(policy);
  expect(http.put).toHaveBeenLastCalledWith('/api/core/user/reading-preferences', policy);
  await saveReadingPreferences({ ...policy, parser_ref: '', study_model_ref: '' });
  expect(http.put).toHaveBeenLastCalledWith('/api/core/user/reading-preferences', { ...policy, parser_ref: '', study_model_ref: '' });
});
