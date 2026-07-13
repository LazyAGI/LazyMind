import { axiosInstance } from "@/components/request";
import { coreApiUrl } from "@/runtime/apiBase";
import type {
  CompleteUploadRequest,
  CompleteUploadResponse,
  InitUploadRequest,
  InitUploadResponse,
  UploadPartResponse,
} from "@/api/generated/core-client";

const DEFAULT_CHUNK_SIZE = 5 * 1024 * 1024;

export interface SkillTempUploadResult {
  uploadId: string;
  storedPath: string;
  fileUrl?: string;
}

export async function uploadSkillTempFile(
  file: File,
  options?: { chunkSize?: number; timeout?: number },
): Promise<SkillTempUploadResult> {
  const chunkSize = options?.chunkSize ?? DEFAULT_CHUNK_SIZE;
  const timeout = options?.timeout ?? 60 * 1000;

  const initRequest: InitUploadRequest = {
    filename: file.name,
    file_size: file.size,
    content_type: file.type || "application/octet-stream",
    part_size: chunkSize,
  };

  const initResponse = await axiosInstance.post<InitUploadResponse>(
    coreApiUrl("temp/uploads:initUpload"),
    initRequest,
    { timeout },
  );

  const { upload_id: uploadId, part_size: partSize, total_parts: totalParts } = initResponse.data;
  if (!uploadId) {
    throw new Error("Missing upload_id from temp upload init");
  }

  const actualPartSize = partSize || chunkSize;
  const actualTotalParts = totalParts || Math.ceil(file.size / actualPartSize);

  for (let partNumber = 1; partNumber <= actualTotalParts; partNumber += 1) {
    const start = (partNumber - 1) * actualPartSize;
    const end = Math.min(start + actualPartSize, file.size);
    const chunk = file.slice(start, end);

    await axiosInstance.put<UploadPartResponse>(
      coreApiUrl(`temp/uploads/${encodeURIComponent(uploadId)}/parts/${partNumber}`),
      chunk,
      {
        headers: { "Content-Type": "application/octet-stream" },
        timeout,
      },
    );
  }

  const completeRequest: CompleteUploadRequest = { auto_start: false };
  const completeResponse = await axiosInstance.post<CompleteUploadResponse>(
    coreApiUrl(`temp/uploads/${encodeURIComponent(uploadId)}:complete`),
    completeRequest,
    { timeout },
  );

  const storedPath = completeResponse.data.stored_path;
  if (!storedPath) {
    throw new Error("Missing stored_path from temp upload complete");
  }

  return {
    uploadId,
    storedPath,
    fileUrl: completeResponse.data.file_url,
  };
}
