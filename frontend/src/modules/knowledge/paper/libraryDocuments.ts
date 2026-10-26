import type { DocumentAcademic } from '../api/paperWorkflows';
import type { PaperLibraryDataV3 } from './libraryModel';
import { isStoredPdfAttachment } from './paperFilters';

export function paperDocument(data: PaperLibraryDataV3, id: string, states: DocumentAcademic[]): DocumentAcademic | undefined {
  const paper = data.papers.find(item => item.id === id);
  const attachments = data.attachments.filter(item => paper?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
  return attachments.map(item => states.find(state => state.state.document_id === item.contentRef))
    .find(item => item?.state.kind === 'paper' && item.state.paper_id) || attachments.map(item => states.find(state => state.state.document_id === item.contentRef)).find(Boolean);
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
