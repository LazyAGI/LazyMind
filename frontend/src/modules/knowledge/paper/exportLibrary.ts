import { serializeLibraryBackup } from './libraryBackup';
import { toBibTeX, toCSV, toRIS, type PaperRecord, type PaperLibraryData } from './model';
import { loadPersistedPaperLibrary } from './store';
export type PaperCitationFormat = 'bib' | 'ris' | 'csv';
export async function loadPaperLibraryForExport(datasetId: string): Promise<PaperLibraryData> {
    return (await loadPersistedPaperLibrary(datasetId)).data;
}
export function downloadPaperCitations(library: Pick<PaperLibraryData,'papers'|'folders'>, format: PaperCitationFormat, name = 'papers') {
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

export function toCslJson(items: PaperRecord[]) {
    return JSON.stringify(items.map(({ id, metadata }) => ({ id, type: 'article-journal', title: metadata.title,
        author: metadata.authors.map(literal => ({ literal })), issued: { 'date-parts': [[metadata.year]] },
        'container-title': metadata.venue, DOI: metadata.identifiers.doi,
        URL: metadata.identifiers.arxiv ? `https://arxiv.org/abs/${metadata.identifiers.arxiv}` : undefined,
    })), null, 2);
}
export { serializeLibraryBackup, parseLibraryBackup, prepareLibraryRestore } from './libraryBackup';
export function downloadLibraryJson(data: PaperLibraryData, name: string, backup = false, datasetId?: string) {
    const content = backup ? serializeLibraryBackup(data, datasetId) : toCslJson(data.papers);
    const url = URL.createObjectURL(new Blob([content], { type: 'application/json;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `${name.replace(/[\\/:*?"<>|]/g, '_').trim() || 'papers'}${backup ? '-backup' : '-csl'}.json`;
    document.body.appendChild(link); link.click(); link.remove(); URL.revokeObjectURL(url);
}
