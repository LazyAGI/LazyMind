import type { LibraryImportContext } from "../api/academicReferences";
import type { PaperLibraryDataV3 } from "./libraryModel";

export function isPaperLibrary(scene?: string) {
  return scene === "academic_papers";
}

export interface PersistedPaperLibrary {
  data: PaperLibraryDataV3;
  revision: number;
  canWrite: boolean;
  paperCount?: number;
  pdfAttachmentCount?: number;
  paperImportance?: Record<string, boolean>;
  active?: boolean;
}

export interface LibraryAttachmentContext {
  operation: 'attach' | 'detach';
  paper_id: string;
  document_id: string;
  attachment_id: string;
  confirmed: boolean;
  idempotency_key: string;
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
  for (const key of ['hiddenDocumentIds', 'hiddenFolderIds', 'hiddenArxivWorkIds', 'hiddenArxivPapers']) {
    if (value[key] !== undefined && !Array.isArray(value[key])) throw new Error('服务器文献库关联数据格式无效');
  }
}

function decodePersistedLibrary(value: unknown): PersistedPaperLibrary {
  if (!object(value) || !Number.isSafeInteger(value.revision) || (value.revision as number) < 0 || typeof value.can_write !== 'boolean') {
    throw new Error('服务器文献库响应格式无效，请重试');
  }
  validateLibrary(value.data);
  return { data: value.data, revision: value.revision as number, canWrite: value.can_write,
    paperCount: typeof value.paper_count === 'number' ? value.paper_count : undefined,
    pdfAttachmentCount: typeof value.pdf_attachment_count === 'number' ? value.pdf_attachment_count : undefined,
    paperImportance: object(value.paper_importance) ? value.paper_importance as Record<string, boolean> : undefined,
    active: value.active === true,
  };
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
export async function savePersistedPaperLibrary(datasetId: string, data: PaperLibraryDataV3, revision: number, importContext?: LibraryImportContext, attachmentContext?: LibraryAttachmentContext): Promise<PersistedPaperLibrary> {
  validateLibrary(data);
  const { axiosInstance, BASE_URL } = await import('@/components/request');
  try {
    const response = await axiosInstance.put(`${BASE_URL}/api/core/academic/library`, { data, revision, ...(importContext ? { import_context: importContext } : {}), ...(attachmentContext ? { attachment_context: attachmentContext } : {}) }, {
      params: { target_dataset_id: datasetId }, timeout: 30_000, ...{ silentError: true },
    });
    return decodePersistedLibrary(response.data);
  } catch (error) { throw persistenceError(error); }
}

export async function loadPersistedPaperLibrary(datasetId: string): Promise<PersistedPaperLibrary> {
  return fetchPersistedLibrary(datasetId);
}

export async function setLibraryPaperImportance(datasetId: string, paperId: string, revision: number, important: boolean): Promise<PersistedPaperLibrary> {
  const { axiosInstance, BASE_URL } = await import('@/components/request');
  try {
    const response = await axiosInstance.put(`${BASE_URL}/api/core/academic/library/papers/${encodeURIComponent(paperId)}/importance`, { revision, is_important: important }, { params: { target_dataset_id: datasetId }, silentError: true } as never);
    return decodePersistedLibrary(response.data);
  } catch (error) { throw persistenceError(error); }
}
