import {
  Configuration as CoreConfiguration,
  ToolsApiFactory,
  type ToolGroupOpenAPIResponse,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { StructuredAsset } from "./shared";

type ToolListResponsePayload = {
  tool_groups?: ToolGroupOpenAPIResponse[];
};

type WrappedToolListResponse = {
  data?: ToolListResponsePayload;
  tool_groups?: ToolGroupOpenAPIResponse[];
};

const toolsApi = ToolsApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

const toStringValue = (value: unknown, fallback = ""): string => {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number") {
    return String(value);
  }
  return fallback;
};

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
    isEnabled: !item.disabled && item.active !== false,
    readonly: item.can_disable === false,
  };
};

export async function listToolAssets() {
  const response = await toolsApi.apiCoreToolsGet();
  const payload = response.data as WrappedToolListResponse;
  const toolGroups = payload.data?.tool_groups || payload.tool_groups || [];
  return toolGroups.map(normalizeToolGroup);
}

export async function enableTool(name: string) {
  await toolsApi.apiCoreToolsToolNameEnablePost({ toolName: name });
}

export async function disableTool(name: string) {
  await toolsApi.apiCoreToolsToolNameDisablePost({ toolName: name });
}
