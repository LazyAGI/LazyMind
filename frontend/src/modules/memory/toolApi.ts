import {
  Configuration as CoreConfiguration,
  McpServersApiFactory,
  ToolsApiFactory,
  type CheckResponse,
  type CreateServerRequest,
  type DiscoverResponse,
  type ListServersResponse,
  type ServerResponse,
  type ToolResponse,
  type ToolGroupOpenAPIResponse,
  type UpdateServerRequest,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { StructuredAsset } from "./shared";

type ToolListResponsePayload = {
  tool_groups?: ToolGroupOpenAPIResponse[];
  total?: number;
  page?: number;
  page_size?: number;
  pageSize?: number;
};

type WrappedToolListResponse = {
  data?: ToolListResponsePayload;
  tool_groups?: ToolGroupOpenAPIResponse[];
  total?: number;
  page?: number;
  page_size?: number;
  pageSize?: number;
};

type WrappedMcpListResponse = ListServersResponse & {
  data?: ListServersResponse & {
    total?: number;
    page?: number;
    page_size?: number;
    pageSize?: number;
  };
  total?: number;
  page?: number;
  page_size?: number;
  pageSize?: number;
};

export type ToolListOptions = {
  keyword?: string;
};

export type ToolAssetListResult = {
  records: StructuredAsset[];
  total: number;
};

export type McpServerListResult = {
  records: McpServerAsset[];
  total: number;
};

const toolsApi = ToolsApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);
const mcpServersApi = McpServersApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);
const coreBasePath = `${BASE_URL}/api/core`;

export type McpToolAsset = {
  id: string;
  name: string;
  description: string;
};

export type McpServerAsset = {
  id: string;
  name: string;
  url: string;
  transport: string;
  timeout: number;
  enabled: boolean;
  isVerified: boolean;
  share: boolean;
  toolCount: number;
  tools: McpToolAsset[];
  allowedTools?: string[];
  apiKeyPreview: string;
  createTime: string;
  updateTime: string;
};

export type McpServerDraft = {
  name: string;
  url: string;
  transport: string;
  apiKey: string;
  timeout: number;
  enabled: boolean;
};

export type McpCheckResult = {
  success: boolean;
  message: string;
  toolCount: number;
};

const toStringValue = (value: unknown, fallback = ""): string => {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number") {
    return String(value);
  }
  return fallback;
};

const toNumberValue = (value: unknown, fallback = 0): number => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : fallback;
  }
  return fallback;
};

const toBooleanValue = (value: unknown, fallback = false): boolean => {
  if (typeof value === "boolean") {
    return value;
  }
  return fallback;
};

const normalizeMcpTransport = (value: string) =>
  value === "streamable_http" ? "http" : value;

const unwrapResponsePayload = <T>(payload: T | { data?: T }): T => {
  if (
    payload &&
    typeof payload === "object" &&
    "data" in payload &&
    (payload as { data?: T }).data
  ) {
    return (payload as { data: T }).data;
  }
  return payload as T;
};

const buildListParams = (options: ToolListOptions = {}) => {
  const params: Record<string, string> = {};
  const keyword = options.keyword?.trim();
  if (keyword) {
    params.keyword = keyword;
  }
  return params;
};

const readListTotal = (
  payload: Record<string, unknown> | null,
  raw: Record<string, unknown> | null,
  fallbackCount: number,
) =>
  toNumberValue(
    payload?.total ?? raw?.total ?? payload?.total_size ?? raw?.total_size,
    fallbackCount,
  );

const toRecord = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

const normalizeToolGroup = (item: ToolGroupOpenAPIResponse): StructuredAsset => {
  const name = toStringValue(item.name).trim();
  const label = toStringValue(item.label, name).trim();
  const description = toStringValue(item.description).trim();
  const methodSummaries = (item.methods || [])
    .map((method) => toStringValue(method.summary || method.name).trim())
    .filter(Boolean);

  return {
    id: name || label,
    name: label || name,
    description,
    category: "",
    tags: [],
    content: methodSummaries.join("；") || description,
    isEnabled: !item.disabled,
    readonly: item.can_disable === false,
  };
};

export async function listToolAssets(options: ToolListOptions = {}) {
  const result = await listToolAssetsPage(options);
  return result.records;
}

export async function listToolAssetsPage(
  options: ToolListOptions = {},
): Promise<ToolAssetListResult> {
  const response = await axiosInstance.get(`${coreBasePath}/tools`, {
    params: buildListParams(options),
  });
  const payload = unwrapResponsePayload(response.data as WrappedToolListResponse);
  const rawPayload = toRecord(payload);
  const rawResponse = toRecord(response.data);
  const toolGroups = payload.tool_groups || [];
  const records = toolGroups.map(normalizeToolGroup);
  return {
    records,
    total: readListTotal(rawPayload, rawResponse, records.length),
  };
}

