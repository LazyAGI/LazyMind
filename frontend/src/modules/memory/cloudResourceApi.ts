import { axiosInstance, BASE_URL } from "@/components/request";

export type CloudResourceType = "skill" | "workflow";
export type CloudPresenceStatus =
  | "present_current"
  | "download_required"
  | "local_missing"
  | "cloud_updated"
  | "local_modified"
  | "diverged"
  | "incompatible";

export interface CloudResourceItem {
  resource_id: string;
  resource_type: CloudResourceType;
  resource_name: string;
  content_size: number;
  format_schema: string;
  updated_at: string;
  presence_status: CloudPresenceStatus;
  local_resource_id?: string;
  local_resource_ref?: string;
}

interface CloudResourcePage {
  items: CloudResourceItem[];
  next_cursor?: string;
}

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

export async function listCloudResources(resourceType: CloudResourceType): Promise<CloudResourceItem[]> {
  const response = await axiosInstance.get<CoreResponse<CloudResourcePage>>(
    `${BASE_URL}/api/core/cloud/${collection(resourceType)}`,
    { params: { page_size: 100 } },
  );
  return response.data.data?.items ?? [];
}

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
