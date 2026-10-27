import type { PaperFolder, PaperRecord } from './model';
import type { Collection, CompletePaperLibrary } from './libraryModel';

export const MAX_FOLDER_DEPTH = 32;

export function descendantFolderIds(folders: PaperFolder[], id: string): Set<string> {
  const children = new Map<string, string[]>();
  for (const folder of folders) {
    if (folder.parentId) {
      const siblings = children.get(folder.parentId) || [];
      siblings.push(folder.id);
      children.set(folder.parentId, siblings);
    }
  }
  const ids = new Set<string>();
  const pending = folders.some((folder) => folder.id === id) ? [id] : [];
  while (pending.length) {
    const current = pending.pop()!;
    if (ids.has(current)) continue;
    ids.add(current);
    pending.push(...(children.get(current) || []).slice().reverse());
  }
  return ids;
}

export function folderPath(folders: PaperFolder[], id: string): string {
  const byId = new Map(folders.map((folder) => [folder.id, folder]));
  const seen = new Set<string>();
  const path: string[] = [];
  let current = byId.get(id);
  while (current && !seen.has(current.id)) {
    seen.add(current.id);
    path.unshift(current.name);
    current = current.parentId ? byId.get(current.parentId) : undefined;
  }
  return path.join(' / ');
}

export interface FolderTreeRow {
  folder: PaperFolder;
  depth: number;
  hasChildren: boolean;
}

export function flattenFolderTree(folders: PaperFolder[], collapsed = new Set<string>()): FolderTreeRow[] {
  const ids = new Set(folders.map((folder) => folder.id));
  const children = new Map<string | null, PaperFolder[]>();
  for (const folder of folders) {
    const parent = folder.parentId && ids.has(folder.parentId) ? folder.parentId : null;
    const siblings = children.get(parent) || [];
    siblings.push(folder);
    children.set(parent, siblings);
  }
  const rows: FolderTreeRow[] = [];
  const visited = new Set<string>();
  const walk = (parent: string | null, depth: number) => {
    for (const folder of children.get(parent) || []) {
      if (visited.has(folder.id)) continue;
      visited.add(folder.id);
      rows.push({ folder, depth, hasChildren: !!children.get(folder.id)?.length });
      if (!collapsed.has(folder.id) && depth < MAX_FOLDER_DEPTH) walk(folder.id, depth + 1);
    }
  };
  walk(null, 0);
  return rows;
}

export function validateFolderChange(folders: PaperFolder[], proposed: PaperFolder): void {
  const name = proposed.name.trim();
  if (!name || name.length > 80) throw new Error('文件夹名称须为 1–80 个字符');
  const parentId = proposed.parentId || null;
  if (parentId === proposed.id) throw new Error('文件夹不能移动到自身');
  if (parentId && !folders.some((folder) => folder.id === parentId)) throw new Error('上级文件夹已不存在，请重新选择');
  if (parentId && descendantFolderIds(folders, proposed.id).has(parentId)) throw new Error('文件夹不能移动到自己的子文件夹');
  if (folders.some((folder) => folder.id !== proposed.id && (folder.parentId || null) === parentId && folder.name.trim() === name)) {
    throw new Error('该位置已有同名文件夹');
  }
  const next = [...folders.filter((folder) => folder.id !== proposed.id), proposed];
  const byId = new Map(next.map((folder) => [folder.id, folder]));
  for (const id of descendantFolderIds(next, proposed.id)) {
    let current = byId.get(id);
    let depth = 0;
    const seen = new Set<string>();
    while (current) {
      if (seen.has(current.id)) throw new Error('文件夹层级不能循环引用');
      seen.add(current.id);
      if (++depth > MAX_FOLDER_DEPTH) throw new Error(`文件夹最多支持 ${MAX_FOLDER_DEPTH} 层`);
      current = current.parentId ? byId.get(current.parentId) : undefined;
    }
  }
}

export function paperBelongsToFolders(paper: PaperRecord & { collectionIds?: string[] }, ids: Set<string>): boolean {
  return !!paper.folderId && ids.has(paper.folderId) || !!paper.collectionIds?.some((id) => ids.has(id));
}

export function movePaperRecords(papers: PaperRecord[], ids: string[], folderId: string | null): PaperRecord[] {
  const wanted = new Set(ids);
  return papers.map((paper) => wanted.has(paper.id) ? { ...paper, folderId, collectionIds: folderId ? [folderId] : [] } : paper);
}

export function updateFolderTree(data: CompletePaperLibrary, folder: PaperFolder): CompletePaperLibrary {
  validateFolderChange(data.folders, folder);
  const exists = data.folders.some((item) => item.id === folder.id);
  const folders = exists ? data.folders.map((item) => item.id === folder.id ? { ...item, ...folder, parentId: folder.parentId || null } : item) : [...data.folders, folder];
  return {
    ...data,
    folders,
    collections: syncFolderCollections({ ...data, folders }),
  };
}

/** Legacy folders also need collection identities before papers refer to them. */
export function syncFolderCollections(data: Pick<CompletePaperLibrary, 'folders' | 'collections'>): Collection[] {
  const byId = new Map(data.folders.map((item) => [item.id, item]));
  const collectionIds = new Set(data.collections.map((item) => item.id));
  return [
    ...data.collections.map((item) => byId.has(item.id) ? { ...item, ...byId.get(item.id), parentId: byId.get(item.id)!.parentId || null } : item),
    ...data.folders.filter((item) => !collectionIds.has(item.id)).map((item) => ({ ...item })),
  ];
}

export function deleteFolderSubtree(data: CompletePaperLibrary, folderId: string): CompletePaperLibrary {
  const folderIds = new Set(data.folders.map((folder) => folder.id));
  const removed = descendantFolderIds([...data.folders, ...data.collections.filter((collection) => !folderIds.has(collection.id))], folderId);
  const clean = (paper: PaperRecord): PaperRecord => ({
    ...paper,
    folderId: paper.folderId && removed.has(paper.folderId) ? null : paper.folderId,
    collectionIds: (paper.collectionIds || []).filter((id) => !removed.has(id)),
  });
  return {
    ...data,
    folders: data.folders.filter((folder) => !removed.has(folder.id)),
    collections: data.collections.filter((folder) => !removed.has(folder.id)),
    papers: data.papers.map(clean),
    ...(data.hiddenArxivPapers ? { hiddenArxivPapers: data.hiddenArxivPapers.map(clean) } : {}),
  };
}
