import { axiosInstance, BASE_URL } from "@/components/request";

const coreBasePath = `${BASE_URL}/api/core`;

interface ApiEnvelope<T> {
  data?: T;
}

export interface SettingsControls {
  task_center_enabled: boolean;
  skills_enabled: boolean;
  workflows_enabled: boolean;
  mcp_enabled: boolean;
  document_parsing_enabled: boolean;
}

export interface SettingsOverviewCounts {
  total: number;
  enabled: number;
  verified: number;
  runnable: number;
  configured: number;
}

export interface SettingsOverviewSection {
  id: string;
  title: string;
  route: string;
  raw_enabled?: boolean;
  effective_enabled?: boolean;
  counts: SettingsOverviewCounts;
  status: "ready" | "paused";
  detail: string;
}

export interface SettingsOverviewIssue {
  id: string;
  severity: "info" | "warning";
  message: string;
  section: string;
}

export interface SettingsOverview {
  controls: SettingsControls;
  sections: SettingsOverviewSection[];
  issues: SettingsOverviewIssue[];
  updated_at: string;
}

export interface SettingsCheckResult {
  id: string;
  status: "passed" | "attention" | "not_checked";
  message: string;
  section: string;
}

export interface SettingsChecks {
  started_at: string;
  finished_at: string;
  results: SettingsCheckResult[];
}

function unwrap<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function fetchSettingsOverview(): Promise<SettingsOverview> {
  const response = await axiosInstance.get<ApiEnvelope<SettingsOverview>>(
    `${coreBasePath}/settings/overview`,
  );
  return unwrap<SettingsOverview>(response.data);
}

export async function runSettingsChecks(): Promise<SettingsChecks> {
  const response = await axiosInstance.post<ApiEnvelope<SettingsChecks>>(
    `${coreBasePath}/settings/checks`,
  );
  return unwrap<SettingsChecks>(response.data);
}
