import type { ImportSource, PaperImportMethod, PaperFolder, PaperItem, PaperLibraryData } from './model';

export type AttachmentKind = 'pdf' | 'supplement' | 'snapshot' | 'other';
export type NoteKind = 'note' | 'ai_summary' | 'method' | 'conclusion';
export type AnnotationColor = 'yellow' | 'blue' | 'red' | 'green';

export interface PaperMetadata {
  abstract?: string;
  identifiers: { doi?: string; arxiv?: string; pmid?: string; isbn?: string };
  confidence: Partial<Record<'title' | 'authors' | 'venue' | 'year' | 'abstract', number>>;
}

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

export interface Annotation {
  id: string;
  paperId: string;
  attachmentId?: string;
  page?: number;
  quote?: string;
  comment: string;
  color: AnnotationColor;
  createdAt: string;
}

export interface Collection {
  id: string;
  name: string;
  description?: string;
}

export interface PaperRecord extends PaperItem {
  metadata: PaperMetadata;
  attachmentIds: string[];
  noteIds: string[];
  annotationIds: string[];
  collectionIds: string[];
  relatedPaperIds: string[];
}

export interface PaperRelation {
  id: string;
  fromPaperId: string;
  toPaperId: string;
  kind: 'related' | 'cites' | 'cited_by' | 'version_of';
}

export interface MergeSnapshot {
  id: string;
  createdAt: string;
  primaryId: string;
  duplicateId: string;
  before: PaperLibraryDataV3;
}

export interface PaperLibraryDataV3 {
  schemaVersion: 3;
  folders: PaperFolder[];
  collections: Collection[];
  papers: PaperRecord[];
  attachments: Attachment[];
  notes: Note[];
  annotations: Annotation[];
  relations: PaperRelation[];
  mergeSnapshots: MergeSnapshot[];
  hiddenDocumentIds?: string[];
  hiddenFolderIds?: string[];
  hiddenArxivWorkIds?: string[];
  hiddenArxivPapers?: PaperRecord[];
}

const now = () => new Date().toISOString();

export function emptyLibraryV3(): PaperLibraryDataV3 {
  return { schemaVersion: 3, folders: [], collections: [], papers: [], attachments: [], notes: [], annotations: [], relations: [], mergeSnapshots: [] };
}

export function createAttachment(input: Omit<Attachment, 'id' | 'addedAt'>): Attachment {
  return { ...input, id: `attachment-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, addedAt: now() };
}

export function createNote(paperId: string, content = '', kind: NoteKind = 'note'): Note {
  const timestamp = now();
  return { id: `note-${Date.now()}`, paperId, title: '新笔记', content, kind, createdAt: timestamp, updatedAt: timestamp };
}

export function asLegacyLibrary(data: PaperLibraryDataV3): PaperLibraryData {
  return { folders: data.folders, papers: data.papers.map(({ metadata, attachmentIds, noteIds, annotationIds, collectionIds, relatedPaperIds, ...paper }) => ({ ...paper, abstract: paper.abstract ?? metadata.abstract })) };
}

export function readingSummary(data: PaperLibraryDataV3, paperId: string) {
  const paper = data.papers.find((item) => item.id === paperId);
  if (!paper) return { attachments: 0, notes: 0, annotations: 0 };
  return { attachments: paper.attachmentIds.length, notes: paper.noteIds.length, annotations: paper.annotationIds.length };
}
