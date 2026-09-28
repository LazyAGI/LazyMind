import type { OrdinaryCollection, OrdinaryTaskView } from "../types/ordinaryTask";

const collections: OrdinaryCollection[] = ["process_steps", "sources", "stage_artifacts"];

export function readOrdinaryTask(value: unknown): OrdinaryTaskView | undefined {
  if (!value || typeof value !== "object") return undefined;
  const task = value as OrdinaryTaskView;
  if (task.schema_version !== 1 || typeof task.display_key !== "string" || !task.display_key
    || !Number.isSafeInteger(task.revision) || task.revision < 0
    || !task.pages || !task.timing
    || collections.some(key => !Array.isArray(task[key]) || !task.pages[key])) return undefined;
  if (task.plan_steps !== undefined && (!Array.isArray(task.plan_steps)
    || task.plan_steps.length < 3 || task.plan_steps.length > 5
    || task.plan_steps.some(step => typeof step !== "string" || !step.trim() || [...step.trim()].length > 100))) return undefined;
  if (task.progress_pct !== undefined && (!Number.isInteger(task.progress_pct)
    || task.progress_pct < 0 || task.progress_pct > 100)) return undefined;
  return task;
}

function objectId(value: object): string {
  const item = value as Record<string, unknown>;
  return String(item.step_id ?? item.source_id ?? item.artifact_id ?? "");
}

/** Every ordinary stream event is a durable snapshot, never an append-only raw log delta. */
export function mergeOrdinarySnapshot(
  current: OrdinaryTaskView | undefined,
  incoming: OrdinaryTaskView,
  appendedCollection?: OrdinaryCollection,
): OrdinaryTaskView {
  if (!current) return incoming;
  // The server keeps this watermark monotonic across resumes of a task.
  if (current.task_id === incoming.task_id && incoming.revision < current.revision) return current;
  if (current.display_key !== incoming.display_key) return incoming;
  if (incoming.revision < current.revision) return current;
  const result = { ...incoming, pages: { ...incoming.pages } };
  for (const key of collections) {
    if (current.pages[key].revision !== incoming.pages[key].revision) continue;
    if (key !== appendedCollection && current[key].length <= incoming[key].length) continue;
    const objects = new Map<string, object>();
    for (const item of current[key]) objects.set(objectId(item), item);
    for (const item of incoming[key]) objects.set(objectId(item), item);
    // Collection versions are unchanged, so previously loaded pages are still valid.
    Object.assign(result, { [key]: [...objects.values()] });
    result.pages[key] = key === appendedCollection ? incoming.pages[key] : current.pages[key];
  }
  return result;
}
