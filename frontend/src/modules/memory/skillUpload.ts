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
export const MAX_SKILL_PACKAGE_SIZE = 100 * 1024 * 1024;

export function getSkillPackageValidationError(file: File): "type" | "size" | null {
  if (!file.name.toLowerCase().endsWith(".zip")) return "type";
  if (file.size > MAX_SKILL_PACKAGE_SIZE) return "size";
  return null;
}

export interface SkillTempUploadResult {
  uploadId: string;
  storedPath: string;
  fileUrl?: string;
}

function flattenErrorText(value: unknown, depth = 0): string {
  if (value == null || depth > 6) return "";
  if (typeof value !== "object") return String(value);
  return Object.values(value as Record<string, unknown>)
    .map((item) => flattenErrorText(item, depth + 1))
    .filter(Boolean)
    .join(" ");
}

export function isInvalidSkillPackageError(error: unknown): boolean {
  const response = (error as { response?: { status?: number; data?: unknown } } | null)?.response;
  if (response?.status !== 422) return false;
  return /skill[ _-]?(?:md|package)|SKILL\.md/i.test(flattenErrorText(response.data));
}

export async function uploadSkillTempFile(
  file: File,
  options?: {
    chunkSize?: number;
    timeout?: number;
    silentError?: boolean;
    signal?: AbortSignal;
    onProgress?: (percent: number) => void;
  },
): Promise<SkillTempUploadResult> {
  options?.signal?.throwIfAborted();
  const validationError = getSkillPackageValidationError(file);
  if (validationError) {
    throw Object.assign(new Error("Invalid skill package"), {
      code: validationError === "size" ? "payload_too_large" : "invalid_skill_package",
    });
  }
  const reportProgress = (loaded: number) => options?.onProgress?.(
    file.size ? Math.min(100, Math.round((loaded / file.size) * 100)) : 100,
  );
  reportProgress(0);
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
    { timeout, silentError: options?.silentError, signal: options?.signal } as never,
  );

  options?.signal?.throwIfAborted();
  const { upload_id: uploadId, part_size: partSize, total_parts: totalParts } = initResponse.data;
  if (!uploadId) {
    throw new Error("Missing upload_id from temp upload init");
  }

  const actualPartSize = partSize || chunkSize;
  const actualTotalParts = totalParts || Math.ceil(file.size / actualPartSize);

  for (let partNumber = 1; partNumber <= actualTotalParts; partNumber += 1) {
    options?.signal?.throwIfAborted();
    const start = (partNumber - 1) * actualPartSize;
    const end = Math.min(start + actualPartSize, file.size);
    const chunk = file.slice(start, end);

    await axiosInstance.put<UploadPartResponse>(
      coreApiUrl(`temp/uploads/${encodeURIComponent(uploadId)}/parts/${partNumber}`),
      chunk,
      {
        headers: { "Content-Type": "application/octet-stream" },
        timeout,
        silentError: options?.silentError,
        signal: options?.signal,
        onUploadProgress: (event: { loaded: number }) => {
          if (!options?.signal?.aborted) reportProgress(Math.min(start + event.loaded, end));
        },
      } as never,
    );
    options?.signal?.throwIfAborted();
    reportProgress(end);
  }

  const completeRequest: CompleteUploadRequest = { auto_start: false };
  const completeResponse = await axiosInstance.post<CompleteUploadResponse>(
    coreApiUrl(`temp/uploads/${encodeURIComponent(uploadId)}:complete`),
    completeRequest,
    { timeout, silentError: options?.silentError, signal: options?.signal } as never,
  );

  options?.signal?.throwIfAborted();
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
