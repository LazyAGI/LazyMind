import { axiosInstance, BASE_URL } from '@/components/request';
import type { PaperLibraryData } from './model';
import { decodePersistedLibrary, type PersistedPaperLibrary } from './store';
import {
  buildZoteroImport, downloadZoteroAttachment, matchLocalZoteroFile, zoteroImportError,
  type ZoteroAttachmentCandidate, type ZoteroImportConfig, type ZoteroImportOptions,
  type ZoteroImportResult, type ZoteroPreview,
} from './zoteroImport';

interface ConfirmedImport {
  datasetId: string;
  preview: ZoteroPreview;
  config: ZoteroImportConfig;
  options: ZoteroImportOptions;
  getLibrary: () => PaperLibraryData;
  saveLibrary: (library: PaperLibraryData) => Promise<void>;
  storeAttachment: (file: File, candidate: ZoteroAttachmentCandidate) => Promise<void>;
}

/** Persist the confirmed records first. Every attachment then has a durable, retryable target. */
export async function executeZoteroImport(input: ConfirmedImport): Promise<ZoteroImportResult> {
  const { datasetId, preview, config, options, getLibrary, saveLibrary, storeAttachment } = input;
  const plan = buildZoteroImport(preview, getLibrary(), options.targetFolderId);
  await saveLibrary(plan.library);
  const failures: string[] = [];
  let attachments = 0;
  if (options.includeAttachments) {
    for (const candidate of plan.attachments) {
      // The caller also checks that the same library is still mounted before each write.
      const authoritative = getLibrary().attachments.find(attachment => attachment.id === candidate.id);
      const target = authoritative ? { ...candidate, paperId: authoritative.paperId } : candidate;
      try {
        const localFile = matchLocalZoteroFile(candidate, options.localFiles);
        if (!localFile && config.source === 'local') throw new Error('未找到对应的本地 PDF，请选择 Zotero storage 或导出文件所在目录后重试');
        const file = localFile || await downloadZoteroAttachment(datasetId, config, candidate);
        await storeAttachment(file, target);
        attachments++;
      } catch (error) {
        failures.push(`${candidate.name}：${zoteroImportError(error, config)}`);
      }
    }
  }
  return { imported: plan.counts.papers, attachments, failures };
}

export async function storeZoteroAttachment(datasetId: string, file: File, candidate: ZoteroAttachmentCandidate): Promise<PersistedPaperLibrary> {
  const form = new FormData();
  form.append('file', file);
  form.append('paper_id', candidate.paperId);
  form.append('attachment_id', candidate.id);
  if (candidate.md5) form.append('expected_md5', candidate.md5);
  try {
    const response = await axiosInstance.post(`${BASE_URL}/api/core/academic/zotero/attachment/store`, form, {
      params: { target_dataset_id: datasetId }, timeout: 120_000, ...{ silentError: true },
    });
    return decodePersistedLibrary(response.data);
  } catch (error) {
    // A parsing failure can still return a durably saved file. Preserve that snapshot and surface the error.
    const body = (error as { response?: { data?: { library?: unknown } } })?.response?.data;
    let library: PersistedPaperLibrary | undefined;
    if (body?.library) {
      try { library = decodePersistedLibrary(body.library); } catch { /* Do not replace the original failure with an invalid optional snapshot. */ }
    }
    throw Object.assign(new Error(zoteroImportError(error)), { library, cause: error });
  }
}

/** The server atomically merges stable Zotero identities into authoritative entities. */
export async function importZoteroSnapshot(datasetId: string, data: PaperLibraryData, sourceId: string, idempotencyKey: string): Promise<PersistedPaperLibrary> {
  const response = await axiosInstance.post(`${BASE_URL}/api/core/academic/zotero/import`, { data, source_id: sourceId, idempotency_key: idempotencyKey }, {
    params: { target_dataset_id: datasetId }, timeout: 120_000, ...{ silentError: true },
  });
  return decodePersistedLibrary(response.data);
}
