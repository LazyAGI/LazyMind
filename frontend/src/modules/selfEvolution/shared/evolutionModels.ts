export type EvolutionModelSummary = {
  model_ref: string;
  display_name: string;
  provider_name: string;
  source: "personal" | "shared";
  validation_status?: "unverified" | "passed" | "failed";
  validation_version?: string;
};

export type EvolutionModels = {
  models: EvolutionModelSummary[];
  configured_default?: EvolutionModelSummary;
  available_default_ref?: string;
  can_select: boolean;
  unavailable_reason?: string;
};

export type ThreadObservation = {
  status?: string;
  runtime_status?: string;
  cleanup_pending?: boolean;
  status_source?: "live" | "cached";
  observed_at?: string;
  thread_payload?: { model_at_creation?: EvolutionModelSummary };
};

export const isTerminalThread = (status?: string) =>
  ["ended", "completed", "succeeded", "failed", "canceled", "cancelled"].includes(status?.toLowerCase() || "");

export const hasLiveTerminalStatus = (thread?: ThreadObservation) =>
  thread?.status_source === "live" && !thread.cleanup_pending && thread.runtime_status !== "cancelling" && isTerminalThread(thread.status);
