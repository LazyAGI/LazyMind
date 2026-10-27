import { axiosInstance, BASE_URL } from '@/components/request';
import { computeTextSha256 } from '../utils/fileHash';
import { emptyLibrary, withLibraryGraph, type Annotation, type CompletePaperLibrary, type PaperRecord } from './libraryModel';
import type { PaperLibraryData } from './model';
import { normalizeDoi } from './model';

export interface ZoteroImportConfig {
  source: 'local' | 'remote' | 'webdav';
  library_type: 'users' | 'groups';
  library_id: string;
  api_key?: string;
  local_url?: string;
  webdav?: { url: string; username: string; password: string };
}
export interface ZoteroPreview { items: unknown[]; collections: unknown[]; source_id: string; warnings: string[] }
export interface ZoteroImportOptions { includeAttachments: boolean; localFiles: File[]; targetFolderId?: string | null }
export interface ZoteroImportResult { imported: number; attachments: number; failures?: string[] }
export interface ZoteroAttachmentCandidate { id: string; paperId: string; key: string; name: string; md5?: string; localPath?: string }
export interface ZoteroPaperRecord extends PaperRecord { zoteroSources?: string[] }
type ZoteroFolderRecord = PaperLibraryData['folders'][number] & { zoteroSources?: string[] };
export interface ZoteroImportPlan {
  library: Omit<CompletePaperLibrary, 'papers' | 'folders' | 'collections'> & { papers: ZoteroPaperRecord[]; folders: ZoteroFolderRecord[]; collections: ZoteroFolderRecord[] };
  attachments: ZoteroAttachmentCandidate[];
  counts: { papers: number; existing: number; folders: number; notes: number; annotations: number; pdfs: number };
}
type ObjectValue = Record<string, unknown>;
const object = (value: unknown): ObjectValue | undefined => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as ObjectValue : undefined;
const text = (value: unknown): string => typeof value === 'string' ? value.trim() : '';
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const unique = <T,>(values: T[]): T[] => [...new Set(values)];
const dataOf = (value: unknown): ObjectValue => { const raw = object(value); return object(raw?.data) || raw || {}; };
const keyOf = (value: unknown): string => {
  const raw = object(value); const data = dataOf(value);
  return text(data.key) || text(raw?.key) || text(data.itemKey) || text(data.uri).split('/items/')[1] || (typeof data.itemID === 'number' ? String(data.itemID) : text(data.itemID));
};
const fingerprint = (value: string) => computeTextSha256(value).slice(0, 32);
const recordID = (source: string, kind: string, key: string) => `zotero-${kind}-${fingerprint(`${source}:${key}`)}`;
const sourceKey = (source: string, key: string) => `${source}:${key}`;
const safeURL = (value: unknown): string | undefined => {
  try { const url = new URL(text(value)); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined; } catch { return undefined; }
};

