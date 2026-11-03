import type { ImportSource, PaperImportMethod } from './model';
import type { DocumentAcademic } from '../api/paperWorkflows';
export type { PaperRecord, PaperLibraryData, PaperMetadata } from './model';
export type AttachmentKind = 'pdf' | 'supplement' | 'snapshot' | 'other';
export type NoteKind = 'note' | 'ai_summary' | 'method' | 'conclusion';
export interface Attachment {
    academicState?: DocumentAcademic['state'];
    id: string;
    paperId: string;
    name: string;
    kind: AttachmentKind;
    mimeType?: string;
    size?: number;
    source: ImportSource;
    sourceProvider?: string;
    importMethod?: PaperImportMethod;
    expansionId?: string;
    expansionVersion?: number;
    addedAt: string;
    isPrimary?: boolean;
    contentRef?: string;
    sourceVersion?: string;
    sourceUrl?: string;
    contentHash?: string;
    available?: boolean;
}
export interface Note {
    id: string;
    paperId: string;
    title: string;
    content: string;
    kind: NoteKind;
    createdAt: string;
    updatedAt: string;
}
export function emptyLibrary(): import('./model').PaperLibraryData { return { folders: [], papers: [], attachments: [], notes: [] }; }
export function readingSummary(data: import('./model').PaperLibraryData, paperId: string) { const paper = data.papers.find(item => item.id === paperId); return { attachments: paper?.attachmentIds.length || 0, notes: paper?.noteIds.length || 0 }; }

export interface Collection { id: string; name: string; parentId?: string | null; description?: string; zoteroSources?: string[] }
export interface Annotation { id: string; paperId: string; attachmentId?: string; page?: number; quote?: string; comment: string; color: 'yellow' | 'blue' | 'red' | 'green'; createdAt: string }
export interface PaperRelation { id: string; fromPaperId: string; toPaperId: string; kind: 'related' | 'cites' | 'cited_by' | 'version_of' }
export type CompletePaperLibrary = import('./model').PaperLibraryData & { collections: Collection[]; annotations: Annotation[]; relations: PaperRelation[]; mergeSnapshots: unknown[] };
/** Supply optional graph arrays while preserving every server-owned extension. */
export function withLibraryGraph(data: import('./model').PaperLibraryData): CompletePaperLibrary {
    return { ...data, collections: data.collections || [], annotations: data.annotations || [], relations: data.relations || [], mergeSnapshots: data.mergeSnapshots || [] };
}