export async function enableTool(name: string) {
  await toolsApi.apiCoreToolsToolNameEnablePost({ toolName: name });
}

export async function disableTool(name: string) {
  await toolsApi.apiCoreToolsToolNameDisablePost({ toolName: name });
}

const normalizeMcpTool = (item: ToolResponse): McpToolAsset => {
  const id = toStringValue(item.id || item.tool_name).trim();
  const name = toStringValue(item.tool_name || item.id).trim();
  return {
    id: id || name,
    name: name || id,
    description: toStringValue(item.description).trim(),
  };
};

const normalizeMcpServer = (item: ServerResponse): McpServerAsset => {
  const tools = (item.tools || []).map(normalizeMcpTool).filter((tool) => tool.id);
  const id = toStringValue(item.id || item.name).trim();
  const name = toStringValue(item.name || item.id).trim();

  return {
    id: id || name,
    name: name || id,
    url: toStringValue(item.url).trim(),
    transport: toStringValue(item.transport).trim(),
    timeout: toNumberValue(item.timeout, 30),
    enabled: toBooleanValue(item.enabled),
    isVerified: toBooleanValue(item.is_verified),
    share: toBooleanValue(item.share),
    toolCount: toNumberValue(item.tool_count, tools.length),
    tools,
    allowedTools: Array.isArray(item.allowed_tools) ? item.allowed_tools : undefined,
    apiKeyPreview: toStringValue(item.api_key_preview).trim(),
    createTime: toStringValue(item.create_time).trim(),
    updateTime: toStringValue(item.update_time).trim(),
  };
};

export async function listMcpServers(options: ToolListOptions = {}) {
  const result = await listMcpServersPage(options);
  return result.records;
}

export async function listMcpServersPage(
  options: ToolListOptions = {},
): Promise<McpServerListResult> {
  const response = await axiosInstance.get(`${coreBasePath}/mcp_servers`, {
    params: buildListParams(options),
  });
  const payload = unwrapResponsePayload(response.data as WrappedMcpListResponse);
  const rawPayload = toRecord(payload);
  const rawResponse = toRecord(response.data);
  const records = (payload.mcp_servers || []).map(normalizeMcpServer);
  return {
    records,
    total: readListTotal(rawPayload, rawResponse, records.length),
  };
}

export async function createMcpServer(draft: McpServerDraft) {
  const payload: CreateServerRequest = {
    api_key: draft.apiKey.trim(),
    enabled: draft.enabled,
    name: draft.name.trim(),
    timeout: draft.timeout,
    transport: normalizeMcpTransport(draft.transport),
    url: draft.url.trim(),
  };
  const response = await mcpServersApi.apiCoreMcpServersPost({
    createServerRequest: payload,
  });
  return normalizeMcpServer(unwrapResponsePayload(response.data as ServerResponse));
}

export async function updateMcpServer(id: string, draft: McpServerDraft) {
  const payload: UpdateServerRequest = {
    enabled: draft.enabled,
    name: draft.name.trim(),
    timeout: draft.timeout,
    url: draft.url.trim(),
  };
  const apiKey = draft.apiKey.trim();
  if (apiKey) {
    payload.api_key = apiKey;
  }

  const response = await mcpServersApi.apiCoreMcpServersIdPatch({
    id,
    updateServerRequest: payload,
  });
  return normalizeMcpServer(unwrapResponsePayload(response.data as ServerResponse));
}

export async function deleteMcpServer(id: string) {
  await mcpServersApi.apiCoreMcpServersIdDelete({ id });
}

export async function checkMcpServer(id: string): Promise<McpCheckResult> {
  const response = await mcpServersApi.apiCoreMcpServersIdCheckPost({ id });
  const payload = unwrapResponsePayload(response.data as CheckResponse);
  return {
    success: Boolean(payload.success),
    message: toStringValue(payload.message),
    toolCount: toNumberValue(payload.tool_count),
  };
}

export async function discoverMcpServerTools(id: string) {
  const response = await mcpServersApi.apiCoreMcpServersIdDiscoverPost({ id });
  const payload = unwrapResponsePayload(response.data as DiscoverResponse);
  return {
    success: Boolean(payload.success),
    tools: (payload.tools || []).map(normalizeMcpTool).filter((tool) => tool.id),
  };
}

export async function updateMcpServerTools(id: string, allowedTools: string[]) {
  const response = await mcpServersApi.apiCoreMcpServersIdToolsPut({
    id,
    updateToolsRequest: { allowed_tools: allowedTools },
  });
  return normalizeMcpServer(unwrapResponsePayload(response.data as ServerResponse));
}