export function zoteroImportError(error: unknown, config?: ZoteroImportConfig): string {
  const raw = object(error); const response = object(raw?.response); const body = object(response?.data);
  let message = text(body?.message) || text(object(body?.error)?.message) || text(raw?.message) || 'Zotero 导入失败，请检查连接设置后重试';
  for (const secret of [config?.api_key, config?.webdav?.password]) if (secret) message = message.split(secret).join('••••••');
  return message;
}
function decodePreview(value: unknown): ZoteroPreview {
  const body = object(value);
  if (body && (body.success === false || body.error || (body.code !== undefined && !['0', '200'].includes(String(body.code))))) {
    throw new Error(text(body.message) || text(object(body.error)?.message) || 'Zotero 服务拒绝了导入请求');
  }
  if (!body || !Array.isArray(body.items) || !Array.isArray(body.collections) || !text(body.source_id)
    || (body.warnings !== undefined && (!Array.isArray(body.warnings) || !body.warnings.every(item => typeof item === 'string')))) {
    throw new Error('Zotero 返回的数据格式无效，未导入任何文献');
  }
  if (body.items.length > 50000 || body.collections.length > 10000 || text(body.source_id).length > 2000) throw new Error('Zotero 文献库超过单次导入上限，请分批导入');
  return { items: body.items, collections: body.collections, source_id: text(body.source_id), warnings: (body.warnings || []) as string[] };
}
export async function previewZotero(datasetId: string, config: ZoteroImportConfig, signal?: AbortSignal): Promise<ZoteroPreview> {
  try {
    const response = await axiosInstance.post(`${BASE_URL}/api/core/academic/zotero/preview`, config, {
      params: { target_dataset_id: datasetId }, timeout: 120_000, signal, ...{ silentError: true },
    });
    return decodePreview(response.data);
  } catch (error) { throw new Error(zoteroImportError(error, config)); }
}
export async function downloadZoteroAttachment(datasetId: string, config: ZoteroImportConfig, candidate: ZoteroAttachmentCandidate, signal?: AbortSignal): Promise<File> {
  try {
    const response = await axiosInstance.post(`${BASE_URL}/api/core/academic/zotero/attachment`, { ...config, item_key: candidate.key }, {
      params: { target_dataset_id: datasetId }, responseType: 'blob', timeout: 120_000, signal, ...{ silentError: true },
    });
    const blob = response.data as Blob;
    if (!(blob instanceof Blob) || blob.size === 0 || blob.size > 128 * 1024 * 1024) throw new Error('Zotero 附件为空或超过 128 MiB 上限');
    const signature = await readBlobText(blob.slice(0, 5));
    if (signature !== '%PDF-') throw new Error('Zotero 返回的附件不是有效 PDF');
    return new File([blob], candidate.name, { type: 'application/pdf' });
  } catch (error) {
    const raw = object(error); const response = object(raw?.response);
    if (response?.data instanceof Blob) {
      try { response.data = JSON.parse(await readBlobText(response.data)); } catch { /* retain the HTTP error when no JSON body is available */ }
    }
    throw new Error(zoteroImportError(error, config));
  }
}
export function readBlobText(file: Blob): Promise<string> {
  if (typeof file.text === 'function') return file.text();
  return new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.onerror = () => reject(reader.error || new Error('文件读取失败')); reader.readAsText(file); });
}

