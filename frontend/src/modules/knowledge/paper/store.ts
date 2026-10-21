import { emptyLibrary, normalizeLibrary, sampleLibrary, type PaperLibraryData } from "./model";
import { asLegacyLibrary, migratePaperLibrary, type PaperLibraryDataV3 } from "./libraryModel";
import { computeTextSha256 } from '../utils/fileHash';

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
  for (const key of ['hiddenArxivWorkIds', 'hiddenArxivPapers', 'migrationSnapshots']) {
    if (value[key] !== undefined && !Array.isArray(value[key])) throw new Error('服务器文献库关联数据格式无效');
  }
}

export function decodePersistedLibrary(value: unknown): PersistedPaperLibrary {
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
export async function savePersistedPaperLibrary(datasetId: string, data: PaperLibraryDataV3, revision: number): Promise<PersistedPaperLibrary> {
  validateLibrary(data);
  const { axiosInstance, BASE_URL } = await import('@/components/request');
  try {
    const response = await axiosInstance.put(`${BASE_URL}/api/core/academic/library`, { data, revision }, {
      params: { target_dataset_id: datasetId }, timeout: 30_000, ...{ silentError: true },
    });
    return decodePersistedLibrary(response.data);
  } catch (error) { throw persistenceError(error); }
}

function legacyBrowserCopy(datasetId: string): { data: PaperLibraryDataV3; raw: string } | undefined {
  let modern: string | null;
  let legacy: string | null;
  try {
    modern = localStorage.getItem(dataKeyV3(datasetId));
    legacy = localStorage.getItem(dataKey(datasetId));
  } catch { return undefined; } // Storage may be disabled; the server still works.
  if (!modern && !legacy) return undefined;
  try {
    const parsed = JSON.parse(modern || legacy!);
    if (!modern && (!object(parsed) || !Array.isArray(parsed.papers) || !Array.isArray(parsed.folders))) throw new Error('Invalid legacy library');
    const data = modern ? parsed : migratePaperLibrary(parsed);
    validateLibrary(data);
    return { data, raw: modern || legacy! };
  } catch {
    throw new Error('旧浏览器文献数据无法解析，原数据已保留，请先恢复数据后重试迁移');
  }
}

const unionRecords = <T extends { id: string }>(server: T[], legacy: T[]): T[] => {
  const existing = new Set(server.map((item) => item.id));
  return [...server, ...legacy.filter((item) => !existing.has(item.id))];
};

function migrateBrowserCopy(server: PaperLibraryDataV3, legacy: PaperLibraryDataV3, id: string): PaperLibraryDataV3 {
  const hidden = new Set(server.hiddenArxivWorkIds || []);
  const visibleWorks = new Set(server.papers.map((paper) => paper.academicWorkId).filter(Boolean));
  return {
    ...server,
    folders: unionRecords(server.folders, legacy.folders),
    collections: unionRecords(server.collections, legacy.collections),
    papers: unionRecords(server.papers, legacy.papers.filter((paper) => !paper.academicWorkId || !hidden.has(paper.academicWorkId))),
    attachments: unionRecords(server.attachments, legacy.attachments),
    notes: unionRecords(server.notes, legacy.notes),
    annotations: unionRecords(server.annotations, legacy.annotations),
    relations: unionRecords(server.relations, legacy.relations),
    mergeSnapshots: unionRecords(server.mergeSnapshots, legacy.mergeSnapshots),
    hiddenArxivWorkIds: [...new Set([...(server.hiddenArxivWorkIds || []), ...(legacy.hiddenArxivWorkIds || []).filter((workId) => !visibleWorks.has(workId))])],
    hiddenArxivPapers: unionRecords(server.hiddenArxivPapers || [], legacy.hiddenArxivPapers || []),
    migrationSnapshots: [...(server.migrationSnapshots || []), { id, createdAt: new Date().toISOString(), source: 'browser-local-v3', data: legacy }],
  };
}

/** Always read the server first. Old browser records are migrated durably, never used as an offline source of truth. */
export async function loadPersistedPaperLibrary(datasetId: string): Promise<PersistedPaperLibrary> {
  let current = await fetchPersistedLibrary(datasetId);
  const copy = legacyBrowserCopy(datasetId);
  const legacy = copy?.data;
  if (!legacy || !entityKeys.some((key) => legacy[key].length) && !legacy.hiddenArxivWorkIds?.length && !legacy.hiddenArxivPapers?.length) return current;
  const migrationId = `browser-${computeTextSha256(copy!.raw)}`;
  // A competing browser can update the library while its legacy copy is being merged.
  // Re-read and merge missing records only; never retry an ordinary edit against a newer revision.
  for (let attempt = 0; attempt < 3; attempt++) {
    if (current.data.migrationSnapshots?.some((snapshot) => snapshot.id === migrationId)) return current;
    if (!current.canWrite) throw new Error('本浏览器有尚未迁移的文献数据，需要知识库编辑权限才能保存到服务器；原数据已保留');
    try {
      return await savePersistedPaperLibrary(datasetId, migrateBrowserCopy(current.data, legacy, migrationId), current.revision);
    } catch (error) {
      if (!object(error) || error.status !== 409 || attempt === 2) throw error;
      current = await fetchPersistedLibrary(datasetId);
    }
  }
  throw new Error('文献库正在被其他用户更新，请重试迁移');
}
