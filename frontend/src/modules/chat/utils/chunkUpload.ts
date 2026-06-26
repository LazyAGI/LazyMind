import { axiosInstance } from "@/components/request";
import i18n from "@/i18n";
import { coreApiUrl } from "@/runtime/apiBase";
import {
  InitUploadRequest,
  InitUploadResponse,
  CompleteUploadRequest,
  CompleteUploadResponse,
  UploadPartResponse,
} from "@/api/generated/core-client";

const DEFAULT_CHUNK_SIZE = 5 * 1024 * 1024;

export interface ChunkUploadProgress {
  uploadedParts: number;
  totalParts: number;
  uploadedBytes: number;
  totalBytes: number;
  percentage: number;
}

export interface ChunkUploadOptions {
  onProgress?: (progress: ChunkUploadProgress) => void;
  chunkSize?: number;
  timeout?: number;
}


export async function uploadFileInChunks(
  file: File,
  options: ChunkUploadOptions = {},
): Promise<string> {
  const {
    onProgress,
    chunkSize = DEFAULT_CHUNK_SIZE,
    timeout = 60 * 1000,
  } = options;

  try {
    const initRequest: InitUploadRequest = {
      filename: file.name,
      file_size: file.size,
      content_type: file.type,
      part_size: chunkSize,
    };

    const initResponse = await axiosInstance.post<InitUploadResponse>(
      coreApiUrl("temp/uploads:initUpload"),
      initRequest,
      { timeout },
    );

    const { upload_id, part_size, total_parts } = initResponse.data;

    if (!upload_id) {
      throw new Error(i18n.t("chat.chunkUploadInitFailedNoUploadId"));
    }

    const actualPartSize = part_size || chunkSize;
    const actualTotalParts = total_parts || Math.ceil(file.size / actualPartSize);

    let uploadedBytes = 0;
    for (let partNumber = 1; partNumber <= actualTotalParts; partNumber++) {
      const start = (partNumber - 1) * actualPartSize;
      const end = Math.min(start + actualPartSize, file.size);
      const chunk = file.slice(start, end);

      await axiosInstance.put<UploadPartResponse>(
        coreApiUrl(`temp/uploads/${encodeURIComponent(upload_id)}/parts/${partNumber}`),
        chunk,
        {
          headers: {
            "Content-Type": "application/octet-stream",
          },
          timeout,
        },
      );

      uploadedBytes += chunk.size;

      if (onProgress) {
        onProgress({
          uploadedParts: partNumber,
          totalParts: actualTotalParts,
          uploadedBytes,
          totalBytes: file.size,
          percentage: Math.round((uploadedBytes / file.size) * 100),
        });
      }
    }

    const completeRequest: CompleteUploadRequest = {
      auto_start: false,
    };

    const completeResponse = await axiosInstance.post<CompleteUploadResponse>(
      coreApiUrl(`temp/uploads/${encodeURIComponent(upload_id)}:complete`),
      completeRequest,
      { timeout },
    );

    const storedPath = completeResponse.data.stored_path;
    if (!storedPath) {
      throw new Error(i18n.t("chat.chunkUploadCompleteFailedNoStoredPath"));
    }

    return storedPath;
  } catch (error) {
    console.error(i18n.t("chat.chunkUploadFailedLog"), error);
    throw error;
  }
}


export async function abortUpload(uploadId: string): Promise<void> {
  try {
    await axiosInstance.post(
      coreApiUrl(`temp/uploads/${encodeURIComponent(uploadId)}:abort`),
      {},
    );
  } catch (error) {
    console.error(i18n.t("chat.chunkUploadAbortFailedLog"), error);
    throw error;
  }
}
