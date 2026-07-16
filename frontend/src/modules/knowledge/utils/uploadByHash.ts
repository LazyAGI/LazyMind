import {
  TaskServiceApi,
  uploadLargeFileToDataset,
  type CreateTaskItem,
} from "@/modules/knowledge/utils/request";
import {
  CHECK_HASHES_BATCH_SIZE,
  computeFileSha256,
} from "@/modules/knowledge/utils/fileHash";

const LARGE_FILE_THRESHOLD = 10 * 1024 * 1024; // 10MB

export type ImportFileItem = {
  originFile: File;
  path: string;
};

export type BuildUploadTaskItemsOptions = {
  datasetId: string;
  fileItems: ImportFileItem[];
  tags?: string[];
  documentPid?: string;
  /** Include relative_path in each create-task item (folder import). */
  folderMode?: boolean;
};

type HashedFileItem = ImportFileItem & {
  contentHash: string;
  displayName: string;
};

/**
 * Hash → checkHashes → upload only missing unique hashes → build create-task items.
 * Identical content in one batch is uploaded once; other items reuse content_hash.
 */
export async function buildUploadTaskItems(
  options: BuildUploadTaskItemsOptions,
): Promise<CreateTaskItem[]> {
  const { datasetId, fileItems, tags, documentPid, folderMode } = options;
  if (!fileItems.length) {
    return [];
  }

  const hashedItems: HashedFileItem[] = [];
  for (const item of fileItems) {
    const contentHash = await computeFileSha256(item.originFile);
    hashedItems.push({
      ...item,
      contentHash,
      displayName: item.originFile.name,
    });
  }

  const uniqueHashes = [...new Set(hashedItems.map((item) => item.contentHash))];
  const missingHashes = await fetchMissingHashes(datasetId, uniqueHashes);
  const missingSet = new Set(missingHashes);

  // Upload each missing unique hash once (first file that carries it).
  const uploadFileIdByHash = new Map<string, string>();
  for (const hash of missingHashes) {
    const source = hashedItems.find((item) => item.contentHash === hash);
    if (!source) continue;

    const uploaded = await uploadSingleFile(datasetId, source, {
      documentPid,
      folderMode,
    });
    uploadFileIdByHash.set(hash, uploaded.uploadFileId);
  }

  const firstUploadUsed = new Set<string>();
  return hashedItems.map((item) => {
    const task = buildTaskPayload(item, { tags, documentPid, folderMode });

    if (missingSet.has(item.contentHash)) {
      const uploadFileId = uploadFileIdByHash.get(item.contentHash);
      if (!uploadFileId) {
        throw new Error(`missing upload for content hash ${item.contentHash}`);
      }
      // First occurrence of a newly uploaded hash uses upload_file_id;
      // other identical files in this batch reuse content_hash.
      if (!firstUploadUsed.has(item.contentHash)) {
        firstUploadUsed.add(item.contentHash);
        return { upload_file_id: uploadFileId, task };
      }
    }

    return { content_hash: item.contentHash, task };
  });
}

async function fetchMissingHashes(
  datasetId: string,
  hashes: string[],
): Promise<string[]> {
  if (!hashes.length) return [];

  const missing: string[] = [];
  for (let i = 0; i < hashes.length; i += CHECK_HASHES_BATCH_SIZE) {
    const batch = hashes.slice(i, i + CHECK_HASHES_BATCH_SIZE);
    const res = await TaskServiceApi().checkHashes(datasetId, batch);
    missing.push(...(res.data.missing_hashes || []));
  }
  return missing;
}

async function uploadSingleFile(
  datasetId: string,
  item: HashedFileItem,
  options: { documentPid?: string; folderMode?: boolean },
): Promise<{ uploadFileId: string; contentHash?: string }> {
  if (item.originFile.size > LARGE_FILE_THRESHOLD) {
    return uploadLargeFileToDataset(datasetId, item.originFile, {
      documentPid: options.documentPid,
      ...(options.folderMode ? { relativePath: item.path } : {}),
    });
  }

  const formData = new FormData();
  formData.append("files", item.originFile);
  if (options.documentPid) {
    formData.append("document_pid", options.documentPid);
  }
  if (options.folderMode) {
    formData.append("relative_path", item.path);
  }

  const uploadRes = await TaskServiceApi().uploadFiles(datasetId, formData);
  const uploaded = uploadRes.data.files?.[0];
  if (!uploaded?.upload_file_id) {
    throw new Error("upload result missing upload_file_id");
  }

  return {
    uploadFileId: uploaded.upload_file_id,
    contentHash: uploaded.content_hash || item.contentHash,
  };
}

function buildTaskPayload(
  item: HashedFileItem,
  options: {
    tags?: string[];
    documentPid?: string;
    folderMode?: boolean;
  },
) {
  return {
    display_name: item.displayName,
    ...(options.folderMode ? { relative_path: item.path } : {}),
    ...(options.documentPid ? { document_pid: options.documentPid } : {}),
    ...(options.tags?.length ? { document_tags: options.tags } : {}),
  };
}
