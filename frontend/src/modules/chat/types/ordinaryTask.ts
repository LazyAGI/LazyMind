import type { MediaCapabilityDependencyDetail } from "../utils/mediaCapabilityDependency";

/** Public execution data. Raw agent reasoning and tool arguments are never part of this contract. */
export interface PublicProcessStep {
  step_id: string;
  revision: number;
  order: number;
  title: string;
  status: "pending" | "running" | "succeeded" | "failed" | "interrupted" | "canceled";
  summary?: string;
  started_at: string | null;
  finished_at: string | null;
  elapsed_ms: number | null;
}

export interface PublicSource {
  source_id: string;
  platform: string;
  domain: string | null;
  title: string;
  snippet?: string;
  url?: string;
  resource_id?: string;
  dataset_id?: string;
  kind: "web" | "repository" | "document" | "knowledge";
}

export interface PublicArtifact {
  artifact_id: string;
  revision: number;
  producer_display_key: string;
  name: string;
  content_type: string;
  size_bytes: number | null;
  state: "processing" | "ready" | "failed" | "unavailable";
  preview_kind: string | null;
  capabilities: { preview: boolean; open: boolean; download: boolean };
  preview_url?: string;
  open_url?: string;
  download_url?: string;
  inline_content?: string;
  created_at: string;
}

export type OrdinaryCollection = "process_steps" | "sources" | "stage_artifacts";

export interface CollectionPage {
  total: number;
  next_cursor: string | null;
  revision: number;
}

export interface OrdinaryTaskView {
  schema_version: 1;
  display_key: string;
  task_id: string | null;
  conversation_id?: string;
  trigger_history_id?: string;
  agent_type?: string;
  session_id: string | null;
  workflow_step_id: string | null;
  attempt_id: string | null;
  run_id: string;
  execution_id: string;
  parallel_group_id: string | null;
  order: number;
  title: string;
  status: string;
  revision: number;
  process_state: "available" | "not_provided" | "unavailable";
  process_steps: PublicProcessStep[];
  /** A public task outline; entries have no inferred execution status. */
  plan_steps?: string[];
  /** Overall task progress used to estimate plan position, not individual execution states. */
  progress_pct?: number;
  /** Validated setup actions, without the raw task failure or tool logs. */
  capability_dependency?: Pick<MediaCapabilityDependencyDetail, "status" | "required" | "missing" | "message">;
  sources: PublicSource[];
  stage_artifacts: PublicArtifact[];
  pages: Record<OrdinaryCollection, CollectionPage>;
  timing: {
    started_at: string | null;
    finished_at: string | null;
    execution_elapsed_ms: number | null;
    thinking_elapsed_ms: number | null;
    measured_at: string;
  };
}

export interface OrdinaryRunView {
  run_id: string;
  revision: number;
  final_output_refs: string[];
  final_artifacts?: PublicArtifact[];
}

export interface OrdinaryTaskSnapshotEvent {
  type: "task_snapshot";
  schema_version: 1;
  event_id: string;
  display_key: string;
  revision: number;
  emitted_at: string;
  data: OrdinaryTaskView;
}
