import { loadLibraryDocuments, mergeLibraryDocuments, personalLibrary } from './libraryDocuments';
import type { PaperLibraryDataV3 } from './libraryModel';
import { loadPersistedPaperLibrary } from './store';

// Documents and their academic states cover local, arXiv and Workflow imports.
// Keep this join shared by the workbench and all citation exports.
export async function loadPaperLibrarySources(dataset: string, current: PaperLibraryDataV3) {
  const documents = await loadLibraryDocuments(dataset);
  if (!Array.isArray(documents.documents) || !Array.isArray(documents.states)) throw new Error('附件同步返回的数据不完整');
  return {
    data: mergeLibraryDocuments(current, documents), states: documents.states,
    active: documents.states.some(item => ['pending', 'processing'].includes(item.state.status)
        || item.processing_status === 'parsing'),
  };
}

export async function loadPaperLibrary(dataset: string) {
  const saved = await loadPersistedPaperLibrary(dataset);
  const source = await loadPaperLibrarySources(dataset, saved.data);
  return { ...saved, ...source, data: personalLibrary(source.data, source.states) };
}
