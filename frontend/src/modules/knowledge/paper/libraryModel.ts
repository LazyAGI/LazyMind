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
  hiddenArxivWorkIds?: string[];
  hiddenArxivPapers?: PaperRecord[];
  /** Original browser records retained on the server after legacy migration. */
  migrationSnapshots?: { id: string; createdAt: string; source: 'browser-local-v3'; data: PaperLibraryDataV3 }[];
}

const now = () => new Date().toISOString();

export function emptyLibraryV3(): PaperLibraryDataV3 {
  return { schemaVersion: 3, folders: [], collections: [], papers: [], attachments: [], notes: [], annotations: [], relations: [], mergeSnapshots: [] };
}

export function migratePaperLibrary(raw: unknown): PaperLibraryDataV3 {
  if (raw && typeof raw === 'object' && (raw as { schemaVersion?: number }).schemaVersion === 3) {
    const value = raw as PaperLibraryDataV3;
    return { ...emptyLibraryV3(), ...value, schemaVersion: 3 };
  }
  const legacy = (raw || {}) as Partial<PaperLibraryData>;
  const attachments: Attachment[] = [];
  const papers = (legacy.papers || []).map((item) => {
    const attachmentId = `attachment-${item.id}`;
    attachments.push({ id: attachmentId, paperId: item.id, name: `${item.title || 'paper'}.pdf`, kind: 'pdf', mimeType: 'application/pdf', source: item.source, addedAt: now(), isPrimary: true });
    return ({
    ...item,
    metadata: { identifiers: { doi: item.doi, arxiv: item.arxiv }, confidence: {} },
    attachmentIds: [attachmentId], noteIds: [], annotationIds: [], collectionIds: [], relatedPaperIds: [],
  });
  });
  return { ...emptyLibraryV3(), folders: legacy.folders || [], papers, attachments };
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
