import { asLegacyLibrary, type PaperLibraryDataV3 } from './libraryModel';
import { toBibTeX, toCSV, toRIS, type PaperItem, type PaperLibraryData } from './model';
import { loadPersistedPaperLibrary } from './store';
import { listImportedArxivPapers } from './arxivImport';
import { mergeArxivImports } from './arxivLibrary';

export type PaperCitationFormat = 'bib' | 'ris' | 'csv';

export async function loadPaperLibraryForExport(datasetId: string): Promise<PaperLibraryData> {
  const persisted = await loadPersistedPaperLibrary(datasetId);
  const imported = await listImportedArxivPapers(datasetId);
  return asLegacyLibrary(mergeArxivImports(persisted.data, imported));
}

export function downloadPaperCitations(library: PaperLibraryData, format: PaperCitationFormat, name = 'papers') {
  const content = format === 'bib' ? toBibTeX(library.papers) : format === 'ris' ? toRIS(library.papers) : toCSV(library.papers, library.folders);
  const url = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = `${name.replace(/[\\/:*?"<>|]/g, '_').trim() || 'papers'}.${format}`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

export function toCslJson(items: PaperItem[]) {
  return JSON.stringify(items.map((item) => ({ id: item.id, type: 'article-journal', title: item.title, author: item.authors.split('、').filter(Boolean).map((literal) => ({ literal })), issued: { 'date-parts': [[item.year]] }, 'container-title': item.venue, DOI: item.doi, URL: item.arxiv ? `https://arxiv.org/abs/${item.arxiv}` : undefined })), null, 2);
}

export function serializeLibraryBackup(data: PaperLibraryDataV3) { return JSON.stringify(data, null, 2); }

export function parseLibraryBackup(text: string): PaperLibraryDataV3 {
  const value = JSON.parse(text) as Partial<PaperLibraryDataV3>;
  if (value.schemaVersion !== 3 || !Array.isArray(value.papers) || !Array.isArray(value.attachments)) throw new Error('不是有效的文献库备份');
  return value as PaperLibraryDataV3;
}
