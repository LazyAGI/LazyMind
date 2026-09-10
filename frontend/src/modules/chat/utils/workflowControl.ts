import type { Snapshot, WorkflowControlCommand, WorkflowHostAction, WorkflowReviewCheckpoint } from '@/api/generated/core-client/api';

export type WorkflowActionKind = 'save' | 'confirm' | 'confirm_and_continue' | 'continue' | 'retry' | 'rewind' | 'stop' | 'resume';

export type WorkflowReview = Pick<WorkflowReviewCheckpoint, 'id' | 'step_id' | 'execution_id' | 'status' | 'version' | 'manifest_hash'>;

export interface WorkflowControlView extends Pick<Snapshot, 'session_id' | 'state_version' | 'continuation' | 'admission' | 'active_executions' | 'binding'> {
  protocol: 'workflow.control.v1';
  reviews: WorkflowReview[];
  active_execution_ids: string[];
  delivery: Pick<WorkflowHostAction, 'id' | 'kind' | 'status' | 'execution_id' | 'consumed_at' | 'last_error'> | null;
  available_actions: string[];
}

export interface WorkflowActionIntent {
  kind: WorkflowActionKind;
  stepId?: string;
  review?: Pick<WorkflowReview, 'id' | 'version' | 'manifest_hash'>;
  preferenceScope?: 'step' | 'following';
}

export interface WorkflowControlRequest extends Pick<WorkflowControlCommand, 'command_id' | 'expected_state_version' | 'step_id' | 'review_id' | 'review_version' | 'manifest_hash'> {
  kind: Exclude<WorkflowActionKind, 'save'>;
  preference_scope?: 'step' | 'following';
}

export class ReviewRefreshRequired extends Error {}

export function deliveryPending(control: WorkflowControlView): boolean {
  const delivery = control.delivery;
  if (!delivery || delivery.consumed_at) return false;
  if (delivery.status === 'accepted' && delivery.execution_id && !control.active_execution_ids.includes(delivery.execution_id)) return false;
  return ['pending', 'dispatching', 'unknown'].includes(delivery.status) || delivery.status === 'accepted' && delivery.kind === 'continue';
}

/** One user operation retains its exact command after uncertain delivery. */
export function controlActions(
  read: () => Promise<WorkflowControlView>,
  write: (command: WorkflowControlRequest) => Promise<unknown>,
  id: () => string = () => crypto.randomUUID(),
) {
  let pending: { key: string; command: WorkflowControlRequest; committed?: boolean } | undefined;
  let inFlight: Promise<void> | undefined;
  const perform = async (intent: WorkflowActionIntent) => {
    if (intent.kind === 'save') { await read(); return; }
    const key = JSON.stringify(intent);
    if (!pending || pending.key !== key) {
      const current = await read();
      if (intent.kind === 'confirm' || intent.kind === 'confirm_and_continue') {
        const review = current.reviews.find(item => item.id === intent.review?.id);
        // A fresh read may include another editor's change. Never silently approve it.
        if (!review || review.status !== 'pending' || review.version !== intent.review?.version || review.manifest_hash !== intent.review.manifest_hash) {
          throw new ReviewRefreshRequired('Review changed after saving');
        }
      }
      pending = { key, command: {
        command_id: id(), kind: intent.kind, expected_state_version: current.state_version,
        ...(intent.stepId ? { step_id: intent.stepId } : {}),
        ...(intent.review ? { review_id: intent.review.id, review_version: intent.review.version, manifest_hash: intent.review.manifest_hash } : {}),
        ...(intent.preferenceScope ? { preference_scope: intent.preferenceScope } : {}),
      } };
    }
    try {
      if (!pending.committed) { await write(pending.command); pending.committed = true; }
      await read();
      pending = undefined;
    } catch (error) {
      const status = (error as { response?: { status?: number } })?.response?.status;
      // A definite rejection did not commit. Network/5xx uncertainty retains the command.
      if (status && status >= 400 && status < 500 && !pending?.committed) pending = undefined;
      throw error;
    }
  };
  return {
    execute(intent: WorkflowActionIntent): Promise<void> {
      if (inFlight) return inFlight;
      inFlight = perform(intent).finally(() => { inFlight = undefined; });
      return inFlight;
    },
  };
}
