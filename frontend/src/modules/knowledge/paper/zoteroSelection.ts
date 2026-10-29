import type { ZoteroPreview } from './zoteroImport';

export interface ZoteroSelectablePaper {
  key: string;
  title: string;
  authors: string[];
  year: string;
  pdfCount: number;
  unavailablePdfCount: number;
  noteCount: number;
  annotationCount: number;
  collections: string[];
  searchText: string;
}

type RecordData = Record<string, unknown>;
interface ItemNode { key: string; data: RecordData; parent: string; type: string; pdf: boolean; memberships: string[] }
interface CollectionNode { key: string; data: RecordData; parent: string }
interface SelectionGraph {
  items: Map<string, ItemNode>;
  collections: Map<string, CollectionNode>;
  roots: Set<string>;
  owners: Map<string, string>;
}
const asObject = (value: unknown): RecordData | undefined => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordData : undefined;
const text = (value: unknown) => typeof value === 'string' ? value.trim() : '';
const dataOf = (value: unknown): RecordData => { const raw = asObject(value); return asObject(raw?.data) || raw || {}; };
const keyOf = (value: unknown) => {
  const raw = asObject(value); const data = dataOf(value);
  return text(data.key) || text(raw?.key) || text(data.itemKey) || text(data.uri).split('/items/')[1] || (typeof data.itemID === 'number' ? String(data.itemID) : text(data.itemID));
};
const isPDF = (data: RecordData) => data.contentType === 'application/pdf' || data.mimeType === 'application/pdf' || /\.pdf$/i.test(text(data.filename) || text(data.path));
const isPaper = (type: string) => !['attachment', 'note', 'annotation'].includes(type);
const reference = (value: unknown, kind: string) => {
  if (value === undefined || value === null || value === false || value === '') return '';
  if (typeof value !== 'string' || !value.trim()) throw new Error(`Zotero ${kind}标识无效`);
  return value.trim();
};

/** Every edge is visited once; even adversarial chains never recurse on the JS stack. */
function validateParents(nodes: Map<string, { parent: string }>, kind: string) {
  const depths = new Map<string, number>();
  for (const key of nodes.keys()) {
    if (depths.has(key)) continue;
    const path: string[] = []; const visiting = new Set<string>();
    let current = key;
    while (current && !depths.has(current)) {
      if (visiting.has(current)) throw new Error(`Zotero ${kind}存在循环父子关系`);
      const node = nodes.get(current);
      if (!node) throw new Error(`Zotero ${kind}的父记录不存在，未导入`);
      visiting.add(current); path.push(current); current = node.parent;
      if (path.length > 33) throw new Error(`Zotero ${kind}层级超过 32 层`);
    }
    let depth = current ? depths.get(current)! : -1;
    for (let index = path.length - 1; index >= 0; index--) {
      depth++;
      if (depth > 32) throw new Error(`Zotero ${kind}层级超过 32 层`);
      depths.set(path[index], depth);
    }
  }
}

