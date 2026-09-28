import type { OrdinaryTaskView } from "@/modules/chat/types/ordinaryTask";

export function ordinary(id: string, overrides: Partial<OrdinaryTaskView> = {}): OrdinaryTaskView {
  const page = { total: 0, next_cursor: null, revision: 1 };
  return {
    schema_version: 1, display_key: `task:${id}:1`, task_id: id, session_id: null,
    workflow_step_id: null, attempt_id: null, run_id: "run-1", execution_id: "1",
    parallel_group_id: null, order: 1, title: id, status: "succeeded", revision: 1,
    process_state: "not_provided", process_steps: [], sources: [], stage_artifacts: [],
    pages: { process_steps: { ...page }, sources: { ...page }, stage_artifacts: { ...page } },
    timing: { started_at: null, finished_at: null, execution_elapsed_ms: null, thinking_elapsed_ms: null, measured_at: "2026-08-20T06:00:00Z" },
    ...overrides,
  };
}
