import { mergeRun } from "../../utils/ordinaryTaskState";
import { useEffect, useRef, useState } from 'react';
import type { OrdinaryRunView } from '../../types/ordinaryTask';

export function formatTaskDuration(seconds: number, zh: boolean): string {
  const value = Math.max(0, Math.floor(seconds));
  const units = [Math.floor(value / 86400), Math.floor(value / 3600) % 24, Math.floor(value / 60) % 60, value % 60];
  const found = units.findIndex(unit => unit > 0);
  const first = found < 0 ? 3 : found;
  return units.slice(first).map((unit, index) => `${unit}${(zh ? ['天', '小时', '分', '秒'] : ['d', 'h', 'm', 's'])[index + (first)]}`).join(zh ? '' : ' ');
}

const terminal = new Set(['completed', 'succeeded', 'failed', 'canceled', 'cancelled', 'interrupted', 'stopped']);

/** The server owns timing facts. The browser only extrapolates a received
 * snapshot on a monotonic clock, unaffected by token renders or wall-clock skew. */
export function useRunTiming(incoming: OrdinaryRunView | undefined) {
  const anchor = useRef<{ run: OrdinaryRunView; at: number }>();
  const [, tick] = useState(0);
  if (!incoming?.timing || !incoming.execution_id) anchor.current = undefined;
  else {
    const current = anchor.current;
    const next = current?.run.run_id === incoming.run_id ? mergeRun(current.run, incoming) : incoming;
    if (!current || next.execution_id !== current.run.execution_id || next.revision !== current.run.revision
      || next.timing?.measured_at !== current.run.timing?.measured_at) anchor.current = { run: next, at: performance.now() };
  }
  const run = anchor.current?.run;
  const active = Boolean(run?.timing && !run.timing.finished_at && !terminal.has(run.status ?? ''));
  const executionID = run?.execution_id;
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => tick(value => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, [active, executionID]);
  const timing = run?.timing;
  const delta = active && anchor.current ? Math.max(0, performance.now() - anchor.current.at) : 0;
  return {
    elapsedSeconds: timing?.elapsed_ms == null ? undefined : Math.floor((timing.elapsed_ms + delta) / 1000),
    cumulativeExecutionSeconds: timing?.cumulative_execution_ms == null ? undefined : Math.floor((timing.cumulative_execution_ms + delta * timing.running_attempt_count) / 1000),
  };
}
