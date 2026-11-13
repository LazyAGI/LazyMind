import type { Snapshot, WorkflowControlCommand, WorkflowHostAction, WorkflowReviewCheckpoint } from '@/api/generated/core-client/api';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { AskQuestion } from '@/modules/chat/components/AskCard';

export type WorkflowActionKind = 'save' | 'confirm' | 'confirm_and_continue' | 'continue' | 'retry' | 'rewind' | 'stop' | 'resume' | 'answer_input';

export interface WorkflowInputRequest {
  artifact_id: string;
  revision: number;
  slot: string;
  title?: string;
  questions: Array<AskQuestion & { options?: string[] }>;
}

export type WorkflowReview = Pick<WorkflowReviewCheckpoint, 'id' | 'step_id' | 'execution_id' | 'status' | 'version' | 'manifest_hash'>;

export interface WorkflowControlView extends Pick<Snapshot, 'session_id' | 'state_version' | 'continuation' | 'admission' | 'active_executions' | 'binding'> {
  protocol: 'workflow.control.v1';
  reviews: WorkflowReview[];
  active_execution_ids: string[];
  delivery: Pick<WorkflowHostAction, 'id' | 'kind' | 'status' | 'execution_id' | 'consumed_at' | 'last_error'> | null;
  available_actions: string[];
  input_request?: WorkflowInputRequest | null;
}

export interface WorkflowActionIntent {
  kind: WorkflowActionKind;
  stepId?: string;
  completedStepId?: string;
  review?: Pick<WorkflowReview, 'id' | 'version' | 'manifest_hash'>;
  preferenceScope?: 'step' | 'following';
  input?: Pick<WorkflowInputRequest, 'artifact_id' | 'revision'>;
  answerText?: string;
}

/** User intent from the shared panel, independent of the host protocol. */
export interface WorkflowPanelActionIntent extends Omit<WorkflowActionIntent, 'kind' | 'review'> {
  kind: 'approve' | 'continue' | 'retry' | 'rewind' | 'stop' | 'resume';
}

/** A completed host notification may hand control back before the next step begins. */
export function externalContinuationStep(session: WorkflowSession, control?: WorkflowControlView): string | undefined {
  const delivery = control?.delivery;
  if (!control || control.session_id !== session.session_id || !control.binding?.bound
    || control.continuation !== 'continue' || !control.admission.can_begin || control.active_executions !== 0
    || !control.available_actions.includes('continue') || control.reviews.some(review => review.status === 'pending')
    || !['active', 'waiting'].includes(session.status) || !session.projection?.ready?.length
    || delivery?.kind !== 'continue' || delivery.status !== 'accepted' || delivery.consumed_at || !delivery.execution_id) return undefined;
  const completed = session.steps?.find(step => step.step_id === session.current_step_id
    && step.status === 'succeeded' && step.validity !== 'stale');
  const node = completed && session.projection.nodes?.[completed.step_id];
  return node?.execution === 'succeeded' && node.validity !== 'stale' && !node.requires_approval
    ? completed?.step_id : undefined;
}

/** Translate the shared action using the snapshot the user saw when clicking. */
export function externalPanelAction(
  intent: WorkflowPanelActionIntent,
  session: WorkflowSession,
  control: WorkflowControlView,
): WorkflowActionIntent {
  if (intent.kind === 'approve') {
    const review = control.reviews.find(item => item.status === 'pending' && item.step_id === intent.stepId);
    if (!review) throw new ReviewRefreshRequired('Review is no longer available');
    return { ...intent, kind: 'confirm_and_continue', review };
  }
  if (intent.kind === 'retry') {
    // Browsing another tab never changes which failed execution is retried.
    const failedStep = session.projection?.current?.find(id => {
      const node = session.projection?.nodes?.[id];
      return node?.validity !== 'stale' && node?.execution === 'failed';
    }) ?? session.steps?.find(step => step.validity !== 'stale' && step.status === 'failed')?.step_id;
    return { ...intent, kind: 'retry', stepId: failedStep ?? session.current_step_id };
  }
  return { ...intent, kind: intent.kind };
}

