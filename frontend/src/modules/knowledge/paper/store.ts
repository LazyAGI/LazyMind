import { parseLibraryBackup } from './libraryBackup';
import { computeTextSha256 } from '../utils/fileHash';
import type { PaperLibraryData, PaperRecord, PaperMetadata, PaperFolder } from './model';
import { axiosInstance, BASE_URL } from '@/components/request';
export const isPaperLibrary = (scene?: string) => scene === 'academic_papers';
export interface PersistedPaperLibrary {
    data: PaperLibraryData;
    canWrite: boolean;
    paperCount: number;
    pdfAttachmentCount: number;
    active: boolean;
}
export interface LibraryChanges {
    data: PaperLibraryData;
    removedPaperIds?: string[];
    removedFolderIds?: string[];
    batch_id?: string;
    status?: string;
}
const core = `${BASE_URL}/api/core/academic`;
function checkData(data: PaperLibraryData): PaperLibraryData {
    if (!data || !['papers', 'folders', 'attachments', 'notes'].every(key => Array.isArray(data[key as keyof PaperLibraryData])))
        throw new Error('服务器论文库数据格式无效，请重试');
    if (!data.papers.every(p => p.id && Number.isSafeInteger(p.revision) && p.revision > 0 && p.metadata && typeof p.metadata.title === 'string' && Array.isArray(p.metadata.authors) && p.metadata.identifiers && Array.isArray(p.tags) && Array.isArray(p.attachmentIds) && Array.isArray(p.noteIds)))
        throw new Error('服务器论文条目格式无效，请重试');
    return data;
}
export function decodePersistedLibrary(value: unknown): PersistedPaperLibrary {
    if (!value || typeof value !== 'object' || typeof (value as { can_write?: unknown }).can_write !== 'boolean')
        throw new Error('服务器论文库响应格式无效');
    const data = value as { data: PaperLibraryData; can_write: boolean; paper_count: number; pdf_attachment_count: number; active: boolean };
    return { data: checkData(data.data), canWrite: data.can_write, paperCount: data.paper_count, pdfAttachmentCount: data.pdf_attachment_count, active: data.active };
}
export async function loadPersistedPaperLibrary(dataset: string): Promise<PersistedPaperLibrary> {
    const { data } = await axiosInstance.get(`${core}/library`, { params: { target_dataset_id: dataset }, timeout: 30000, silentError: true } as never);
    const current = decodePersistedLibrary(data);
    let raw: string | null = null;
    let version = 3;
    try {
        raw = localStorage.getItem(`lazymind.paper-library.data.v3.${dataset}`);
        if (!raw) { raw = localStorage.getItem(`lazymind.paper-library.data.v2.${dataset}`); version = 2; }
    } catch { return current; }
    if (!raw) return current;
    const migrationId = `browser-${computeTextSha256(raw)}`;
    if (current.data.migrationSnapshots?.some(snapshot => (snapshot as { id?: string })?.id === migrationId)) return current;
    let legacy: PaperLibraryData;
    try {
        const parsed = JSON.parse(raw);
        legacy = parseLibraryBackup(version === 3 ? raw : JSON.stringify({ ...parsed, schemaVersion: 3, attachments: [], notes: [] }));
    } catch { throw new Error('旧浏览器文献数据无法解析，原数据已保留，请先恢复数据后重试迁移'); }
    if (!legacy.papers.length && !legacy.folders.length && !legacy.attachments.length && !legacy.notes.length && !legacy.annotations?.length) return current;
    if (!current.canWrite) throw new Error('本浏览器有尚未迁移的文献数据，需要知识库编辑权限才能保存到服务器；原数据已保留');
    const migration = { ...legacy, migrationSnapshots: [...(current.data.migrationSnapshots || []), { id: migrationId, createdAt: new Date().toISOString(), source: 'browser-local-v3', data: JSON.parse(raw) }] };
    const saved = await axiosInstance.post(`${core}/zotero/import`, { data: migration, source_id: 'browser-local-v3', idempotency_key: migrationId }, { params: { target_dataset_id: dataset }, timeout: 120000, silentError: true } as never);
    return decodePersistedLibrary(saved.data);
}
async function mutation(dataset: string, method: 'post' | 'patch', path: string, body: unknown): Promise<LibraryChanges> {
    const { data } = await axiosInstance.request({ method, url: `${core}/${path}`, data: body, params: { target_dataset_id: dataset }, timeout: 30000, silentError: true } as never);
    checkData(data.data);
    return data;
}
export function patchLibraryPaper(dataset: string, paper: PaperRecord, changes: {
    metadata?: PaperMetadata;
    status?: PaperRecord['status'];
    tags?: string[];
    primary_attachment_id?: string;
}) { return mutation(dataset, 'patch', `library/papers/${encodeURIComponent(paper.id)}`, { revision: paper.revision, ...changes }); }
const targets = (papers: PaperRecord[]) => papers.map(p => ({ id: p.id, revision: p.revision }));
export function moveLibraryPapers(dataset: string, papers: PaperRecord[], folderId: string | null) { return mutation(dataset, 'post', 'library/papers:move', { items: targets(papers), folder_id: folderId || '' }); }
export function removeLibraryPapers(dataset: string, papers: PaperRecord[]) { return mutation(dataset, 'post', 'library/papers:remove', { items: targets(papers) }); }
export function detachLibraryAttachment(dataset: string, paper: PaperRecord, id: string) { return mutation(dataset, 'post', `library/papers/${encodeURIComponent(paper.id)}/attachments/${encodeURIComponent(id)}:detach`, { revision: paper.revision }); }
export function addLibraryNote(dataset: string, paperId: string, id: string, content: string) { return mutation(dataset, 'post', `library/papers/${encodeURIComponent(paperId)}/notes`, { id, content }); }
export function createLibraryFolder(dataset: string, id: string, name: string, parentId: string | null) { return mutation(dataset, 'post', 'library/folders', { id, name, parent_id: parentId || '' }); }
export function renameLibraryFolder(dataset: string, folder: PaperFolder, name: string) { return mutation(dataset, 'patch', `library/folders/${encodeURIComponent(folder.id)}`, { revision: folder.revision, name }); }
export function deleteLibraryFolder(dataset: string, folder: PaperFolder) { return mutation(dataset, 'post', `library/folders/${encodeURIComponent(folder.id)}:delete`, { revision: folder.revision }); }
function importPaper(paper: PaperRecord) { const { id, metadata, folderId, source, sourceUrl, arxivVersion, tags } = paper; return { id, metadata, folderId, source, sourceUrl, arxivVersion, tags }; }
export function importLibraryPapers(dataset: string, papers: PaperRecord[], folders: PaperFolder[], key: string, format: 'bib' | 'ris' | 'csv', fileName?: string) { return mutation(dataset, 'post', 'imports', { entry_type: 'bibliography', target_dataset_id: dataset, idempotency_key: key, format, file_name: fileName, folders: folders.map(f => ({ id: f.id, name: f.name, parentId: f.parentId || null, revision: f.revision || 0 })), items: papers.map(paper => ({ paper: importPaper(paper) })) }); }
export function confirmLibraryPdf(dataset: string, paper: PaperRecord, documentId: string, key: string, version?: string) { return mutation(dataset, 'post', 'imports', { entry_type: 'pdf_upload', target_dataset_id: dataset, idempotency_key: key, items: [{ paper: importPaper(paper), document_id: documentId, revision: paper.revision, confirmed: true, candidate: { version } }] }); }
export function mergeLibraryChanges(current: PaperLibraryData, change: LibraryChanges): PaperLibraryData {
    const removed = new Set(change.removedPaperIds || []), changed = new Set(change.data.papers.map(p => p.id));
    const merge = <T extends {
        id: string;
    }>(old: T[], fresh: T[]) => { const updates = new Map(fresh.map(item => [item.id, item])); const previous = new Set(old.map(item => item.id)); return [...old.map(item => updates.get(item.id) || item), ...fresh.filter(item => !previous.has(item.id))]; };
    const data: PaperLibraryData = { ...current, ...change.data };
    data.papers = merge(current.papers.filter(p => !removed.has(p.id)), change.data.papers);
    data.folders = merge(current.folders.filter(f => !change.removedFolderIds?.includes(f.id)), change.data.folders);
    data.attachments = [...current.attachments.filter(a => !removed.has(a.paperId) && !changed.has(a.paperId)), ...change.data.attachments];
    data.notes = [...current.notes.filter(n => !removed.has(n.paperId) && !changed.has(n.paperId)), ...change.data.notes];
    data.annotations = merge((current.annotations || []).filter(item => !removed.has(item.paperId)), change.data.annotations || []);
    data.relations = merge((current.relations || []).filter(item => !removed.has(item.fromPaperId) && !removed.has(item.toPaperId)), change.data.relations || []);
    data.collections = merge((current.collections || []).filter(item => !change.removedFolderIds?.includes(item.id)), change.data.collections || []);
    return data;
}
export async function previewLibraryPapers(dataset: string, papers: PaperRecord[]): Promise<PaperRecord[]> {
    const { data } = await axiosInstance.post(`${core}/imports:preview`, { target_dataset_id: dataset, papers: papers.map(importPaper), resolve_external: false }, { silentError: true } as never);
    const ids = new Map<string, string>((data.items || []).map((item: {
        library_paper_id: string;
        work_id: string;
    }) => [item.library_paper_id, item.work_id]));
    return papers.map(p => ({ ...p, academicWorkId: ids.get(p.id) || p.academicWorkId }));
}
