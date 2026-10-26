import { expect, it, vi } from 'vitest';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { savePersistedPaperLibrary } from './store';
import { loadPaperLibraryForExport } from './exportLibrary';

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: api }));


it('preserves known, zero, and unknown citation counts through server persistence and export loading', async () => {
  const data = libraryFixture({ papers: [
    paperFixture({ id: 'cited', citationCount: 42 }),
    paperFixture({ id: 'uncited', citationCount: 0 }),
    paperFixture({ id: 'unknown' }),
  ] });
  let saved = { data, revision: 0, can_write: true };
  api.put.mockImplementation(async (_url, body) => {
    saved = { data: JSON.parse(JSON.stringify(body.data)), revision: 1, can_write: true };
    return { data: saved };
  });
  api.get.mockImplementation(async () => ({ data: saved }));

  await savePersistedPaperLibrary('papers', data, 0);
  const exported = await loadPaperLibraryForExport('papers');
  expect(exported.papers.map(paper => paper.citationCount)).toEqual([42, 0, undefined]);
  expect(exported.papers[2]).not.toHaveProperty('citationCount');
});
