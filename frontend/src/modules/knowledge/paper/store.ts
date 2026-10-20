import { emptyLibrary, normalizeLibrary, sampleLibrary, type PaperLibraryData } from "./model";

const kindKey = "lazymind.paper-library.ids";
const dataKey = (datasetId: string) => `lazymind.paper-library.data.v2.${datasetId}`;

function readIds(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(kindKey) || "[]");
    return Array.isArray(parsed) ? parsed.filter((id) => typeof id === "string") : [];
  } catch {
    return [];
  }
}

export function isPaperLibrary(datasetId?: string) {
  return Boolean(datasetId && readIds().includes(datasetId));
}

export function markPaperLibrary(datasetId: string) {
  const ids = new Set(readIds());
  ids.add(datasetId);
  localStorage.setItem(kindKey, JSON.stringify([...ids]));
}

export function unmarkPaperLibrary(datasetId: string) {
  localStorage.setItem(kindKey, JSON.stringify(readIds().filter((id) => id !== datasetId)));
}

export function loadPaperLibrary(datasetId: string, preview = false): PaperLibraryData {
  const raw = localStorage.getItem(dataKey(datasetId));
  if (raw) {
    try {
      return normalizeLibrary(JSON.parse(raw) as PaperLibraryData);
    } catch {
      /* fall through */
    }
  }
  return preview ? sampleLibrary() : emptyLibrary();
}

export function savePaperLibrary(datasetId: string, data: PaperLibraryData) {
  localStorage.setItem(dataKey(datasetId), JSON.stringify(data));
}
