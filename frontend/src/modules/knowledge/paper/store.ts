import type { PaperLibraryData, PaperRecord, PaperMetadata, PaperFolder } from './model';
import { emptyLibrary } from './libraryModel';
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
export async function loadPersistedPaperLibrary(dataset: string): Promise<PersistedPaperLibrary> {
    const { data } = await axiosInstance.get(`${core}/library`, { params: { target_dataset_id: dataset }, timeout: 30000, silentError: true } as never);
    if (typeof data.can_write !== 'boolean')
        throw new Error('服务器论文库权限响应无效');
    return { data: checkData(data.data), canWrite: data.can_write, paperCount: data.paper_count, pdfAttachmentCount: data.pdf_attachment_count, active: data.active };
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
    const data = emptyLibrary();
    data.papers = merge(current.papers.filter(p => !removed.has(p.id)), change.data.papers);
    data.folders = merge(current.folders.filter(f => !change.removedFolderIds?.includes(f.id)), change.data.folders);
    data.attachments = [...current.attachments.filter(a => !removed.has(a.paperId) && !changed.has(a.paperId)), ...change.data.attachments];
    data.notes = [...current.notes.filter(n => !removed.has(n.paperId) && !changed.has(n.paperId)), ...change.data.notes];
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