/** Accept Zotero JSON exports and API JSON; imported credentials never become graph data. */
export function parseZoteroJSON(content: string): ZoteroPreview {
  if (content.length > 20 * 1024 * 1024) throw new Error('Zotero JSON 文件超过 20 MiB 上限');
  let parsed: unknown;
  try { parsed = JSON.parse(content); } catch { throw new Error('无法解析 JSON，请使用 Zotero API JSON 或 BetterBibTeX JSON 文件'); }
  const root = object(parsed);
  const rawItems = Array.isArray(parsed) ? parsed : root?.items;
  if (!Array.isArray(rawItems)) throw new Error('Zotero JSON 必须包含文献 items 数组');
  const items: ObjectValue[] = [];
  const itemAliases = new Map<string, ObjectValue>();
  const appendItem = (value: unknown, parent?: string, fallbackType?: string) => {
    const data = dataOf(value);
    if (!Object.keys(data).length) throw new Error('Zotero JSON 含有无效文献记录');
    const key = keyOf(value) || (parent && fallbackType ? `child-${fingerprint(`${parent}:${fallbackType}:${JSON.stringify(data)}`)}` : '');
    if (!key) throw new Error('Zotero 文献缺少 key / itemID，无法安全识别重复条目');
    const item: ObjectValue = { ...data, key, itemType: text(data.itemType) || fallbackType, ...(parent ? { parentItem: parent } : {}) };
    if (!item.filename && text(item.path)) item.filename = text(item.path).replace(/\\/g, '/').split('/').pop();
    items.push(item);
    for (const alias of [key, typeof data.itemID === 'number' ? String(data.itemID) : text(data.itemID), text(data.uri)]) {
      if (alias) itemAliases.set(alias, item);
    }
    for (const attachment of list(data.attachments)) appendItem(attachment, key, 'attachment');
    for (const note of list(data.notes)) appendItem(typeof note === 'string' ? { note } : note, key, 'note');
    for (const annotation of list(data.annotations)) appendItem(annotation, key, 'annotation');
  };
  rawItems.forEach(item => appendItem(item));
  const collections: ObjectValue[] = [];
  const appendCollection = (value: unknown, parent?: string, depth = 0) => {
    if (depth > 32) throw new Error('Zotero 集合层级超过 32 层');
    const data = dataOf(value); const key = keyOf(value);
    if (!key || !text(data.name)) throw new Error('Zotero 集合缺少 key 或名称');
    collections.push({ ...data, key, parentCollection: parent || data.parentCollection || data.parent || false });
    const addMembership = (itemKey: unknown) => {
      const id = typeof itemKey === 'number' ? String(itemKey) : text(itemKey) || keyOf(itemKey);
      const item = itemAliases.get(id);
      if (!item) throw new Error('Zotero 集合包含找不到的文献，请导出完整文献库后重试');
      item.collections = unique([...list(item.collections).map(text), key]);
    };
    for (const itemKey of list(data.items)) addMembership(itemKey);
    for (const child of list(data.children)) {
      const childData = dataOf(child);
      if (childData.type === 'item') addMembership(childData.id);
      else appendCollection(child, key, depth + 1);
    }
  };
  const exportedCollections = root?.collections;
  if (Array.isArray(exportedCollections)) exportedCollections.forEach(value => appendCollection(value));
  else if (object(exportedCollections)) Object.entries(exportedCollections as ObjectValue).forEach(([key, value]) => appendCollection({ ...dataOf(value), key: keyOf(value) || key }));
  else if (exportedCollections !== undefined) throw new Error('Zotero 集合数据格式无效');
  const uri = items.map(item => text(item.uri)).find(value => /zotero\.org\/(?:users|groups)\//.test(value));
  const library = uri?.match(/zotero\.org\/(users|groups)\/([^/]+(?:\/[^/]+)?)(?=\/items\/)/);
  const apiLibrary = rawItems.map(value => object(object(value)?.library)).find(value => value && ['user', 'group'].includes(text(value.type)) && /^\d+$/.test(String(value.id)));
  const identifiedSource = library ? `${library[1]}:${library[2]}` : apiLibrary ? `${apiLibrary.type === 'group' ? 'groups' : 'users'}:${apiLibrary.id}` : text(root?.source_id);
  const source = identifiedSource || `export:${fingerprint(items.map(item => String(item.key)).sort().join('|'))}`;
  return decodePreview({ items, collections, source_id: source, warnings: identifiedSource ? [] : ['导出文件未包含文献库来源标识。重复导入同一文件不会重复添加；更新后的导出文件将优先按 DOI / arXiv 匹配。'] });
}

function noteText(value: unknown): string {
  const doc = new DOMParser().parseFromString(text(value), 'text/html');
  doc.querySelectorAll('script,style,iframe,object').forEach(node => node.remove());
  doc.querySelectorAll('br').forEach(node => node.replaceWith('\n'));
  doc.querySelectorAll('p,div,li').forEach(node => node.append('\n'));
  return (doc.body.textContent || '').replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim();
}
const authors = (data: ObjectValue) => list(data.creators).map(object).filter((creator): creator is ObjectValue => !!creator && (!creator.creatorType || creator.creatorType === 'author' || creator.creatorType === 'bookAuthor')).map(creator => text(creator.name) || [text(creator.firstName), text(creator.lastName)].filter(Boolean).join(' ')).filter(Boolean);
const tagsOf = (data: ObjectValue) => unique(list(data.tags).map(value => text(object(value)?.tag) || text(value)).filter(Boolean));
const arxivOf = (data: ObjectValue) => {
  const match = [text(data.archiveID), text(data.extra), text(data.url)].join('\n').match(/(?:arxiv(?:\.org\/(?:abs|pdf)\/|:\s*)|^)(\d{4}\.\d{4,5})(?:v\d+)?/i);
  return match?.[1];
};
const timestamp = (value: unknown, fallback: string) => { const valueText = text(value); return valueText && !Number.isNaN(Date.parse(valueText)) ? new Date(valueText).toISOString() : fallback; };
const pdfAttachment = (data: ObjectValue) => data.contentType === 'application/pdf' || data.mimeType === 'application/pdf' || /\.pdf$/i.test(text(data.filename) || text(data.path));

export function buildZoteroImport(preview: ZoteroPreview, current: PaperLibraryData, targetFolderId?: string | null): ZoteroImportPlan {
  decodePreview(preview);
  if (targetFolderId && !current.folders.some(folder => folder.id === targetFolderId)) throw new Error('导入目标文件夹已不存在，请重新选择');
  const library: ZoteroImportPlan['library'] = withLibraryGraph(JSON.parse(JSON.stringify(current)));
  const counts = { papers: 0, existing: 0, folders: 0, notes: 0, annotations: 0, pdfs: 0 };
  const attachments: ZoteroAttachmentCandidate[] = [];
  for (const folder of library.folders) {
    if (!library.collections.some(collection => collection.id === folder.id)) library.collections.push({ ...folder });
  }
  const now = new Date().toISOString();
  const collectionMap = new Map<string, ObjectValue>();
  for (const value of preview.collections) {
    const key = keyOf(value); const data = dataOf(value);
    if (!key || !text(data.name) || collectionMap.has(key)) throw new Error('Zotero 集合名称或标识无效、重复');
    collectionMap.set(key, data);
  }
  const folderIDs = new Map<string, string>();
  const visiting = new Set<string>();
  const ensureCollection = (key: string, depth = 0): string => {
    const known = folderIDs.get(key); if (known) return known;
    if (visiting.has(key)) throw new Error('Zotero 集合存在循环父子关系');
    if (depth > 32) throw new Error('Zotero 集合层级超过 32 层');
    const data = collectionMap.get(key); if (!data) throw new Error('Zotero 集合的父文件夹不存在，未导入');
    visiting.add(key);
    const parentKey = text(data.parentCollection);
    const parentId = parentKey ? ensureCollection(parentKey, depth + 1) : targetFolderId || null;
    const stableID = recordID(preview.source_id, 'folder', key);
    const external = sourceKey(preview.source_id, key);
    const existing = library.folders.find(folder => folder.id === stableID || folder.zoteroSources?.includes(external))
      || library.folders.find(folder => folder.name === text(data.name) && (folder.parentId || null) === parentId);
    const id = existing?.id || stableID;
    if (existing) existing.zoteroSources = unique([...(existing.zoteroSources || []), external]);
    else { library.folders.push({ id, name: text(data.name), parentId, zoteroSources: [external] }); counts.folders++; }
    const collection = library.collections.find(item => item.id === id);
    if (collection) collection.zoteroSources = unique([...(collection.zoteroSources || []), external]);
    else library.collections.push({ id, name: existing?.name || text(data.name), parentId: existing ? existing.parentId || null : parentId, zoteroSources: [external] });
    folderIDs.set(key, id); visiting.delete(key); return id;
  };
  collectionMap.forEach((_, key) => ensureCollection(key));
  const rawItems = new Map<string, ObjectValue>();
  for (const value of preview.items) {
    const data = dataOf(value); if (data.deleted === true || data.deleted === 1) continue;
    const key = keyOf(value);
    if (!key || !text(data.itemType) || rawItems.has(key)) throw new Error('Zotero 文献类型或标识无效、重复');
    rawItems.set(key, data);
  }
  const paperIDs = new Map<string, string>();
  const membership = (data: ObjectValue) => list(data.collections).map(value => {
    const key = text(value); const id = folderIDs.get(key);
    if (!id) throw new Error('Zotero 文献引用了不存在的集合，未导入');
    return id;
  });
  const addPaper = (key: string, data: ObjectValue, standalone = false) => {
    const external = sourceKey(preview.source_id, key);
    const doi = normalizeDoi(text(data.DOI) || text(data.doi)); const arxiv = arxivOf(data);
    const collections = membership(data);
    const stableID = recordID(preview.source_id, 'paper', key);
    const existing = library.papers.find(paper => paper.id === stableID || paper.zoteroSources?.includes(external))
      || library.papers.find(paper => (doi && normalizeDoi(paper.metadata.identifiers.doi || '') === doi) || (arxiv && paper.metadata.identifiers.arxiv?.replace(/v\d+$/, '') === arxiv));
    if (existing) {
      existing.zoteroSources = unique([...(existing.zoteroSources || []), external]);
      existing.tags = unique([...existing.tags, ...tagsOf(data)]);
      existing.collectionIds = unique([...(existing.collectionIds || []), ...collections]);
      if (!existing.metadata.authors.length) existing.metadata.authors = authors(data); existing.metadata.venue ||= text(data.publicationTitle) || text(data.proceedingsTitle) || text(data.publisher);
      existing.metadata.abstract ||= text(data.abstractNote);
      existing.metadata.identifiers.doi ||= doi || undefined; existing.metadata.identifiers.arxiv ||= arxiv;
      existing.metadata.year ||= Number(text(data.date).match(/\b(\d{4})\b/)?.[1]) || 0;
      paperIDs.set(key, existing.id); counts.existing++; return;
    }
    const record: ZoteroPaperRecord = {
      id: stableID, revision: 0,
      metadata: {
        title: text(data.title) || text(data.filename) || '未命名文献', authors: authors(data),
        venue: text(data.publicationTitle) || text(data.proceedingsTitle) || text(data.publisher), year: Number(text(data.date).match(/\b(\d{4})\b/)?.[1]) || 0,
        abstract: text(data.abstractNote), identifiers: { doi: doi || undefined, arxiv }, confidence: {},
      }, sourceUrl: safeURL(data.url),
      folderId: collections[0] || targetFolderId || null, status: 'unread', important: false, tags: tagsOf(data), source: 'zotero', parse: 'ready',
      incomplete: standalone || !text(data.title),
      attachmentIds: [], noteIds: [], annotationIds: [], collectionIds: collections, relatedPaperIds: [], zoteroSources: [external],
    };
    library.papers.push(record); paperIDs.set(key, stableID); counts.papers++;
  };
  rawItems.forEach((data, key) => { if (!['attachment', 'note', 'annotation'].includes(text(data.itemType))) addPaper(key, data); });
  const attachmentIDs = new Map<string, string>();
  rawItems.forEach((data, key) => {
    if (data.itemType !== 'attachment') return;
    if (!pdfAttachment(data)) return;
    const filename = text(data.filename) || text(data.path).replace(/\\/g, '/').split('/').pop() || `${key}.pdf`;
    let paperId = paperIDs.get(text(data.parentItem));
    if (!text(data.parentItem)) { addPaper(key, { ...data, filename }, true); paperId = paperIDs.get(key); }
    if (!paperId) throw new Error('Zotero PDF 缺少对应文献，未导入');
    const paper = library.papers.find(item => item.id === paperId)!;
    const id = recordID(preview.source_id, 'attachment', key); attachmentIDs.set(key, id);
    let attachment = library.attachments.find(item => item.id === id);
    if (!attachment) {
      attachment = { id, paperId, name: filename, kind: 'pdf', mimeType: 'application/pdf', source: 'zotero', addedAt: timestamp(data.dateAdded, now), sourceUrl: safeURL(data.url), isPrimary: !paper.attachmentIds.length };
      library.attachments.push(attachment);
    }
    paper.attachmentIds = unique([...paper.attachmentIds, id]);
    if (!attachment.contentRef || text((attachment as unknown as ObjectValue).processingError)) attachments.push({ id, paperId, key, name: filename, md5: text(data.md5) || undefined, localPath: text(data.path) || undefined });
    counts.pdfs++;
  });
  rawItems.forEach((data, key) => {
    if (data.itemType !== 'note') return;
    const paperId = paperIDs.get(text(data.parentItem));
    if (!paperId) {
      if (text(data.parentItem)) throw new Error('Zotero 笔记缺少对应文献，未导入');
      return; // Standalone notes have no paper parent; the preview lists this limitation.
    }
    const id = recordID(preview.source_id, 'note', key); const paper = library.papers.find(item => item.id === paperId)!;
    if (!library.notes.some(note => note.id === id)) {
      const content = noteText(data.note);
      library.notes.push({ id, paperId, title: content.split('\n')[0].slice(0, 80) || 'Zotero 笔记', content, kind: 'note', createdAt: timestamp(data.dateAdded, now), updatedAt: timestamp(data.dateModified, now) });
      counts.notes++;
    }
    paper.noteIds = unique([...paper.noteIds, id]);
  });
  rawItems.forEach((data, key) => {
    if (data.itemType !== 'annotation') return;
    const attachmentId = attachmentIDs.get(text(data.parentItem));
    if (!attachmentId) return;
    const attachment = library.attachments.find(item => item.id === attachmentId)!;
    const paper = library.papers.find(item => item.id === attachment.paperId)!;
    const id = recordID(preview.source_id, 'annotation', key);
    if (!library.annotations.some(annotation => annotation.id === id)) {
      let position: ObjectValue | undefined;
      if (text(data.annotationPosition)) { try { position = object(JSON.parse(text(data.annotationPosition))); } catch { throw new Error('Zotero 批注位置信息无效，未导入'); } }
      const color: Annotation['color'] = ({ '#ffd400': 'yellow', '#2ea8e5': 'blue', '#ff6666': 'red', '#5fb236': 'green' } as Record<string, Annotation['color']>)[text(data.annotationColor)] || 'yellow';
      library.annotations.push({ id, paperId: paper.id, attachmentId, quote: text(data.annotationText), comment: text(data.annotationComment), color, page: typeof position?.pageIndex === 'number' && position.pageIndex >= 0 ? position.pageIndex + 1 : undefined, createdAt: timestamp(data.dateAdded, now) });
      counts.annotations++;
    }
    paper.annotationIds = unique([...(paper.annotationIds || []), id]);
  });
  rawItems.forEach((data, key) => {
    const from = paperIDs.get(key); if (!from) return;
    const relation = object(data.relations)?.['dc:relation'];
    for (const uri of Array.isArray(relation) ? relation : relation ? [relation] : []) {
      const to = paperIDs.get(text(uri).split('/items/')[1]); if (!to || to === from) continue;
      const id = recordID(preview.source_id, 'relation', `${from}:${to}`);
      if (!library.relations.some(item => item.id === id)) library.relations.push({ id, fromPaperId: from, toPaperId: to, kind: 'related' });
      const paper = library.papers.find(item => item.id === from)!;
      paper.relatedPaperIds = unique([...(paper.relatedPaperIds || []), to]);
    }
  });
  if (library.papers.length > 10000) throw new Error('导入后文献超过论文库 10000 篇上限，请分批整理');
  return { library, attachments, counts };
}

export function matchLocalZoteroFile(candidate: Pick<ZoteroAttachmentCandidate, 'key' | 'name' | 'localPath'>, files: File[]): File | undefined {
  const pdfs = files.filter(file => /\.pdf$/i.test(file.name));
  const keyed = pdfs.filter(file => (file.webkitRelativePath || '').replace(/\\/g, '/').split('/').includes(candidate.key) && file.name === candidate.name);
  if (keyed.length === 1) return keyed[0];
  // JSON exports may use files/<numeric item ID>/file.pdf rather than storage/<key>.
  const path = (candidate.localPath || '').replace(/\\/g, '/').replace(/^(?:\.\/|\/)+/, '');
  const relative = path.includes('/') ? pdfs.filter(file => {
    const selected = (file.webkitRelativePath || '').replace(/\\/g, '/');
    return file.name === candidate.name && (selected === path || selected.endsWith(`/${path}`) || (selected.includes('/') && path.endsWith(`/${selected}`)));
  }) : [];
  if (relative.length === 1) return relative[0];
  const named = pdfs.filter(file => file.name === candidate.name);
  if (keyed.length > 1 || relative.length > 1 || named.length > 1) throw new Error(`找到多个同名附件「${candidate.name}」，请选择包含 Zotero storage 或导出附件子目录的文件夹`);
  if (named[0]?.webkitRelativePath && (path.includes('/') || /(?:^|\/)storage\//i.test(named[0].webkitRelativePath) || /(?:^|\/)[A-Z0-9]{8}\//.test(named[0].webkitRelativePath))) {
    throw new Error(`附件「${candidate.name}」与 Zotero 目录标识不匹配，请选择对应的附件目录`);
  }
  return named[0];
}

export function summarizeZoteroPreview(preview: ZoteroPreview) {
  const plan = buildZoteroImport(preview, emptyLibrary());
  const standaloneNotes = preview.items.filter(value => { const data = dataOf(value); return data.itemType === 'note' && !text(data.parentItem); }).length;
  const otherAttachments = preview.items.filter(value => { const data = dataOf(value); return data.itemType === 'attachment' && !pdfAttachment(data); }).length;
  return { ...plan.counts, titles: plan.library.papers.slice(0, 4).map(paper => paper.metadata.title), warnings: [...preview.warnings, ...(standaloneNotes ? [`${standaloneNotes} 条独立笔记没有关联文献，本次不会导入。`] : []), ...(otherAttachments ? [`${otherAttachments} 个非 PDF 附件本次不会导入。`] : [])] };
}
