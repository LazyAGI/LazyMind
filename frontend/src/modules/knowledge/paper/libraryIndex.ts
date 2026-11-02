import type { PaperLibraryData } from './model';
import type { Attachment } from './libraryModel';
import { isReadablePdfAttachment, isStoredPdfAttachment } from './paperFilters';

/** Build once per snapshot instead of scanning the attachment graph for every row. */
export function indexLibrary(data: PaperLibraryData) {
  const papers = new Map(data.papers.map(paper => [paper.id, paper]));
  const memberships = new Map(data.papers.map(paper => [paper.id, new Set(paper.attachmentIds)]));
  const attachments = new Map<string, Attachment[]>();
  const valid: Attachment[] = [];
  for (const attachment of data.attachments) {
    if (!memberships.get(attachment.paperId)?.has(attachment.id)) continue;
    valid.push(attachment);
    if (attachment.available === false) continue;
    const children = attachments.get(attachment.paperId) || [];
    children.push(attachment); attachments.set(attachment.paperId, children);
  }
  const materials = new Map(data.papers.map(paper => {
    const children = attachments.get(paper.id) || [];
    return [paper.id, { pdfs: children.filter(isStoredPdfAttachment), links: children.filter(item => isReadablePdfAttachment(item) && !item.contentRef), supplements: children.filter(item => item.kind !== 'pdf'), attachments: children.length, notes: paper.noteIds.length }];
  }));
  const identities = new Map(data.papers.map(paper => {
    const identified = (materials.get(paper.id)?.pdfs || []).filter(item => item.academicState?.document_id === item.contentRef);
    const attachment = identified.find(item => item.academicState?.kind === 'paper' && item.academicState.paper_id) || identified[0];
    return [paper.id, attachment?.academicState ? { state: attachment.academicState, is_important: !!paper.important } : undefined];
  }));
  return { papers, materials, identities, attachments: valid };
}

/** Count each paper once in every ancestor, including multi-collection membership. */
export function indexFolderCounts(data: PaperLibraryData) {
  const folders = new Map(data.folders.map(folder => [folder.id, folder]));
  const counts = new Map(data.folders.map(folder => [folder.id, 0]));
  for (const paper of data.papers) {
    const visited = new Set<string>();
    for (const membership of [paper.folderId, ...(paper.collectionIds || [])]) {
      let id = membership;
      while (id && folders.has(id) && !visited.has(id)) {
        visited.add(id); counts.set(id, (counts.get(id) || 0) + 1);
        id = folders.get(id)?.parentId || null;
      }
    }
  }
  return { folders, counts };
}
