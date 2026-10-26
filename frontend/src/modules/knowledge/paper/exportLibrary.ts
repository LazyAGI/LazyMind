import { asLegacyLibrary } from './libraryModel';
import { toBibTeX, toCSV, toRIS, type PaperLibraryData } from './model';
import { loadPaperLibrary } from './libraryLoader';

export type PaperCitationFormat = 'bib' | 'ris' | 'csv';

export async function loadPaperLibraryForExport(datasetId: string): Promise<PaperLibraryData> {
  return asLegacyLibrary((await loadPaperLibrary(datasetId)).data);
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
