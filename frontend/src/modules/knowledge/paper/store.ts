import type { LibraryImportContext } from "../api/academicReferences";
import { emptyLibrary, normalizeLibrary, sampleLibrary, type PaperLibraryData } from "./model";
import { asLegacyLibrary, migratePaperLibrary, type PaperLibraryDataV3 } from "./libraryModel";

const dataKey = (datasetId: string) => `lazymind.paper-library.data.v2.${datasetId}`;
const dataKeyV3 = (datasetId: string) => `lazymind.paper-library.data.v3.${datasetId}`;

export function isPaperLibrary(scene?: string) {
  return scene === "academic_papers";
}

/** Read browser data only for the explicit demo. Production reads use loadPersistedPaperLibrary. */
export function loadPreviewPaperLibrary(datasetId: string, preview = false): PaperLibraryDataV3 {
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

/** Browser-only preview/legacy helper. Production mutations must use savePersistedPaperLibrary. */
export function savePreviewPaperLibrary(datasetId: string, data: PaperLibraryDataV3) {
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

export interface PersistedPaperLibrary {
  data: PaperLibraryDataV3;
  revision: number;
  canWrite: boolean;
}

const entityKeys = ['folders', 'collections', 'papers', 'attachments', 'notes', 'annotations', 'relations', 'mergeSnapshots'] as const;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

function validateLibrary(value: unknown): asserts value is PaperLibraryDataV3 {
  if (!object(value) || value.schemaVersion !== 3 || entityKeys.some((key) => !Array.isArray(value[key]))) {
    throw new Error('服务器文献库数据格式无效，已停止加载以保护原数据');
  }
  for (const key of entityKeys) {
    if (!(value[key] as unknown[]).every((item) => object(item) && typeof item.id === 'string' && item.id)) {
      throw new Error('服务器文献库记录格式无效，已停止加载以保护原数据');
    }
  }
  if (!(value.papers as unknown[]).every((item) => object(item) && typeof item.title === 'string' && Array.isArray(item.tags)
    && object(item.metadata) && object(item.metadata.identifiers) && object(item.metadata.confidence)
    && ['attachmentIds', 'noteIds', 'annotationIds', 'collectionIds', 'relatedPaperIds'].every((key) => Array.isArray(item[key])))) {
    throw new Error('服务器文献条目格式无效，已停止加载以保护原数据');
  }
  for (const key of ['hiddenDocumentIds', 'hiddenArxivWorkIds', 'hiddenArxivPapers']) {
    if (value[key] !== undefined && !Array.isArray(value[key])) throw new Error('服务器文献库关联数据格式无效');
  }
}

function decodePersistedLibrary(value: unknown): PersistedPaperLibrary {
  if (!object(value) || !Number.isSafeInteger(value.revision) || (value.revision as number) < 0 || typeof value.can_write !== 'boolean') {
    throw new Error('服务器文献库响应格式无效，请重试');
  }
  validateLibrary(value.data);
  return { data: value.data, revision: value.revision as number, canWrite: value.can_write };
}

function persistenceError(error: unknown): Error {
  const response = object(error) && object(error.response) ? error.response : undefined;
  const body = response && object(response.data) ? response.data : undefined;
  const detail = body && typeof body.message === 'string' && body.message.trim();
  const message = detail || (error instanceof Error ? error.message : '文献库请求失败，请重试');
  return Object.assign(new Error(message), { status: response?.status, cause: error });
}

async function fetchPersistedLibrary(datasetId: string): Promise<PersistedPaperLibrary> {
  const { axiosInstance, BASE_URL } = await import('@/components/request');
  try {
    const response = await axiosInstance.get(`${BASE_URL}/api/core/academic/library`, {
      params: { target_dataset_id: datasetId }, timeout: 30_000, ...{ silentError: true },
    });
    return decodePersistedLibrary(response.data);
  } catch (error) { throw persistenceError(error); }
}

/** Production writes never fall back to browser storage. A revision conflict must be resolved by reloading. */
export async function savePersistedPaperLibrary(datasetId: string, data: PaperLibraryDataV3, revision: number, importContext?: LibraryImportContext): Promise<PersistedPaperLibrary> {
  validateLibrary(data);
  const { axiosInstance, BASE_URL } = await import('@/components/request');
  try {
    const response = await axiosInstance.put(`${BASE_URL}/api/core/academic/library`, { data, revision, ...(importContext ? { import_context: importContext } : {}) }, {
      params: { target_dataset_id: datasetId }, timeout: 30_000, ...{ silentError: true },
    });
    return decodePersistedLibrary(response.data);
  } catch (error) { throw persistenceError(error); }
}

export async function loadPersistedPaperLibrary(datasetId: string): Promise<PersistedPaperLibrary> {
  return fetchPersistedLibrary(datasetId);
}
