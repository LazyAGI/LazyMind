import { expect, it, vi } from 'vitest';
import { readLibraryViewCache, writeLibraryViewCache } from './libraryViewCache';
import { emptyLibrary } from './libraryModel';
const saved = { data: emptyLibrary(), canWrite: true, active: false, paperCount: 0, pdfAttachmentCount: 0 };
it('reuses only fresh snapshots from the same authenticated session', () => {
  writeLibraryViewCache('session-a', 'library', saved);
  expect(readLibraryViewCache('session-a', 'library')).toBe(saved);
  expect(readLibraryViewCache('session-b', 'library')).toBeUndefined();
  writeLibraryViewCache('session-b', 'library', saved);
  const now = Date.now();
  vi.spyOn(Date, 'now').mockReturnValue(now + 60001);
  expect(readLibraryViewCache('session-b', 'library')).toBeUndefined();
  vi.restoreAllMocks();
});
it('invalidates denied snapshots and bounds retained libraries', () => {
  for (const id of ['a', 'b', 'c']) writeLibraryViewCache('session', id, saved);
  expect(readLibraryViewCache('session', 'a')).toBeUndefined();
  writeLibraryViewCache('session', 'b', null);
  expect(readLibraryViewCache('session', 'b')).toBeUndefined();
  expect(readLibraryViewCache('', 'c')).toBeUndefined();
});
