import { useTranslation } from 'react-i18next';
import type { WorkflowPanelControlContext } from '@/modules/chat/components/WorkflowPanel';
import { deliveryPending, type WorkflowActionIntent, type WorkflowControlView } from '@/modules/chat/utils/workflowControl';

export function WorkflowControlActions({ context, control, act }: {
  context: WorkflowPanelControlContext;
  control: WorkflowControlView;
  act(intent: WorkflowActionIntent): Promise<void>;
}) {
  const { t } = useTranslation();
  const available = new Set(control.available_actions);
  const review = control.reviews.find(item => item.status === 'pending' && context.stepIds.includes(item.step_id));
  const otherReview = !review && control.reviews.find(item => item.status === 'pending');
  const deliveryBusy = deliveryPending(control);
  const latest = context.session.steps?.filter(step => step.step_id === context.stepId && step.validity !== 'stale')
    .sort((a, b) => b.attempt - a.attempt)[0];
  const canRetry = !!latest && ['failed', 'interrupted', 'cancelled', 'canceled'].includes(latest.status);
  const canRewind = latest?.status === 'succeeded';
  const perform = (intent: WorkflowActionIntent, flush = true) => { void context.runAction(() => act(intent), flush); };
  const button = (label: string, intent: WorkflowActionIntent, enabled: boolean, tone = 'secondary', flush = true) =>
    <button type='button' className={`workflow-panel__action-btn workflow-panel__action-btn--${tone}`}
      disabled={context.pending || !enabled} onClick={() => perform(intent, flush)}>{label}</button>;
  return <>
    {available.has('save') && button(t('chat.workflowControlSave'), { kind: 'save' }, true)}
    {review && <>
      {button(t('chat.workflowControlConfirm'), { kind: 'confirm', review }, available.has('confirm'))}
      {button(t('chat.workflowControlConfirmContinue'), { kind: 'confirm_and_continue', review }, available.has('confirm_and_continue') && !deliveryBusy, 'primary')}
      {button(t('chat.workflowSkipThisApproval'), { kind: 'confirm_and_continue', review, preferenceScope: 'step' }, available.has('confirm_and_continue') && !deliveryBusy)}
    </>}
    {otherReview && <span role='status'>{t('chat.workflowControlOtherReview', { step: otherReview.step_id })}</span>}
    {!review && available.has('continue') && button(t('chat.workflowContinue'), { kind: 'continue' }, !deliveryBusy, 'primary')}
    {available.has('retry') && canRetry && button(t('chat.workflowRetry'), { kind: 'retry', stepId: context.stepId }, !!context.stepId && !deliveryBusy)}
    {available.has('rewind') && canRewind && button(t(review ? 'chat.workflowControlRegenerate' : 'chat.workflowControlRewind'), { kind: 'rewind', stepId: context.stepId }, !!context.stepId && !deliveryBusy)}
    {available.has('stop') && button(t('chat.workflowStop'), { kind: 'stop' }, true, 'danger', false)}
    {available.has('resume') && button(t('chat.workflowControlResume'), { kind: 'resume' }, !deliveryBusy, 'primary', false)}
  </>;
}
