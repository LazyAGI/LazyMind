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

export function isPaperLibrary(datasetId?: string, libraryKind?: "paper" | "document") {
  if (!datasetId) return false;
  if (libraryKind !== undefined) return libraryKind === "paper";
  return readIds().includes(datasetId);
}

export function markPaperLibrary(datasetId: string) {
  const ids = new Set(readIds());
  ids.add(datasetId);
  localStorage.setItem(kindKey, JSON.stringify([...ids]));
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
      return parsed;
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
  saveLibraryRecords(datasetId, JSON.stringify(migratePaperLibrary(data)), JSON.stringify(data));
}

export function savePaperLibraryV3(datasetId: string, data: PaperLibraryDataV3) {
  saveLibraryRecords(datasetId, JSON.stringify(data), JSON.stringify(asLegacyLibrary(data)));
}

function saveLibraryRecords(datasetId: string, modern: string, legacy: string) {
  const records = [[dataKeyV3(datasetId), modern], [dataKey(datasetId), legacy]];
  const previous = records.map(([key]) => localStorage.getItem(key));
  const written: number[] = [];
  try {
    records.forEach(([key, value], index) => {
      localStorage.setItem(key, value);
      written.push(index);
    });
  } catch (error) {
    const failures: unknown[] = [error];
    written.reverse().forEach((index) => {
      try {
        const old = previous[index];
        if (old === null) localStorage.removeItem(records[index][0]);
        else localStorage.setItem(records[index][0], old);
      } catch (rollbackError) { failures.push(rollbackError); }
    });
    if (failures.length > 1) throw Object.assign(new Error("文献保存失败，恢复原数据也失败，请检查浏览器存储空间"), { errors: failures });
    throw error;
  }
}