function indexPreview(preview: ZoteroPreview): SelectionGraph {
  if (!Array.isArray(preview.items) || !Array.isArray(preview.collections) || !text(preview.source_id)
    || !Array.isArray(preview.warnings) || !preview.warnings.every(value => typeof value === 'string')) throw new Error('Zotero 返回的数据格式无效');
  if (preview.items.length > 50000 || preview.collections.length > 10000 || preview.source_id.length > 2000) throw new Error('Zotero 文献库超过单次导入上限，请分批导入');
  const collections = new Map<string, CollectionNode>();
  for (const value of preview.collections) {
    const key = keyOf(value); const data = dataOf(value);
    if (!key || !text(data.name) || collections.has(key)) throw new Error('Zotero 集合名称或标识无效、重复');
    collections.set(key, { key, data, parent: reference(data.parentCollection, '集合父记录') });
  }
  validateParents(collections, '集合');
  const items = new Map<string, ItemNode>();
  let membershipCount = 0;
  for (const value of preview.items) {
    const data = dataOf(value);
    if (data.deleted === true || data.deleted === 1) continue;
    const key = keyOf(value); const type = text(data.itemType);
    if (!key || !type || items.has(key)) throw new Error('Zotero 文献类型或标识无效、重复');
    if (data.collections !== undefined && !Array.isArray(data.collections)) throw new Error('Zotero 文献集合引用格式无效');
    const memberships = [...new Set((data.collections as unknown[] || []).map(value => reference(value, '集合')))];
    membershipCount += memberships.length;
    if (membershipCount > 250000) throw new Error('Zotero 集合引用超过单次导入上限');
    if (memberships.some(key => !collections.has(key))) throw new Error('Zotero 文献引用了不存在的集合，未导入');
    items.set(key, { key, data, type, parent: reference(data.parentItem, '文献父记录'), pdf: type === 'attachment' && isPDF(data), memberships });
  }
  validateParents(items, '文献');
  const roots = new Set<string>(); const owners = new Map<string, string>();
  for (const node of items.values()) {
    const parent = items.get(node.parent);
    if (isPaper(node.type)) {
      if (parent) throw new Error('Zotero 文献的父子关系无效');
      roots.add(node.key); owners.set(node.key, node.key);
    } else if (node.type === 'attachment') {
      if (parent && !isPaper(parent.type)) throw new Error('Zotero 附件缺少对应文献');
      if (node.pdf) {
        if (!parent) roots.add(node.key);
        owners.set(node.key, parent?.key || node.key);
      }
    } else if (node.type === 'note' && parent) {
      if (!isPaper(parent.type) && !(parent.pdf && !parent.parent)) throw new Error('Zotero 笔记缺少对应文献');
      owners.set(node.key, parent.key);
    } else if (node.type === 'annotation') {
      if (!parent || parent.type !== 'attachment') throw new Error('Zotero 批注缺少对应附件');
      if (parent.pdf) owners.set(node.key, parent.parent || parent.key);
    }
  }
  return { items, collections, roots, owners };
}

/** Build the visible list without creating a library or hashing every imported record. */
export function listZoteroPapers(preview: ZoteroPreview): ZoteroSelectablePaper[] {
  const graph = indexPreview(preview);
  const paths = new Map<string, string>();
  const collectionPath = (key: string) => {
    if (paths.has(key)) return paths.get(key)!;
    const parts: string[] = [];
    let current = key;
    while (current) { const node = graph.collections.get(current)!; parts.unshift(text(node.data.name)); current = node.parent; }
    const path = parts.join(' / '); paths.set(key, path); return path;
  };
  const papers = new Map<string, ZoteroSelectablePaper>();
  for (const key of graph.roots) {
    const node = graph.items.get(key)!; const data = node.data;
    const creators = Array.isArray(data.creators) ? data.creators : [];
    const authors = creators.map(asObject).filter((creator): creator is RecordData => !!creator && (!creator.creatorType || creator.creatorType === 'author' || creator.creatorType === 'bookAuthor'))
      .map(creator => text(creator.name) || [text(creator.firstName), text(creator.lastName)].filter(Boolean).join(' ')).filter(Boolean);
    const title = text(data.title) || text(data.filename) || text(data.path).replace(/\\/g, '/').split('/').pop() || '未命名文献';
    const year = text(data.date).match(/\b(\d{4})\b/)?.[1] || '';
    papers.set(key, { key, title, authors, year, pdfCount: 0, unavailablePdfCount: 0, noteCount: 0, annotationCount: 0,
      collections: node.memberships.map(collectionPath),
      searchText: [title, ...authors, year].filter(Boolean).join(' ').toLocaleLowerCase(),
    });
  }
  for (const [key, owner] of graph.owners) {
    const paper = papers.get(owner)!; const node = graph.items.get(key)!;
    if (node.pdf) {
      paper.pdfCount++;
      if (node.data.localFileAvailable === false) paper.unavailablePdfCount++;
      if (text(node.data.note)) paper.noteCount++;
    }
    if (node.type === 'note') paper.noteCount++;
    if (node.type === 'annotation') paper.annotationCount++;
  }
  return [...papers.values()];
}

