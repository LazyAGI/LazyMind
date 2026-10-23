import type { Doc } from '@/api/generated/core-client';
import { documentAcademicStates, type DocumentAcademic } from '../api/paperWorkflows';
import { duplicateOf } from './model';
import type { PaperLibraryDataV3, PaperRecord } from './libraryModel';

export interface LibraryDocuments {
  documents: Partial<Doc>[];
  states: DocumentAcademic[];
}

export async function loadLibraryDocuments(dataset: string): Promise<LibraryDocuments> {
  const { DocumentServiceApi } = await import('../utils/request');
  const documents: Partial<Doc>[] = [];
  const folders = [''];
  const visited = new Set<string>();
  while (folders.length) {
    const parent = folders.shift()!;
    if (visited.has(parent)) continue;
    visited.add(parent);
    let token: string | undefined;
    do {
      const response = await DocumentServiceApi().documentServiceSearchDocuments({
        dataset, searchDocumentsRequest: { parent: "", p_id: parent, page_size: 200, page_token: token },
      });
      for (const document of response.data.documents || []) {
        documents.push(document);
        if (document.type === 'FOLDER' && document.document_id) folders.push(document.document_id);
      }
      token = response.data.next_page_token;
    } while (token);
  }
  const ids = documents.filter(item => item.display_name?.toLowerCase().endsWith('.pdf')).map(item => item.document_id!).filter(Boolean);
  const states: DocumentAcademic[] = [];
  for (let offset = 0; offset < ids.length; offset += 100) {
    states.push(...await documentAcademicStates(dataset, ids.slice(offset, offset + 100)));
  }
  return { documents, states };
}

export function mergeLibraryDocuments(current: PaperLibraryDataV3, source: LibraryDocuments): PaperLibraryDataV3 {
  const papers = [...current.papers];
  const attachments = [...current.attachments];
  const folders = [...current.folders];
  const states = new Map(source.states.map(item => [item.state.document_id, item]));
  const hidden = new Set(current.hiddenDocumentIds || []);
  const hiddenWorks = new Set(current.hiddenArxivWorkIds || []);
  for (const document of source.documents) {
    const id = document.document_id;
    if (!id || !document.display_name?.toLowerCase().endsWith('.pdf') || hidden.has(id)) continue;
    const identity = states.get(id);
    const work = identity?.state.kind === 'paper' ? identity.paper : undefined;
    if (work && hiddenWorks.has(work.paper_id)) continue;
    const attached = attachments.find(item => item.contentRef === id);
    const draft: PaperRecord = {
      id: `document-${id}`, title: work?.title || document.display_name.replace(/\.pdf$/i, ''),
      authors: work?.authors?.join('、') || '', year: work?.year || 0, venue: work?.venue || '',
      doi: work?.doi, arxiv: work?.arxiv_id, abstract: work?.abstract || undefined,
      academicWorkId: work?.paper_id, arxivVersion: identity?.source_version, folderId: null, status: 'unread', important: false,
      tags: document.tags || [], source: 'local', parse: 'parsing',
      metadata: { abstract: work?.abstract || undefined, identifiers: { doi: work?.doi, arxiv: work?.arxiv_id }, confidence: {} },
      attachmentIds: [], noteIds: [], annotationIds: [], collectionIds: [], relatedPaperIds: [],
    };
    const existing = papers.find(item => item.id === attached?.paperId)
      || (work && papers.find(item => item.academicWorkId === work.paper_id))
      || duplicateOf(papers, draft) as PaperRecord | undefined;
    const paper = existing ? { ...existing } : draft;
    const incomingVersion = (Number(identity?.source_version?.replace(/^v/, '')) || 0);
    const currentVersion = (Number(paper.arxivVersion?.replace(/^v/, '')) || 0);
    const useMetadata = !!work && (!existing?.academicWorkId || incomingVersion >= currentVersion);
    if (work && useMetadata) Object.assign(paper, {
      academicWorkId: work.paper_id, arxivVersion: identity?.source_version || paper.arxivVersion, title: work.title || paper.title, authors: work.authors?.join('、') || paper.authors,
      year: work.year || paper.year, venue: work.venue || paper.venue, doi: work.doi, arxiv: work.arxiv_id,
      abstract: work.abstract || undefined,
      metadata: { ...paper.metadata, abstract: work.abstract || undefined, identifiers: { ...paper.metadata.identifiers, doi: work.doi, arxiv: work.arxiv_id } },
    });
    if (!existing && document.p_id) {
      const folder = source.documents.find(item => item.document_id === document.p_id);
      if (folder) {
        const folderId = `document-folder-${folder.document_id}`;
        if (!folders.some(item => item.id === folderId)) folders.push({ id: folderId, name: folder.rel_path || folder.display_name || '原目录' });
        paper.folderId = folderId;
      }
    }
    if (useMetadata || !existing) {
      paper.parse = identity?.processing_status === 'ready' ? 'ready' : identity?.processing_status === 'failed' ? 'failed' : 'parsing';
      paper.processingError = identity?.processing_error || undefined;
    }
    const attachmentId = attached?.id || `document-attachment-${id}`;
    const promote = incomingVersion > currentVersion || !paper.attachmentIds.length;
    if (promote) for (let i = 0; i < attachments.length; i++) { if (attachments[i].paperId === paper.id) attachments[i] = { ...attachments[i], isPrimary: false }; }
    const attachment = { ...attached, sourceVersion: identity?.source_version, contentHash: identity?.content_sha256, id: attachmentId, paperId: paper.id, name: document.display_name,
      kind: 'pdf' as const, source: attached?.source || (identity?.source_provider === 'arxiv' ? 'arxiv' as const : 'local' as const), addedAt: attached?.addedAt || '',
      contentRef: id, isPrimary: promote || (attached?.isPrimary ?? false) };
    const attachmentIndex = attachments.findIndex(item => item.id === attachmentId);
    if (attachmentIndex < 0) attachments.push(attachment); else attachments[attachmentIndex] = attachment;
    paper.attachmentIds = [...new Set([...paper.attachmentIds, attachmentId])];
    const index = papers.findIndex(item => item.id === paper.id);
    if (index < 0) papers.push(paper); else papers[index] = paper;
  }
  return { ...current, folders, papers, attachments };
}

