import { axiosInstance, BASE_URL } from "@/components/request";

import type {
  CloudResourceMetadata as MetadataDTO,
  CloudResourceListItem,
  CloudResourcePage,
  CloudResourceTree as TreeDTO,
  CloudResourceContent as ContentDTO,
} from "@/api/generated/core-client";

export type CloudResourceType = CloudResourceListItem["resource_type"];
export type CloudPresenceStatus = CloudResourceListItem["presence_status"];
export type CloudResourceItem = CloudResourceListItem;

interface CloudDownloadResult {
  resource_id: string;
  local_resource_id: string;
  local_resource_ref?: string;
  already_present: boolean;
}

export type CloudUploadStatus =
  | "upload_not_required"
  | "upload_first"
  | "upload_update_available"
  | "cloud_updated"
  | "diverged"
  | "incompatible";

export interface CloudUploadResult {
  status: CloudUploadStatus;
  resource_id?: string;
}

interface CoreResponse<T> {
  data?: T;
}

function collection(resourceType: CloudResourceType) {
  return resourceType === "skill" ? "skills" : "workflows";
}

export async function listCloudResources(resourceType: CloudResourceType, options: { signal?: AbortSignal } = {}): Promise<CloudResourceItem[]> {
  const items: CloudResourceItem[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    options.signal?.throwIfAborted();
    const config = { params: { page_size: 100, ...(cursor ? { cursor } : {}) }, signal: options.signal, timeout: 15000, silentError: true };
    const response = await axiosInstance.get<CoreResponse<CloudResourcePage>>(`${BASE_URL}/api/core/cloud/${collection(resourceType)}`, config);
    const page = response.data.data;
    if (!page || !Array.isArray(page.items) || page.items.length > 100 || (page.next_cursor !== undefined && typeof page.next_cursor !== "string")) {
      throw new Error("Invalid Cloud resource page");
    }
    for (const item of page.items) {
      if (!item || typeof item.resource_id !== "string" || !item.resource_id || typeof item.resource_name !== "string" || typeof item.local_exists !== "boolean"
        || !["skill", "workflow"].includes(item.resource_type) || !Number.isSafeInteger(item.content_size) || item.content_size < 0
        || !["present_current", "download_required", "local_missing", "cloud_updated", "local_modified", "diverged", "incompatible"].includes(item.presence_status)
        || !Number.isFinite(Date.parse(item.updated_at))) throw new Error("Invalid Cloud resource item");
    }
    items.push(...page.items);
    cursor = page.next_cursor;
    if (cursor) {
      if (cursor.length > 2048 || seenCursors.has(cursor)) throw new Error("Invalid Cloud resource cursor");
      seenCursors.add(cursor);
    }
  } while (cursor);
  return items;
}

export type CloudResourceMetadata = MetadataDTO;
export type CloudResourceTree = TreeDTO;
export type CloudResourceFile = TreeDTO["files"][number];
export type CloudResourceContent = ContentDTO;

async function readCloudResource<T>(kind: CloudResourceType, id: string, suffix: string, signal?: AbortSignal, path?: string, hash?: string): Promise<T> {
  const config = { signal, timeout: 15000, silentError: true, ...(path ? { params: { path } } : {}), ...(hash ? { headers: { "If-Match": `"${hash}"` } } : {}) };
  const response = await axiosInstance.get<CoreResponse<T>>(`${BASE_URL}/api/core/cloud/${collection(kind)}/${encodeURIComponent(id)}${suffix}`, config);
  signal?.throwIfAborted();
  if (!response.data.data) throw new Error("Cloud resource read returned no data");
  return response.data.data;
}

export const getCloudResource = (kind: CloudResourceType, id: string, signal?: AbortSignal) => readCloudResource<CloudResourceMetadata>(kind, id, "", signal);
export const getCloudResourceTree = (kind: CloudResourceType, id: string, signal?: AbortSignal) => readCloudResource<CloudResourceTree>(kind, id, "/tree", signal);
export const getCloudResourceContent = (kind: CloudResourceType, id: string, path: string, hash: string, signal?: AbortSignal) => readCloudResource<CloudResourceContent>(kind, id, "/content", signal, path, hash);

export async function downloadCloudResource(
  resourceType: CloudResourceType,
  resourceId: string,
): Promise<CloudDownloadResult> {
  const response = await axiosInstance.post<CoreResponse<CloudDownloadResult>>(
    `${BASE_URL}/api/core/cloud/${collection(resourceType)}/${encodeURIComponent(resourceId)}:download`,
  );
  if (!response.data.data) {
    throw new Error("Cloud resource download returned no result");
  }
  return response.data.data;
}

export async function uploadCloudSkill(skillId: string): Promise<CloudUploadResult> {
  const response = await axiosInstance.post<CoreResponse<CloudUploadResult>>(
    `${BASE_URL}/api/core/cloud/skills/${encodeURIComponent(skillId)}:upload`,
  );
  if (!response.data.data?.status) {
    throw new Error("Cloud Skill upload returned no result");
  }
  return response.data.data;
}
