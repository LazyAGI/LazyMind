import {
  ScanApi,
  type SourceBindingResponse,
  type SourceDocumentItem,
  type SourceListItem,
  type SourceResponse,
  type SourceSummaryResponse,
  type TreeNode,
} from "@/api/generated/scan-client";
import { AgentAppsAuth } from "@/components/auth";
import { DEFAULT_SCAN_TENANT_ID } from "../constants/options";

export type ScanV2Client = ScanApi;
export type ScanV2Source = (SourceListItem | SourceResponse) & Record<string, any>;
export type ScanV2Binding = SourceBindingResponse & Record<string, any>;
export type ScanV2Document = SourceDocumentItem & Record<string, any>;
export type ScanV2Summary = SourceSummaryResponse & Record<string, any>;
export type ScanV2TreeNode = TreeNode & Record<string, any>;

export interface ScanV2AgentHint {
  agent_id?: string;
  tenant_id?: string;
  status?: string;
}

// Resolve the tenant id used inside scan request bodies (tenant_id / tenant_key)
// and OAuth payloads. Header injection is handled globally by the request
// interceptor, so this only feeds business payloads.
export function getScanTenantId() {
  const userInfo = AgentAppsAuth.getUserInfo() as
    | (ReturnType<typeof AgentAppsAuth.getUserInfo> & {
        tenantId?: string;
        tenant_id?: string;
        tenantKey?: string;
        tenant_key?: string;
      })
    | null;

  return (
    userInfo?.tenantId ||
    userInfo?.tenant_id ||
    userInfo?.tenantKey ||
    userInfo?.tenant_key ||
    DEFAULT_SCAN_TENANT_ID
  );
}

export function createScanRequestId(prefix: string) {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}`;
}

export function getScanSourceId(source?: ScanV2Source | null) {
  return `${source?.source_id || source?.id || ""}`.trim();
}

export function getScanSourceName(source?: ScanV2Source | null) {
  return `${source?.name || ""}`.trim();
}

export function getScanSourceDatasetId(source?: ScanV2Source | null) {
  return `${source?.dataset_id || source?.datasetId || ""}`.trim();
}

export function getScanSourceUpdatedAt(source?: ScanV2Source | null) {
  return `${source?.updated_at || source?.updatedAt || source?.created_at || ""}`.trim();
}

export function getScanSourceConfigVersion(source?: ScanV2Source | null) {
  const version = Number(source?.config_version ?? source?.configVersion ?? 0);
  return Number.isFinite(version) ? version : 0;
}

export function getFirstScanBinding(bindings?: ScanV2Binding[] | null) {
  return Array.isArray(bindings) && bindings.length > 0 ? bindings[0] : null;
}

export function getScanBindingId(binding?: ScanV2Binding | null) {
  return `${binding?.binding_id || binding?.bindingId || ""}`.trim();
}

export function getScanBindingTarget(binding?: ScanV2Binding | null) {
  return `${binding?.target_ref || binding?.targetRef || ""}`.trim();
}

export function getScanBindingConnector(binding?: ScanV2Binding | null) {
  return `${binding?.connector_type || binding?.connectorType || ""}`.trim();
}

export function getScanBindingAgentId(binding?: ScanV2Binding | null) {
  return `${binding?.agent_id || binding?.agentId || ""}`.trim();
}

export function getScanBindingTreeKey(binding?: ScanV2Binding | null) {
  return `${binding?.tree_key || binding?.treeKey || ""}`.trim();
}

export function inferSourceKind(source?: ScanV2Source | null, binding?: ScanV2Binding | null) {
  const connector = getScanBindingConnector(binding).toLowerCase();
  const targetType = `${binding?.target_type || ""}`.toLowerCase();
  const sourceOptions = source?.source_options || {};
  const sourceType = `${sourceOptions.source_type || sourceOptions.type || ""}`.toLowerCase();

  if (
    connector.includes("feishu") ||
    targetType.includes("wiki") ||
    targetType.includes("drive") ||
    sourceType.includes("feishu")
  ) {
    return "feishu" as const;
  }

  if (
    connector.includes("notion") ||
    sourceType.includes("notion") ||
    targetType === "page" ||
    targetType === "database" ||
    targetType === "notion_page" ||
    targetType === "notion_database"
  ) {
    return "notion" as const;
  }

  return "local" as const;
}

export function getBindingSchedule(binding?: ScanV2Binding | null) {
  const legacyExpr = `${binding?.schedule_expr || binding?.scheduleExpr || ""}`.trim();
  if (legacyExpr) {
    return legacyExpr;
  }

  const policy = binding?.schedule_policy || binding?.schedulePolicy;
  const firstRule = Array.isArray(policy?.rules) ? policy.rules[0] : null;
  const time = `${firstRule?.time || ""}`.trim();
  const days = Array.isArray(firstRule?.days) ? firstRule.days : [];
  if (!time || days.length === 0) {
    return "";
  }

  const dayMap: Record<string, string[]> = {
    everyday: ["1", "2", "3", "4", "5", "6", "7"],
    workday: ["1", "2", "3", "4", "5"],
    non_workday: ["6", "7"],
    mon: ["1"],
    tue: ["2"],
    wed: ["3"],
    thu: ["4"],
    fri: ["5"],
    sat: ["6"],
    sun: ["7"],
  };
  const normalizedDays = Array.from(
    new Set(days.flatMap((day: string) => dayMap[`${day}`] || [])),
  ).sort((left, right) => Number(left) - Number(right));
  return normalizedDays.length > 0 ? `weekly:${normalizedDays.join(",")}@${time}` : "";
}

export function getBindingLastError(binding?: ScanV2Binding | null) {
  const error = binding?.last_error || binding?.lastError;
  if (!error) return "";
  if (typeof error === "string") return error;
  return error.message || error.error || JSON.stringify(error);
}

export function getDocumentDisplayName(item: ScanV2Document) {
  return `${item.display_name || item.name || item.object_key || item.document_id || "-"}`;
}

export function getDocumentPath(item: ScanV2Document) {
  return `${item.path || item.object_key || item.display_name || item.document_id || "-"}`;
}

export function getDocumentLastUpdatedAt(item: ScanV2Document) {
  return `${item.modified_at || item.last_synced_at || item.updated_at || item.created_at || ""}`;
}

export function getScanTreeNodePath(node?: ScanV2TreeNode | null) {
  return `${node?.target_ref || node?.node_ref || node?.object_key || node?.key || ""}`.trim();
}
