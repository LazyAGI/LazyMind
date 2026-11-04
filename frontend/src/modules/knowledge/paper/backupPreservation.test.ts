import { expect, it } from 'vitest';
import * as exports from './exportLibrary';
import { libraryFixture } from './__tests__/fixtures';

it('round trips the complete normalized library including legacy and Zotero graph data', () => {
  expect(exports.serializeLibraryBackup).toBeTypeOf('function');
  const data = { ...libraryFixture(), annotations: [{ id: 'annotation', paperId: 'paper', quote: 'Result', comment: 'Keep', color: 'yellow' as const, createdAt: '' }], collections: [{ id: 'collection', name: 'Nested', parentId: 'root' }], relations: [{ id: 'related', fromPaperId: 'paper', toPaperId: 'other', kind: 'related' as const }], legacy: { schemaVersion: 3, original: 'preserved' } };
  data.papers[0].zoteroSources = ['users:1:ITEM'];
  data.papers[0].metadata.citationCount = 123;
  expect(exports.parseLibraryBackup(exports.serializeLibraryBackup(data))).toMatchObject(data);
});
it('exports normalized author names and identifiers as CSL JSON', () => {
  expect(exports.toCslJson).toBeTypeOf('function');
  const data = libraryFixture();
  data.papers[0].metadata = { title: 'Study', authors: ['Ada Lovelace'], venue: 'Journal', year: 2024, identifiers: { doi: '10.1/study', arxiv: '2401.12345' } };
  expect(JSON.parse(exports.toCslJson(data.papers))[0]).toMatchObject({ title: 'Study', author: [{ literal: 'Ada Lovelace' }], DOI: '10.1/study', URL: 'https://arxiv.org/abs/2401.12345' });
});

it('reads old schema 3 backups without losing original graph data', () => {
  const old = { schemaVersion: 3, folders: [], attachments: [], notes: [], annotations: [{ id: 'a', comment: 'Keep' }], papers: [{ id: 'p', title: 'Legacy', authors: 'Ada; Grace', venue: 'Journal', year: 2024, doi: '10.1/legacy', citationCount: 12, metadata: { identifiers: { pmid: '123' }, confidence: { title: 1 } }, attachmentIds: [], noteIds: [], tags: [], collectionIds: ['c'], zoteroSources: ['users:1:P'] }] };
  const restored = exports.parseLibraryBackup(JSON.stringify(old));
  expect(restored.papers[0].metadata).toMatchObject({ title: 'Legacy', authors: ['Ada', 'Grace'], identifiers: { doi: '10.1/legacy', pmid: '123' }, citationCount: 12, confidence: { title: 1 } });
  expect(restored.papers[0].collectionIds).toEqual(['c']);
  expect(restored.annotations).toEqual(old.annotations);
  expect(restored.legacy).toEqual(old);
});

it('restores metadata across libraries without attaching another library PDF and keeps the original backup', () => {
  expect(exports.prepareLibraryRestore).toBeTypeOf('function');
  const data = { ...libraryFixture(), backupDatasetId: 'original' };
  data.attachments[0].contentRef = 'original-document';
  const restored = exports.prepareLibraryRestore(data, 'destination', false, 'restore-key');
  expect(restored.attachments[0].contentRef).toBeUndefined();
  expect(restored.attachments[0].available).toBe(false);
  expect(restored.migrationSnapshots?.[0]).toMatchObject({ id: 'restore-key', source: 'library-backup', data });
  expect(data.attachments[0].contentRef).toBeTruthy();
});
it('restores same-library PDF references when explicitly included', () => {
  expect(exports.prepareLibraryRestore).toBeTypeOf('function');
  const data = { ...libraryFixture(), backupDatasetId: 'same' };
  data.attachments[0].contentRef = 'same-document';
  expect(exports.prepareLibraryRestore(data, 'same', true, 'restore-key').attachments).toEqual(data.attachments);
});
