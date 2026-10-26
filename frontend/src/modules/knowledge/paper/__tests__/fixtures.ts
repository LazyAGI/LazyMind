import { emptyLibraryV3, type PaperLibraryDataV3 } from '../libraryModel';
import type { PaperItem, PaperLibraryData } from '../model';

export function paperFixture(overrides: Partial<PaperItem> = {}): PaperItem {
  return {
    id: 'paper', title: 'A study', authors: 'A. Author', venue: 'Journal', year: 2024,
    folderId: null, status: 'unread', important: false, tags: [], source: 'local', parse: 'ready',
    ...overrides,
  };
}

export function libraryFixture(input: Partial<PaperLibraryData> = {}): PaperLibraryDataV3 {
  const papers = (input.papers || [paperFixture()]).map(item => ({
    ...item, metadata: { identifiers: { doi: item.doi, arxiv: item.arxiv }, confidence: {} },
    attachmentIds: [`attachment-${item.id}`], noteIds: [], annotationIds: [], collectionIds: [], relatedPaperIds: [],
  }));
  return {
    ...emptyLibraryV3(), folders: input.folders || [], papers,
    attachments: papers.map(item => ({
      id: item.attachmentIds[0], paperId: item.id, name: `${item.title}.pdf`, kind: 'pdf',
      mimeType: 'application/pdf', source: item.source, addedAt: '2026-10-21T00:00:00Z', isPrimary: true,
    })),
  };
}
