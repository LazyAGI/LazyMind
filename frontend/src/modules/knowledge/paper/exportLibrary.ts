import type { PaperLibraryDataV3 } from './libraryModel';
import type { PaperItem } from './model';

export function toCslJson(items: PaperItem[]) {
  return JSON.stringify(items.map((item) => ({ id: item.id, type: 'article-journal', title: item.title, author: item.authors.split('、').filter(Boolean).map((literal) => ({ literal })), issued: { 'date-parts': [[item.year]] }, 'container-title': item.venue, DOI: item.doi, URL: item.arxiv ? `https://arxiv.org/abs/${item.arxiv}` : undefined })), null, 2);
}

export function serializeLibraryBackup(data: PaperLibraryDataV3) { return JSON.stringify(data, null, 2); }

export function parseLibraryBackup(text: string): PaperLibraryDataV3 {
  const value = JSON.parse(text) as Partial<PaperLibraryDataV3>;
  if (value.schemaVersion !== 3 || !Array.isArray(value.papers) || !Array.isArray(value.attachments)) throw new Error('不是有效的文献库备份');
  return value as PaperLibraryDataV3;
}
