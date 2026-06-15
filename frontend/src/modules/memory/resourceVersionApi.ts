import {
  Configuration as CoreConfiguration,
  ResourceVersionsApiFactory,
  type ResourceVersionOpenAPIResponse,
} from "@/api/generated/core-client";
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

const resourceVersionsApi = ResourceVersionsApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

type WrappedResourceVersionListResponse = {
  data?: {
    items?: ResourceVersionOpenAPIResponse[];
    page?: number;
    page_size?: number;
    total?: number;
  };
  items?: ResourceVersionOpenAPIResponse[];
  page?: number;
  page_size?: number;
  total?: number;
};

type WrappedResourceVersionResponse = {
  data?: ResourceVersionOpenAPIResponse;
};

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
  const response = await resourceVersionsApi.apiCoreResourceVersionsGet({
    page: options.page ?? 1,
    pageSize: options.pageSize ?? 20,
    resourceType: options.resourceType,
    resourceId: options.resourceId,
  });
  const payload = response.data as WrappedResourceVersionListResponse;
  const body = payload.data || payload;
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
  const response = await resourceVersionsApi.apiCoreResourceVersionsVersionIdGet({
    versionId,
  });
  const payload = response.data as WrappedResourceVersionResponse | ResourceVersionOpenAPIResponse;
  const item = "data" in payload && payload.data ? payload.data : payload;

  return normalizeResourceVersion(item as ResourceVersionOpenAPIResponse);
}
