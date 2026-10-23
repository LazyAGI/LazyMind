import { describe, expect, it } from 'vitest';
import { emptyLibraryV3, migratePaperLibrary } from './libraryModel';
import { assignFolders, normalizeLibrary, papersInView, sampleLibrary } from './model';
import { deleteFolderSubtree, descendantFolderIds, flattenFolderTree, folderPath, movePaperRecords, syncFolderCollections, updateFolderTree, validateFolderChange } from './folderTree';

const folders = [
  { id: 'root', name: '研究' },
  { id: 'child', name: '模型', parentId: 'root' },
  { id: 'grandchild', name: '评测', parentId: 'child' },
  { id: 'other', name: '其他' },
];

function library() {
  const value = migratePaperLibrary({ folders, papers: sampleLibrary().papers.slice(0, 3).map((paper, i) => ({ ...paper, folderId: ['grandchild', 'other', null][i] })) });
  value.collections = folders;
  value.papers[1].collectionIds = ['child', 'other'];
  return value;
}

describe('nested paper folders', () => {
  it('preserves parent links when reading legacy libraries', () => {
    expect(normalizeLibrary({ folders, papers: [] }).folders).toEqual(folders);
  });

  it('places bibliography folders under the selected parent without matching another branch', () => {
    const data = library();
    const placed = assignFolders(data, [data.papers[0]], ['模型'], 'other');
    const created = placed.folders.find((folder) => folder.id === placed.papers[0].folderId);
    expect(created).toMatchObject({ name: '模型', parentId: 'other' });
    expect(created?.id).not.toBe('child');
    expect(assignFolders(data, [data.papers[0]], ['模型'], 'root').papers[0].folderId).toBe('child');
  });

  it('includes descendants and multiple collection memberships without duplicating papers', () => {
    const data = library();
    expect(papersInView(data, 'root').map((paper) => paper.id)).toEqual(['p1', 'p2']);
    expect(papersInView(data, 'child').map((paper) => paper.id)).toEqual(['p1', 'p2']);
    expect(papersInView(data, 'other').map((paper) => paper.id)).toEqual(['p2']);
    expect(papersInView(data, 'missing')).toEqual([]);
  });

  it('renders children in tree order and retains path labels while collapsed', () => {
    expect(flattenFolderTree([folders[2], folders[3], folders[1], folders[0]]).map((row) => [row.folder.id, row.depth])).toEqual([
      ['other', 0], ['root', 0], ['child', 1], ['grandchild', 2],
    ]);
    expect(flattenFolderTree(folders, new Set(['root'])).map((row) => row.folder.id)).toEqual(['root', 'other']);
    expect(folderPath(folders, 'grandchild')).toBe('研究 / 模型 / 评测');
    expect([...descendantFolderIds(folders, 'root')]).toEqual(['root', 'child', 'grandchild']);
  });

  it('rejects cycles, missing parents, same-level duplicates and excess depth', () => {
    expect(() => validateFolderChange(folders, { id: 'root', name: '研究', parentId: 'grandchild' })).toThrow(/子文件夹/);
    expect(() => validateFolderChange(folders, { id: 'child', name: '模型', parentId: 'child' })).toThrow(/自身/);
    expect(() => validateFolderChange(folders, { id: 'new', name: '模型', parentId: 'root' })).toThrow(/同名/);
    expect(() => validateFolderChange(folders, { id: 'new', name: '模型', parentId: 'missing' })).toThrow(/不存在/);
    expect(() => validateFolderChange(folders, { id: 'new', name: '模型', parentId: 'other' })).not.toThrow();
    const deep = Array.from({ length: 32 }, (_, i) => ({ id: String(i), name: String(i), parentId: i ? String(i - 1) : null }));
    expect(() => validateFolderChange(deep, { id: 'new', name: '新', parentId: '31' })).toThrow(/32/);
    expect(() => validateFolderChange([...deep, { id: 'a', name: 'a' }, { id: 'b', name: 'b', parentId: 'a' }], { id: 'a', name: 'a', parentId: '30' })).toThrow(/32/);
  });

  it('deletes the whole folder subtree while preserving all papers and materials', () => {
    const original = library();
    original.notes = [{ id: 'note', paperId: 'p1', title: '笔记', content: '保留', kind: 'note', createdAt: '', updatedAt: '' }];
    original.hiddenArxivPapers = [{ ...original.papers[0], id: 'hidden', collectionIds: ['child', 'other'] }];
    const next = deleteFolderSubtree(original, 'root');
    expect(next.folders.map((folder) => folder.id)).toEqual(['other']);
    expect(next.collections.map((folder) => folder.id)).toEqual(['other']);
    expect(next.papers).toHaveLength(3);
    expect(next.papers[0].folderId).toBeNull();
    expect(next.papers[1].collectionIds).toEqual(['other']);
    expect(next.hiddenArxivPapers?.[0]).toMatchObject({ folderId: null, collectionIds: ['other'] });
    expect(next.attachments).toEqual(original.attachments);
    expect(next.notes).toEqual(original.notes);
    expect(original.folders).toHaveLength(4);
    expect(deleteFolderSubtree(emptyLibraryV3(), 'missing')).toEqual(emptyLibraryV3());
  });

  it('moving papers clears prior collection memberships so they leave the old tree', () => {
    const data = library();
    const papers = movePaperRecords(data.papers, ['p1', 'p2'], 'other');
    expect(papers[1]).toMatchObject({ folderId: 'other', collectionIds: ['other'] });
    expect(papersInView({ ...data, papers }, 'root')).toEqual([]);
    expect(movePaperRecords(papers, ['p2'], null)[1]).toMatchObject({ folderId: null, collectionIds: [] });
    expect(papers[2]).toBe(data.papers[2]);
  });

  it('keeps collection parents valid when moving an imported folder below a legacy folder', () => {
    const data = library();
    data.folders = data.folders.map((folder) => folder.id === 'child' ? { ...folder, zoteroSources: ['users/123/child'] } : folder);
    data.collections = [{ id: 'child', name: '模型', description: '保留说明' }];
    const next = updateFolderTree(data, { id: 'child', name: '新名称', parentId: 'other' });
    expect(next.collections.find((folder) => folder.id === 'child')).toMatchObject({ name: '新名称', parentId: 'other', description: '保留说明' });
    expect(next.collections.find((folder) => folder.id === 'other')).toMatchObject({ name: '其他' });
    expect(next.folders.find((folder) => folder.id === 'grandchild')?.parentId).toBe('child');
    expect(next.folders.find((folder) => folder.id === 'child')).toMatchObject({ zoteroSources: ['users/123/child'] });
    expect(next.papers).toBe(data.papers);
  });

  it('creates collection identities for legacy folders without losing independent collections', () => {
    const data = library();
    data.collections = [{ id: 'extra', name: '关联集合', parentId: 'root', description: '保留' }];
    const collections = syncFolderCollections(data);
    expect(collections.find((item) => item.id === 'extra')).toEqual(data.collections[0]);
    expect(collections.find((item) => item.id === 'child')).toEqual(folders[1]);
    const moved = movePaperRecords(data.papers, ['p1'], 'other');
    expect(collections.some((item) => item.id === moved[0].collectionIds[0])).toBe(true);
    expect(data.collections).toHaveLength(1);
  });
});
