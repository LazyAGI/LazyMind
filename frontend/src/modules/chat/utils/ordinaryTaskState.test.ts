import { describe, expect, it } from "vitest";
import type { OrdinaryTaskView, PublicProcessStep, PublicArtifact } from "../types/ordinaryTask";
import { mergeOrdinarySnapshot, readOrdinaryTask } from "./ordinaryTaskState";

function step(id: string): PublicProcessStep {
  return { step_id: id, title: id, order: Number(id), revision: 1, status: "succeeded", started_at: null, finished_at: null, elapsed_ms: null };
}

function snapshot(revision = 1): OrdinaryTaskView {
  return {
    schema_version: 1, display_key: "task:one:run", task_id: "one", session_id: null,
    workflow_step_id: null, attempt_id: null, run_id: "turn", execution_id: "run",
    parallel_group_id: null, order: 1, title: "Research", status: "running", revision,
    process_state: "available", process_steps: [step("1")], sources: [], stage_artifacts: [],
    pages: {
      process_steps: { total: 2, next_cursor: "page2", revision: 1 },
      sources: { total: 0, next_cursor: null, revision: 1 },
      stage_artifacts: { total: 0, next_cursor: null, revision: 1 },
    },
    timing: { started_at: null, finished_at: null, execution_elapsed_ms: null, thinking_elapsed_ms: null, measured_at: "2026-09-23T00:00:00Z" },
  };
}

describe("ordinary task snapshots", () => {
  it("does not accept a legacy raw task response as a public snapshot", () => {
    expect(readOrdinaryTask({ task_id: "one", objective: "internal", steps: [] })).toBeUndefined();
    expect(readOrdinaryTask(snapshot())?.display_key).toBe("task:one:run");
  });

  it("rejects an older REST snapshot after a newer terminal SSE snapshot", () => {
    const current = { ...snapshot(4), status: "succeeded" };
    expect(mergeOrdinarySnapshot(current, snapshot(3))).toBe(current);
  });

  it("accepts optional public plans and rejects malformed plan fields", () => {
    const planned = { ...snapshot(), plan_steps: ["Read", "Check", "Write"] };
    expect(readOrdinaryTask(planned)?.plan_steps).toEqual(planned.plan_steps);
    for (const plan_steps of [null, "text", ["one"], ["Read", {}, "Write"], ["Read", " ", "Write"], ["Read", "x".repeat(101), "Write"]]) {
      expect(readOrdinaryTask({ ...snapshot(), plan_steps })).toBeUndefined();
    }
  });

  it("keeps different attempts isolated even when paginating", () => {
    const next = { ...snapshot(5), display_key: "task:one:retry", execution_id: "retry", process_steps: [step("2")] };
    expect(mergeOrdinarySnapshot(snapshot(4), next, "process_steps").process_steps).toEqual([step("2")]);
  });

  it("accepts optional overall progress and rejects invalid percentages", () => {
    expect(readOrdinaryTask(snapshot())).toBeDefined();
    for (const progress_pct of [0, 25, 100]) {
      expect(readOrdinaryTask({ ...snapshot(), progress_pct })?.progress_pct).toBe(progress_pct);
    }
    for (const progress_pct of [null, "50", -1, 101, NaN, Infinity, 0.5]) {
      expect(readOrdinaryTask({ ...snapshot(), progress_pct })).toBeUndefined();
    }
  });

  it("appends a page once and retains it when the first page is refreshed", () => {
    const second = snapshot();
    second.process_steps = [step("2")];
    second.pages.process_steps.next_cursor = null;
    const loaded = mergeOrdinarySnapshot(snapshot(), second, "process_steps");
    expect(loaded.process_steps.map(s => s.step_id)).toEqual(["1", "2"]);
    const duplicated = mergeOrdinarySnapshot(loaded, second, "process_steps");
    expect(duplicated.process_steps).toHaveLength(2);
    const refreshed = mergeOrdinarySnapshot(duplicated, snapshot(2));
    expect(refreshed.process_steps).toHaveLength(2);
    expect(refreshed.pages.process_steps.next_cursor).toBeNull();
  });

  it("refreshes expired artifact URLs at the same revision while retaining loaded pages", () => {
    const artifact = (id: string, url: string): PublicArtifact => ({ artifact_id: id, revision: 1,
      producer_display_key: "task:one:run", name: `${id}.pdf`, content_type: "application/pdf",
      size_bytes: 100, state: "ready", preview_kind: "pdf", capabilities: { preview: true, open: false, download: true },
      preview_url: url, download_url: url, created_at: "2026-09-23T00:00:00Z" });
    const current = snapshot();
    current.stage_artifacts = [artifact("one", "/api/core/file?signature=expired"), artifact("two", "/api/core/file?id=two")];
    current.pages.stage_artifacts = { total: 2, revision: 1, next_cursor: null };
    const incoming = snapshot();
    incoming.stage_artifacts = [artifact("one", "/api/core/file?signature=renewed")];
    incoming.pages.stage_artifacts = { total: 2, revision: 1, next_cursor: "next" };
    const merged = mergeOrdinarySnapshot(current, incoming);
    expect(merged.stage_artifacts).toHaveLength(2);
    expect(merged.stage_artifacts[0].download_url).toContain("renewed");
    expect(merged.pages.stage_artifacts.next_cursor).toBeNull();
  });

  it("replaces a changed collection instead of retaining revoked or deleted records", () => {
    const current = snapshot(2);
    const next = snapshot(3);
    next.process_steps = [];
    next.pages.process_steps = { total: 0, next_cursor: null, revision: 3 };
    expect(mergeOrdinarySnapshot(current, next).process_steps).toEqual([]);
  });
});
