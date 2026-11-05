import type { SlotRevision, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { SubAgentTask, TaskArtifactStream } from '@/modules/chat/store/taskCenter';

const PAPER_STREAM_SLOT_IDS = new Set(['write_outline_document', 'write_document_document', 'revise_document_document']);

const WRITER_STREAM_SLOT_IDS = new Set([
  'outline_document',
  'flat_draft_document',
  'draft_document',
  ...PAPER_STREAM_SLOT_IDS,
]);

/** A finished preview stays visible until its own published revision arrives. */
export function shouldShowArtifactStream(stream: TaskArtifactStream | undefined, revisions: SlotRevision[]): boolean {
  if (!stream) return false;
  if (!stream.attempt_id) return revisions.length === 0 || stream.state === 'streaming';
  return !revisions.some(revision => revision.selected && (
    revision.producer_attempt_id === stream.attempt_id
    || (revision.step_id === stream.step_id && revision.attempt === stream.attempt)
    || (stream.artifact?.value?.sha256 && revision.artifact_value?.sha256 === stream.artifact.value.sha256)
  ));
}

export function findWriterArtifactStream(
  session: WorkflowSession,
  stepId: string | undefined,
  slotId: string,
  tasks: SubAgentTask[],
): TaskArtifactStream | undefined {
  if (!stepId) return undefined;
  const paper = session.workflow_id === 'paper-writing';
  const findStream = (task: SubAgentTask | undefined, stepStatus?: string) => {
    const stream = task?.artifact_streams
    ?.slice()
    .reverse()
    .find((candidate) => candidate.slot === slotId && candidate.content_type === 'text/markdown');
    // Rendering must not keep replaying a backlog after computation has finished.
    // Keep publication/error state intact; only bypass the presentation queue.
    return stream && task && (task.progress_pct >= 100 || !['pending', 'running'].includes(task.status)
      || ['succeeded', 'failed', 'interrupted', 'canceled'].includes(stepStatus ?? ''))
      ? { ...stream, deltas: undefined, state: stream.state === 'streaming' ? 'ended' : stream.state } : stream;
  };

  const steps = (session.steps ?? [])
    .filter((step) => step.step_id === stepId && (!paper || step.validity !== 'stale'))
    .slice()
    .reverse();
  if (paper) steps.sort((a, b) => b.attempt - a.attempt || Date.parse(b.created_at) - Date.parse(a.created_at));
  for (const step of steps) {
    if (paper && ['failed', 'interrupted', 'canceled', 'cancelled'].includes(step.status)) return undefined;
    const stream = findStream(tasks.find((task) => task.task_id === step.task_id), step.status);
    if (stream) {
      if ((paper || PAPER_STREAM_SLOT_IDS.has(slotId)) && stream.state === 'aborted') return undefined;
      return paper ? { ...stream, attempt_id: step.id, step_id: stepId, attempt: step.attempt } : stream;
    }
    // A new paper attempt must never borrow the previous attempt's preview.
    if (paper) return undefined;
  }

  // The session projection can briefly lag task_created. Only use the fallback
  // when one live stream for this exact slot is unambiguous.
  // Other plugins use exact task ownership; never borrow an unrelated task's preview.
  if (paper || !WRITER_STREAM_SLOT_IDS.has(slotId)) return undefined;
  const liveStreams = tasks
    .filter((task) => task.status === 'pending' || task.status === 'running')
    .map(task => findStream(task))
    .filter((stream): stream is TaskArtifactStream => Boolean(
      stream && !(PAPER_STREAM_SLOT_IDS.has(slotId) && stream.state === 'aborted'),
    ));
  return liveStreams.length === 1 ? liveStreams[0] : undefined;
}
