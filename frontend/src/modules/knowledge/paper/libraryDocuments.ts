import type { DocumentAcademic } from '../api/paperWorkflows';
import type { PaperLibraryData } from './libraryModel';
import { isStoredPdfAttachment } from './paperFilters';

export function paperDocument(data: PaperLibraryData, id: string, states: DocumentAcademic[]): DocumentAcademic | undefined {
  const paper = data.papers.find(item => item.id === id);
  const attachments = data.attachments.filter(item => paper?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
  return attachments.map(item => states.find(state => state.state.document_id === item.contentRef))
    .find(item => item?.state.kind === 'paper' && item.state.paper_id) || attachments.map(item => states.find(state => state.state.document_id === item.contentRef)).find(Boolean);
}
