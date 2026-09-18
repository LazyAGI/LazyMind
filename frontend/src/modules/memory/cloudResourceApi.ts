import { cloudApi, cloudResourceApi, silentCloudRequest } from "@/api/cloudClient";

import type {
  CloudResourceMetadata as MetadataDTO,
  CloudResourceListItem,
  CloudResourceDownloadResult as CloudDownloadResult,
  CloudResourceUploadResult,
  CloudResourceTree as TreeDTO,
  CloudResourceContent as ContentDTO,
} from "@/api/generated/core-client";

export type CloudResourceType = CloudResourceListItem["resource_type"];
export type CloudPresenceStatus = CloudResourceListItem["presence_status"];
export type CloudResourceItem = CloudResourceListItem;

export type CloudUploadResult = CloudResourceUploadResult;
export type CloudUploadStatus = CloudUploadResult["status"];

export async function listCloudResources(resourceType: CloudResourceType, options: { signal?: AbortSignal } = {}): Promise<CloudResourceItem[]> {
  const items: CloudResourceItem[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    options.signal?.throwIfAborted();
    const config = { ...silentCloudRequest, signal: options.signal, timeout: 15000 };
    const list = resourceType === "skill" ? cloudApi.apiCoreCloudSkillsGet : cloudApi.apiCoreCloudWorkflowsGet;
    const response = await list({ pageSize: 100, cursor }, config);
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

function cloudReadOptions(signal?: AbortSignal) {
  return { ...silentCloudRequest, signal, timeout: 15000 };
}

async function readCloudResource<T>(request: Promise<{ data: { data?: T } }>, signal?: AbortSignal): Promise<T> {
  const response = await request;
  signal?.throwIfAborted();
  if (!response.data.data) throw new Error("Cloud resource read returned no data");
  return response.data.data;
}

export function getCloudResource(kind: CloudResourceType, id: string, signal?: AbortSignal): Promise<CloudResourceMetadata> {
  const get = kind === "skill" ? cloudResourceApi.apiCoreCloudSkillsResourceIdGet : cloudResourceApi.apiCoreCloudWorkflowsResourceIdGet;
  return readCloudResource(get({ resourceId: id }, cloudReadOptions(signal)), signal);
}

export function getCloudResourceTree(kind: CloudResourceType, id: string, signal?: AbortSignal): Promise<CloudResourceTree> {
  const get = kind === "skill" ? cloudResourceApi.apiCoreCloudSkillsResourceIdTreeGet : cloudResourceApi.apiCoreCloudWorkflowsResourceIdTreeGet;
  return readCloudResource(get({ resourceId: id }, cloudReadOptions(signal)), signal);
}

export function getCloudResourceContent(kind: CloudResourceType, id: string, path: string, hash: string, signal?: AbortSignal): Promise<CloudResourceContent> {
  const get = kind === "skill" ? cloudResourceApi.apiCoreCloudSkillsResourceIdContentGet : cloudResourceApi.apiCoreCloudWorkflowsResourceIdContentGet;
  return readCloudResource(get({ resourceId: id, path, ifMatch: `"${hash}"` }, cloudReadOptions(signal)), signal);
}

export async function downloadCloudResource(
  resourceType: CloudResourceType,
  resourceId: string,
): Promise<CloudDownloadResult> {
  const download = resourceType === "skill" ? cloudApi.apiCoreCloudSkillsResourceIdDownloadPost : cloudApi.apiCoreCloudWorkflowsResourceIdDownloadPost;
  const response = await download({ resourceId });
  if (!response.data.data) {
    throw new Error("Cloud resource download returned no result");
  }
  return response.data.data;
}

export async function uploadCloudSkill(skillId: string): Promise<CloudUploadResult> {
  const response = await cloudApi.apiCoreCloudSkillsSkillIdUploadPost({ skillId });
  if (!response.data.data?.status) {
    throw new Error("Cloud Skill upload returned no result");
  }
  return response.data.data;
}
