import type { DocumentAcademic } from '../api/paperWorkflows';
import type { PaperLibraryData } from './libraryModel';
import { isStoredPdfAttachment } from './paperFilters';

export function paperDocument(data: PaperLibraryData, id: string): DocumentAcademic | undefined {
  const paper = data.papers.find(item => item.id === id);
  const attachments = data.attachments.filter(item => paper?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
  const identified = attachments.filter(item => item.academicState?.document_id === item.contentRef);
  const attachment = identified.find(item => item.academicState?.kind === 'paper' && item.academicState.paper_id) || identified[0];
  return attachment?.academicState ? { state: attachment.academicState, is_important: !!paper?.important } : undefined;
}