export function paperDocument(data: PaperLibraryDataV3, id: string, states: DocumentAcademic[]): DocumentAcademic | undefined {
  const paper = data.papers.find(item => item.id === id);
  const attachments = data.attachments.filter(item => paper?.attachmentIds.includes(item.id));
  return attachments.map(item => states.find(state => state.state.document_id === item.contentRef))
    .find(item => item?.state.kind === 'paper' && item.state.paper_id) || attachments.map(item => states.find(state => state.state.document_id === item.contentRef)).find(Boolean);
}

export function personalLibrary(data: PaperLibraryDataV3, states: DocumentAcademic[]): PaperLibraryDataV3 {
  return { ...data, papers: data.papers.map(paper => ({ ...paper, important: !!paperDocument(data, paper.id, states)?.is_important })) };
}

export function restoreLibraryPaper(current: PaperLibraryDataV3, workId: string): PaperLibraryDataV3 {
  const archived = current.hiddenArxivPapers?.find(paper => paper.academicWorkId === workId);
  const documents = new Set(current.attachments.filter(item => item.paperId === archived?.id).map(item => item.contentRef));
  return { ...current,
    papers: archived && !current.papers.some(paper => paper.id === archived.id)
      ? [...current.papers, { ...archived, folderId: current.folders.some(folder => folder.id === archived.folderId) ? archived.folderId : null, collectionIds: archived.collectionIds.filter(id => current.collections.some(collection => collection.id === id) || current.folders.some(folder => folder.id === id)) }]
      : current.papers,
    hiddenArxivWorkIds: current.hiddenArxivWorkIds?.filter(id => id !== workId),
    hiddenArxivPapers: current.hiddenArxivPapers?.filter(paper => paper.academicWorkId !== workId),
    hiddenDocumentIds: current.hiddenDocumentIds?.filter(id => !documents.has(id)),
  };
}
