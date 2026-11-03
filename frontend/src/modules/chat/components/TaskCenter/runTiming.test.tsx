import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { OrdinaryRunView } from '../../types/ordinaryTask';
import { formatTaskDuration, useRunTiming } from './runTiming';
const snapshot = (extra: Partial<OrdinaryRunView> = {}): OrdinaryRunView => ({
 run_id: 'run', execution_id: 'execution', status: 'running', revision: 1, final_output_refs: [],
 timing: { requested_at: '2026-10-16T00:00:00Z', finished_at: null, measured_at: '2026-10-16T00:00:10Z', elapsed_ms: 10000, cumulative_execution_ms: 5000, running_attempt_count: 2 }, ...extra,
});
afterEach(() => vi.useRealTimers());
it('ticks despite frequent same-snapshot renders and client clock changes', () => {
 vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date", "performance"] });
 const { result, rerender } = renderHook(({ run }) => useRunTiming(run), { initialProps: { run: snapshot() } });
 for (let i = 0; i < 5; i++) { act(() => vi.advanceTimersByTime(200)); rerender({ run: snapshot() }); }
 expect(result.current.elapsedSeconds).toBe(11); expect(result.current.cumulativeExecutionSeconds).toBe(7);
 vi.setSystemTime(new Date('2030-01-01')); act(() => vi.advanceTimersByTime(1000)); expect(result.current.elapsedSeconds).toBe(12);
});
it('keeps total running during confirmation, freezes on terminal and rejects stale snapshots', () => {
 vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date", "performance"] }); const waiting = snapshot({ status: 'waiting', timing: { ...snapshot().timing!, running_attempt_count: 0 } });
 const { result, rerender } = renderHook(({ run }) => useRunTiming(run), { initialProps: { run: waiting } });
 act(() => vi.advanceTimersByTime(3000)); expect(result.current.elapsedSeconds).toBe(13); expect(result.current.cumulativeExecutionSeconds).toBe(5);
 const done = snapshot({ status: 'completed', revision: 2, timing: { ...snapshot().timing!, finished_at: '2026-10-16T00:00:12Z', elapsed_ms: 12000, cumulative_execution_ms: 6000, running_attempt_count: 0 } });
 rerender({ run: done }); act(() => vi.advanceTimersByTime(5000)); expect(result.current.elapsedSeconds).toBe(12);
 rerender({ run: snapshot() }); expect(result.current.elapsedSeconds).toBe(12);
});
it('resets for a new round and leaves legacy timing unknown', () => {
 vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date", "performance"] }); const { result, rerender } = renderHook(({ run }) => useRunTiming(run), { initialProps: { run: snapshot() } });
 rerender({ run: snapshot({ execution_id: 'new', timing: { ...snapshot().timing!, elapsed_ms: 0, cumulative_execution_ms: 0 } }) }); expect(result.current.elapsedSeconds).toBe(0);
 rerender({ run: { run_id: 'legacy', revision: 0, final_output_refs: [] } }); expect(result.current.elapsedSeconds).toBeUndefined();
});
it.each(['stopped', 'waiting_approval'])('freezes a workflow %s interval and resumes its accumulated duration without counting the wait', (status) => {
 vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date", "performance"] });
 const paused = snapshot({ status, revision: 2, timing: { ...snapshot().timing!, finished_at: '2026-10-16T00:00:10Z', running_attempt_count: 0 } });
 const { result, rerender } = renderHook(({ run }) => useRunTiming(run), { initialProps: { run: paused } });
 act(() => vi.advanceTimersByTime(60000));
 expect(result.current.elapsedSeconds).toBe(10); expect(result.current.cumulativeExecutionSeconds).toBe(5);
 const resumed = snapshot({ execution_id: 'resumed', timing: { ...snapshot().timing!, requested_at: '2026-10-16T00:01:10Z', measured_at: '2026-10-16T00:01:10Z', running_attempt_count: 1 } });
 rerender({ run: resumed });
 act(() => vi.advanceTimersByTime(3000));
 expect(result.current.elapsedSeconds).toBe(13); expect(result.current.cumulativeExecutionSeconds).toBe(8);
 rerender({ run: paused });
 expect(result.current.elapsedSeconds).toBe(13);
});
it('formats seconds through days without losing seconds', () => {
 expect(formatTaskDuration(59, true)).toBe('59秒'); expect(formatTaskDuration(60, true)).toBe('1分0秒'); expect(formatTaskDuration(3605, true)).toBe('1小时0分5秒');
 expect(formatTaskDuration(93784, true)).toBe('1天2小时3分4秒'); expect(formatTaskDuration(90061, false)).toBe('1d 1h 1m 1s');
});
it('switches conversations even when the other conversation started earlier', () => {
 const { result, rerender } = renderHook(({ run }) => useRunTiming(run), { initialProps: { run: snapshot() } });
 rerender({ run: snapshot({ run_id: 'older-conversation-run', execution_id: 'older-execution', status: 'completed', timing: { ...snapshot().timing!, requested_at: '2020-01-01T00:00:00Z', finished_at: '2020-01-01T00:00:02Z', elapsed_ms: 2000, running_attempt_count: 0 } }) });
 expect(result.current.elapsedSeconds).toBe(2);
});
