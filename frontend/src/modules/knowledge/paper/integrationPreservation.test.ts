import { describe, expect, it } from 'vitest';
import { applyPaperEdits, assignFolders, createPaperRecord, papersInView, parseCSV, toCSV } from './model';
import { emptyLibrary } from './libraryModel';

describe('merged library preservation', () => {
  it('keeps imported folders under the selected parent', () => {
    const data = { ...emptyLibrary(), folders: [{ id: 'parent', name: 'Research' }, { id: 'same', name: 'Models' }] };
    const placed = assignFolders(data, [createPaperRecord({ title: 'Paper' })], ['Models'], 'parent');
    expect(placed.folders.find(folder => folder.id === placed.papers[0].folderId)?.parentId).toBe('parent');
  });
  it('includes descendants and additional collection memberships once', () => {
    const data = { ...emptyLibrary(), folders: [{ id: 'root', name: 'Research' }, { id: 'child', name: 'Models', parentId: 'root' }], papers: [createPaperRecord({ title: 'Paper' }, { id: 'p', folderId: 'child', collectionIds: ['root'] }), createPaperRecord({ title: 'Other' }, { id: 'other', collectionIds: ['child'] })] };
    expect(papersInView(data, 'root').map(paper => paper.id)).toEqual(['p', 'other']);
  });
  it('round trips nested directory paths through CSV', () => {
    const folders = [{ id: 'root', name: 'Research' }, { id: 'child', name: 'Models', parentId: 'root' }];
    const paper = createPaperRecord({ title: 'Paper' }, { folderId: 'child' });
    const parsed = parseCSV(toCSV([paper], folders));
    expect(parsed.folderNames).toEqual(['Research / Models']);
    const restored = assignFolders(emptyLibrary(), parsed.papers, parsed.folderNames, null);
    expect(restored.folders.find(folder => folder.id === restored.papers[0].folderId)?.parentId).toBe(restored.folders.find(folder => folder.name === 'Research')?.id);
  });
});

it('retains additional identifiers while editing normalized metadata', () => {
  const paper = createPaperRecord({ title: 'Paper', identifiers: { doi: '10.1/old', pmid: '123', isbn: '978000' } });
  const edited = applyPaperEdits(paper, { title: 'Edited', authors: 'Ada', venue: 'Journal', year: 2024, doi: '10.1/new', tags: [] });
  expect(edited.metadata.identifiers).toMatchObject({ pmid: '123', isbn: '978000' });
});
