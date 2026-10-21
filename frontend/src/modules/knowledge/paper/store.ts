import { emptyLibrary, normalizeLibrary, sampleLibrary, type PaperLibraryData } from "./model";
import { asLegacyLibrary, migratePaperLibrary, type PaperLibraryDataV3 } from "./libraryModel";

const kindKey = "lazymind.paper-library.ids";
const dataKey = (datasetId: string) => `lazymind.paper-library.data.v2.${datasetId}`;
const dataKeyV3 = (datasetId: string) => `lazymind.paper-library.data.v3.${datasetId}`;

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
  const modern = loadPaperLibraryV3(datasetId, preview);
  return asLegacyLibrary(modern);
}

export function loadPaperLibraryV3(datasetId: string, preview = false): PaperLibraryDataV3 {
  const modernRaw = localStorage.getItem(dataKeyV3(datasetId));
  if (modernRaw) {
    try {
      const parsed = migratePaperLibrary(JSON.parse(modernRaw));
      return parsed.attachments.length || !parsed.papers.length ? parsed : migratePaperLibrary(asLegacyLibrary(parsed));
    } catch { /* fall through */ }
  }
  const raw = localStorage.getItem(dataKey(datasetId));
  if (raw) {
    try {
      return migratePaperLibrary(normalizeLibrary(JSON.parse(raw) as PaperLibraryData));
    } catch {
      /* fall through */
    }
  }
  return migratePaperLibrary(preview ? sampleLibrary() : emptyLibrary());
}

export function savePaperLibrary(datasetId: string, data: PaperLibraryData) {
  localStorage.setItem(dataKey(datasetId), JSON.stringify(data));
  localStorage.setItem(dataKeyV3(datasetId), JSON.stringify(migratePaperLibrary(data)));
}

export function savePaperLibraryV3(datasetId: string, data: PaperLibraryDataV3) {
  localStorage.setItem(dataKeyV3(datasetId), JSON.stringify(data));
  localStorage.setItem(dataKey(datasetId), JSON.stringify(asLegacyLibrary(data)));
}
