import { axiosInstance, BASE_URL } from '@/components/request';
import type { PaperLibraryData } from './model';
import { decodePersistedLibrary, type PersistedPaperLibrary } from './store';
import {
  buildZoteroImport, matchLocalZoteroFile, zoteroImportError,
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
  options.onProgress?.(`正在保存 ${plan.counts.papers} 篇文献`);
  await saveLibrary(plan.library);
  const failures: string[] = [];
  let attachments = 0;
  if (options.includeAttachments) {
    const filesByName = new Map<string, File[]>();
    for (const file of options.localFiles) {
      const group = filesByName.get(file.name);
      if (group) group.push(file); else filesByName.set(file.name, [file]);
    }
    let indexedLibrary: PaperLibraryData | undefined;
    let attachmentsById = new Map<string, PaperLibraryData['attachments'][number]>();
    const concurrency = config.source !== 'local' && !options.localFiles.length ? 3 : 1;
    let next = 0, completed = 0;
    const outcomes: string[] = [];
    const worker = async () => {
      for (;;) {
        const index = next++;
        if (index >= plan.attachments.length) return;
        const candidate = plan.attachments[index];
        const currentLibrary = getLibrary();
        if (currentLibrary !== indexedLibrary) {
          attachmentsById = new Map(currentLibrary.attachments.map(attachment => [attachment.id, attachment]));
          indexedLibrary = currentLibrary;
        }
        const authoritative = attachmentsById.get(candidate.id);
        const target = authoritative ? { ...candidate, paperId: authoritative.paperId } : candidate;
        try {
          const localFile = matchLocalZoteroFile(candidate, filesByName.get(candidate.name) || []);
          if (localFile) await storeAttachment(localFile, target);
          else if (config.source === 'local') throw new Error('未找到对应的本地 PDF，请选择 Zotero storage 或导出文件所在目录后重试');
          else await transferZoteroAttachment(datasetId, config, target);
          attachments++;
        } catch (error) {
          outcomes[index] = `${candidate.name}：${zoteroImportError(error, config)}`;
        } finally {
          completed++;
          options.onProgress?.(`正在保存附件 ${completed}/${plan.attachments.length}`);
        }
      }
    };
    // Drain in-flight work even if another worker notices an unmounted library.
    const settled = await Promise.allSettled(Array.from({ length: Math.min(concurrency, plan.attachments.length) }, worker));
    const rejected = settled.find(result => result.status === 'rejected');
    if (rejected?.status === 'rejected') throw rejected.reason;
    failures.push(...outcomes.filter(Boolean));
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
export async function importZoteroSnapshot(datasetId: string, data: PaperLibraryData, sourceId: string, idempotencyKey: string, sourceKeys?: string[]): Promise<PersistedPaperLibrary> {
  const response = await axiosInstance.post(`${BASE_URL}/api/core/academic/zotero/import`, { data, source_id: sourceId, idempotency_key: idempotencyKey, source_keys: sourceKeys }, {
    params: { target_dataset_id: datasetId }, timeout: 120_000, ...{ silentError: true },
  });
  return decodePersistedLibrary(response.data);
}

/** PDF bytes never travel through the browser for server-accessible sources. */
export async function transferZoteroAttachment(datasetId: string, config: ZoteroImportConfig, candidate: ZoteroAttachmentCandidate): Promise<string> {
  const response = await axiosInstance.post(`${BASE_URL}/api/core/academic/zotero/attachment/transfer`, {
    ...config, item_key: candidate.key, paper_id: candidate.paperId, attachment_id: candidate.id,
    ...(candidate.md5 ? { expected_md5: candidate.md5 } : {}),
  }, { params: { target_dataset_id: datasetId }, timeout: 125_000, ...{ silentError: true } });
  const result = response.data;
  if (result?.attachment_id !== candidate.id || typeof result?.document_id !== 'string' || !result.document_id.trim()) {
    throw new Error('附件搬运未返回有效保存结果，请刷新论文库后重试');
  }
  return result.document_id;
}