export interface WorkflowControlRequest extends Pick<WorkflowControlCommand, 'command_id' | 'expected_state_version' | 'step_id' | 'review_id' | 'review_version' | 'manifest_hash'> {
  kind: Exclude<WorkflowActionKind, 'save'>;
  preference_scope?: 'step' | 'following';
  input_artifact_id?: string;
  input_revision?: number;
  answer_text?: string;
}

export class ReviewRefreshRequired extends Error {}
export class InputRefreshRequired extends Error {}

export const REVIEW_CHANGED_NOTICE = 'chat.workflowControlReviewChanged';
export const INPUT_CHANGED_NOTICE = 'chat.workflowControlInputChanged';
export const INPUT_SAVED_EXTERNAL_NOTICE = 'chat.workflowInputSavedExternal';
export const INPUT_SAVED_PAUSED_NOTICE = 'chat.workflowInputSavedPaused';
export const INPUT_SAVED_NOTICE = 'chat.workflowInputSaved';

export type OverlayInfoBanner = { key: string; tone: 'info' | 'warning' };

/** Only delivery problems need a banner; routine delivery stays silent. */
export function deliveryBanner(delivery: WorkflowControlView['delivery'] | null | undefined, binding?: WorkflowControlView['binding']): OverlayInfoBanner | undefined {
  if (!delivery || delivery.consumed_at) return undefined;
  if (delivery.status === 'unknown') return { key: 'chat.workflowControlDeliveryUnknown', tone: 'warning' };
  if (delivery.status === 'failed' && delivery.kind === 'cancel' && binding?.provider === 'codex') {
    return { key: 'chat.workflowControlCodexStopped', tone: 'info' };
  }
  if (delivery.status === 'failed') return { key: 'chat.workflowControlDeliveryFailed', tone: 'warning' };
  return undefined;
}

/** At most one info/warning strip so the panel workspace is not squeezed by stacked Alerts. */
export function overlayInfoBanner(noticeKey: string, delivery: WorkflowControlView['delivery'] | null | undefined, binding?: WorkflowControlView['binding']): OverlayInfoBanner | undefined {
  if ([REVIEW_CHANGED_NOTICE, INPUT_CHANGED_NOTICE, INPUT_SAVED_EXTERNAL_NOTICE, INPUT_SAVED_PAUSED_NOTICE, INPUT_SAVED_NOTICE].includes(noticeKey)) return { key: noticeKey, tone: 'info' };
  return deliveryBanner(delivery, binding);
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
      if (intent.kind === 'answer_input') {
        if (!intent.input || current.input_request?.artifact_id !== intent.input.artifact_id
          || current.input_request.revision !== intent.input.revision) {
          throw new InputRefreshRequired('Input request changed before submitting');
        }
      }
      if (intent.kind === 'confirm' || intent.kind === 'confirm_and_continue') {
        const review = current.reviews.find(item => item.id === intent.review?.id);
        // A fresh read may include another editor's change. Never silently approve it.
        if (!review || review.status !== 'pending' || review.version !== intent.review?.version || review.manifest_hash !== intent.review.manifest_hash) {
          throw new ReviewRefreshRequired('Review changed after saving');
        }
      }
      // Resolve Continue after editor saves and the authoritative refresh. Editing
      // may have already invalidated the completed target's old attempt.
      const completedRewind = intent.kind === 'continue' && intent.completedStepId
        && current.admission.reason !== 'edits_pending_continue' && current.continuation !== 'stopped';
      const kind = intent.kind === 'continue' && current.continuation === 'stopped'
        ? 'resume' : completedRewind ? 'rewind' : intent.kind;
      const stepId = completedRewind ? intent.completedStepId : intent.stepId;
      pending = { key, command: {
        command_id: id(), kind, expected_state_version: current.state_version,
        ...(stepId ? { step_id: stepId } : {}),
        ...(intent.review ? { review_id: intent.review.id, review_version: intent.review.version, manifest_hash: intent.review.manifest_hash } : {}),
        ...(intent.preferenceScope ? { preference_scope: intent.preferenceScope } : {}),
        ...(intent.kind === 'answer_input' && intent.input ? {
          input_artifact_id: intent.input.artifact_id, input_revision: intent.input.revision,
          answer_text: intent.answerText,
        } : {}),
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