/** Summarize omitted materials after listZoteroPapers has validated the graph, without building an import. */
export function zoteroSelectionWarnings(preview: ZoteroPreview): string[] {
  let standaloneNotes = 0;
  let otherAttachments = 0;
  for (const value of preview.items) {
    const data = dataOf(value);
    if (data.deleted === true || data.deleted === 1) continue;
    if (data.itemType === 'note' && !text(data.parentItem)) standaloneNotes++;
    if (data.itemType === 'attachment' && !isPDF(data)) otherAttachments++;
  }
  return [...new Set([...preview.warnings,
    ...(standaloneNotes ? [`${standaloneNotes} 条独立笔记没有关联文献，本次不会导入。`] : []),
    ...(otherAttachments ? [`${otherAttachments} 个非 PDF 附件本次不会导入。`] : []),
  ])];
}

function withoutNestedRecords(data: RecordData): RecordData {
  const copy = { ...data };
  for (const key of ['attachments', 'notes', 'annotations', 'items', 'children']) delete copy[key];
  return copy;
}

function selectedRelations(value: unknown, selected: ReadonlySet<string>): RecordData {
  const result: RecordData = {};
  for (const [kind, references] of Object.entries(asObject(value) || {})) {
    const kept = (Array.isArray(references) ? references : [references]).filter(value => {
      const key = text(value).split('/items/')[1];
      return !!key && selected.has(key);
    });
    if (kept.length) result[kind] = Array.isArray(references) ? kept : kept[0];
  }
  return result;
}

/** Only explicit selections may reach the import builder, including on partial retries. */
export function filterZoteroPreview(preview: ZoteroPreview, selectedKeys: ReadonlySet<string> | string[]): ZoteroPreview {
  const graph = indexPreview(preview); const selected = new Set(selectedKeys);
  for (const key of selected) if (!graph.roots.has(key)) throw new Error('选择的 Zotero 文献已不存在，请重新读取后选择');
  const requiredCollections = new Set<string>();
  for (const key of selected) for (const membership of graph.items.get(key)!.memberships) {
    let current = membership;
    while (current && !requiredCollections.has(current)) {
      requiredCollections.add(current); current = graph.collections.get(current)!.parent;
    }
  }
  const items: unknown[] = [];
  for (const [key, node] of graph.items) {
    if (!selected.has(graph.owners.get(key) || '')) continue;
    const data = withoutNestedRecords(node.data);
    data.key = key;
    if (data.collections !== undefined) data.collections = node.memberships.filter(key => requiredCollections.has(key));
    if (data.relations !== undefined) data.relations = selectedRelations(data.relations, selected);
    items.push({ key, data });
  }
  const collections: unknown[] = [];
  for (const [key, node] of graph.collections) {
    if (!requiredCollections.has(key)) continue;
    const data = withoutNestedRecords(node.data); data.key = key;
    if (data.relations !== undefined) data.relations = selectedRelations(data.relations, selected);
    collections.push({ key, data });
  }
  return { ...preview, items, collections, warnings: [...preview.warnings] };
}

export interface ZoteroFolderNode { key: string; title: string; count: number; children: ZoteroFolderNode[] }
/** Build ancestry memberships once so folder changes never rebuild the import graph. */
export function zoteroBrowserIndex(preview: ZoteroPreview) {
  const graph = indexPreview(preview);
  const nodes = new Map<string, ZoteroFolderNode>();
  for (const [key, node] of graph.collections) nodes.set(key, { key, title: text(node.data.name), count: 0, children: [] });
  const folders: ZoteroFolderNode[] = [];
  for (const [key, node] of graph.collections) (node.parent ? nodes.get(node.parent)!.children : folders).push(nodes.get(key)!);
  const memberships = new Map<string, Set<string>>();
  for (const key of graph.roots) {
    const ancestors = new Set<string>();
    for (const membership of graph.items.get(key)!.memberships) {
      let current = membership;
      while (current && !ancestors.has(current)) { ancestors.add(current); current = graph.collections.get(current)!.parent; }
    }
    for (const folder of ancestors) nodes.get(folder)!.count++;
    memberships.set(key, ancestors);
  }
  return { folders, memberships };
}

/** Matches only persisted identities for this source in the current paper library. */
export function importedZoteroKeys(sourceId: string, papers: ZoteroSelectablePaper[], persisted: readonly { zoteroSources?: string[] }[]) {
  const sources = new Set(persisted.flatMap(paper => paper.zoteroSources || []));
  return new Set(papers.filter(paper => sources.has(`${sourceId}:${paper.key}`)).map(paper => paper.key));
}
