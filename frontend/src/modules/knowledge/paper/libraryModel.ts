import type { ImportSource, PaperImportMethod } from './model';
export type { PaperRecord, PaperLibraryData, PaperMetadata } from './model';
export type AttachmentKind = 'pdf' | 'supplement' | 'snapshot' | 'other';
export type NoteKind = 'note' | 'ai_summary' | 'method' | 'conclusion';
export interface Attachment {
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
