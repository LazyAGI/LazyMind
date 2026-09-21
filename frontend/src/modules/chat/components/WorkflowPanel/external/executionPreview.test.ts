import { expect, it } from 'vitest';
import type { TabDef, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { executionPreview } from './executionPreview';
import { reduceActivity } from './useExecutionActivity';

const tab = { id: 'slides', slots: [
  { id: 'html', cardinality: 'list' }, { id: 'notes', cardinality: 'list' }, { id: 'title' },
] } as TabDef;
const run = { session_id: 'run', status: 'active', slots: [], steps: [
  { step_id: 'slides', task_id: 'task', attempt: 1, status: 'running' },
] } as unknown as WorkflowSession;
const artifact = (slot: string, seq: number, text: string, index?: number) => ({
  type: 'artifact', slot, seq, content_type: 'text', value: { text, ...(index === undefined ? {} : { list_index: index }) },
});

it('shows pages and notes incrementally, aligns durable indices, and never mutates formal slots', () => {
  let activity = reduceActivity({}, artifact('html', 1, 'page 1', 7));
  const first = executionPreview(run, tab, { task: activity });
  expect(first.slots).toHaveLength(1);
  activity = reduceActivity(activity, artifact('notes', 1, 'notes 1', 9));
  activity = reduceActivity(activity, artifact('html', 2, 'page 2', 8));
  const next = executionPreview(run, tab, { task: activity });
  expect(next.slots?.map(slot => [slot.slot, slot.list_index, slot.sort_order])).toEqual([
    ['html', 7, 1], ['notes', 9, 1], ['html', 8, 2],
  ]);
  expect(run.slots).toEqual([]);
  expect(first.slots).toHaveLength(1);
});

it('deduplicates replay and replaces an updated page without changing its position', () => {
  let activity = reduceActivity({}, artifact('html', 1, 'old', 7));
  activity = reduceActivity(activity, artifact('html', 1, 'old', 7));
  expect(activity.artifacts).toHaveLength(1);
  activity = reduceActivity(activity, artifact('html', 2, 'new', 7));
  const preview = executionPreview(run, tab, { task: activity });
  expect(preview.slots).toHaveLength(1);
  expect(preview.slots?.[0]).toMatchObject({ artifact_value: { text: 'new' }, sort_order: 1 });
});

it('keeps preview until formal completion, discards failure and retry, and ignores other tabs', () => {
  const activity = reduceActivity({}, artifact('html', 1, 'preview'));
  expect(executionPreview(run, tab, { task: reduceActivity(activity, { type: 'done' }) }).slots).toHaveLength(1);
  expect(executionPreview(run, tab, { task: reduceActivity(activity, { type: 'error' }) })).toBe(run);
  const complete = { ...run, steps: [{ ...run.steps![0], status: 'succeeded' }] };
  expect(executionPreview(complete, tab, { task: activity })).toBe(complete);
  const retry = { ...run, steps: [{ ...run.steps![0], task_id: 'retry', attempt: 2 }] };
  expect(executionPreview(retry, tab, { task: activity })).toBe(retry);
  expect(executionPreview(run, { ...tab, id: 'other' }, { task: activity })).toBe(run);
});

it('uses latest single output and appends unindexed list artifacts after existing items', () => {
  let activity = reduceActivity({}, artifact('title', 1, 'old'));
  activity = reduceActivity(activity, artifact('title', 2, 'new'));
  activity = reduceActivity(activity, artifact('html', 1, 'page'));
  const preview = executionPreview(run, tab, { task: activity });
  expect(preview.slots).toHaveLength(2);
  expect(preview.slots?.find(slot => slot.slot === 'title')?.artifact_value.text).toBe('new');
  expect(preview.slots?.find(slot => slot.slot === 'html')).toMatchObject({ list_index: 0, sort_order: 1 });
});
