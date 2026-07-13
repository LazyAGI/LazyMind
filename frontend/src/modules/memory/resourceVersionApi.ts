import { axiosInstance, BASE_URL } from "@/components/request";

export type ResourceVersionType = "skill" | "memory" | "user_preference";

export interface ResourceVersionRecord {
  id: string;
  resourceType: ResourceVersionType | string;
  resourceId: string;
  userId: string;
  changeSource: string;
  fromVersion: number;
  toVersion: number;
  sourceRefType: string;
  sourceRefId: string;
  beforeContent: string;
  afterContent: string;
  diff: string;
  createdAt: string;
}

export interface ResourceVersionListOptions {
  resourceType?: ResourceVersionType;
  resourceId?: string;
  page?: number;
  pageSize?: number;
}

export interface ResourceVersionListResult {
  items: ResourceVersionRecord[];
  page: number;
  pageSize: number;
  total: number;
}

interface ResourceVersionOpenAPIResponse {
  id: string;
  resource_type: string;
  resource_id: string;
  user_id: string;
  change_source: string;
  from_version: number;
  to_version: number;
  source_ref_type: string;
  source_ref_id: string;
  before_content: string;
  after_content: string;
  diff: string;
  created_at: string;
}

const coreBasePath = `${BASE_URL}/api/core`;

type ApiEnvelope<T> = {
  code?: number;
  data?: T;
};

type WrappedResourceVersionListResponse = {
  items?: ResourceVersionOpenAPIResponse[];
  page?: number;
  page_size?: number;
  total?: number;
};

type WrappedResourceVersionResponse = ResourceVersionOpenAPIResponse;

const toNumberValue = (value: unknown, fallback = 0) => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return fallback;
};

const unwrapEnvelope = <T>(payload: unknown): T => {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
};

const normalizeResourceVersion = (
  item: ResourceVersionOpenAPIResponse,
): ResourceVersionRecord => ({
  id: item.id,
  resourceType: item.resource_type,
  resourceId: item.resource_id,
  userId: item.user_id,
  changeSource: item.change_source,
  fromVersion: item.from_version,
  toVersion: item.to_version,
  sourceRefType: item.source_ref_type,
  sourceRefId: item.source_ref_id,
  beforeContent: item.before_content,
  afterContent: item.after_content,
  diff: item.diff,
  createdAt: item.created_at,
});

export async function listResourceVersions(
  options: ResourceVersionListOptions,
): Promise<ResourceVersionListResult> {
  const response = await axiosInstance.get(`${coreBasePath}/resource-versions`, {
    params: {
      page: options.page ?? 1,
      page_size: options.pageSize ?? 20,
      resource_type: options.resourceType,
      resource_id: options.resourceId,
    },
  });
  const body = unwrapEnvelope<WrappedResourceVersionListResponse>(response.data);
  const items = (body.items || []).map(normalizeResourceVersion);

  return {
    items,
    page: Math.max(1, toNumberValue(body.page, options.page ?? 1)),
    pageSize: Math.max(1, toNumberValue(body.page_size, options.pageSize ?? 20)),
    total: Math.max(items.length, toNumberValue(body.total, items.length)),
  };
}

export async function getResourceVersion(
  versionId: string,
): Promise<ResourceVersionRecord> {
  const response = await axiosInstance.get(
    `${coreBasePath}/resource-versions/${encodeURIComponent(versionId)}`,
  );
  const item = unwrapEnvelope<WrappedResourceVersionResponse>(response.data);
  return normalizeResourceVersion(item);
}
