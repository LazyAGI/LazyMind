import { Popconfirm } from 'antd';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { WorkflowPanelControlContext } from '@/modules/chat/components/WorkflowPanel';
import { deliveryPending, type WorkflowActionIntent, type WorkflowControlView } from '@/modules/chat/utils/workflowControl';

export function WorkflowControlActions({ context, control, act }: {
  context: WorkflowPanelControlContext;
  control: WorkflowControlView;
  act(intent: WorkflowActionIntent): Promise<void>;
}) {
  const { t } = useTranslation();
  const [skipApproval, setSkipApproval] = useState(false);
  const available = new Set(control.available_actions);
  const review = control.reviews.find(item => item.status === 'pending' && context.stepIds.includes(item.step_id));
  const deliveryBusy = deliveryPending(control);
  const latest = context.session.steps?.filter(step => step.step_id === context.stepId && step.validity !== 'stale')
    .sort((a, b) => b.attempt - a.attempt)[0];
  const executing = control.active_executions > 0
    || ['continue', 'awaiting_executor', 'draining'].includes(control.continuation)
    || deliveryBusy && control.delivery?.kind === 'continue';
  const settled = !executing && !deliveryBusy
    && ['awaiting_user', 'completed', 'failed'].includes(control.continuation);
  const canRetry = settled && !!latest && ['failed', 'interrupted', 'cancelled', 'canceled'].includes(latest.status);
  const canRewind = latest?.status === 'succeeded';
  const showConfirmContinue = !!review && available.has('confirm_and_continue');
  // Confirm-only is the fallback when the host cannot be resumed from this panel.
  const showConfirmOnly = !!review && available.has('confirm') && !showConfirmContinue;
  const perform = (intent: WorkflowActionIntent, flush = true) => { void context.runAction(() => act(intent), flush); };
  const button = (label: string, intent: WorkflowActionIntent, enabled: boolean, tone = 'secondary', flush = true) =>
    <button type='button' className={`workflow-panel__action-btn workflow-panel__action-btn--${tone}`}
      disabled={context.pending || !enabled} onClick={() => perform(intent, flush)}>{label}</button>;
  return <>
    {showConfirmOnly && button(t('chat.workflowControlConfirm'), { kind: 'confirm', review }, true)}
    {showConfirmContinue && <>
      <label className='workflow-panel__footer-skip-approval'>
        <input type='checkbox' checked={skipApproval} disabled={context.pending || deliveryBusy}
          onChange={event => setSkipApproval(event.target.checked)} />
        {t('chat.workflowSkipThisApproval')}
      </label>
      {button(t('chat.workflowControlConfirmContinue'), {
        kind: 'confirm_and_continue', review, preferenceScope: skipApproval ? 'step' : undefined,
      }, !deliveryBusy, 'primary')}
    </>}
    {available.has('retry') && canRetry && button(t('chat.workflowRetry'), { kind: 'retry', stepId: context.stepId }, !!context.stepId && !deliveryBusy)}
    {available.has('rewind') && canRewind && <Popconfirm key={context.stepId}
      title={t('chat.workflowControlRegenerate')}
      description={t(executing ? 'chat.workflowRegenerateRunningConfirm' : 'chat.workflowRegenerateConfirm')}
      okText={t('chat.workflowControlRegenerate')}
      cancelText={t('chat.workflowRegenerateCancel')}
      disabled={context.pending || !context.stepId || (control.continuation === 'stopped' && deliveryBusy)}
      onConfirm={() => perform({ kind: 'rewind', stepId: context.stepId })}>
      <button type='button' className='workflow-panel__action-btn workflow-panel__action-btn--secondary'
        disabled={context.pending || !context.stepId || (control.continuation === 'stopped' && deliveryBusy)}>{t('chat.workflowControlRegenerate')}</button>
    </Popconfirm>}
    {executing && available.has('stop') && button(t('chat.workflowStop'), { kind: 'stop' }, true, 'danger', false)}
    {!review && !executing && control.continuation === 'awaiting_user' && available.has('continue') && button(t('chat.workflowContinue'), { kind: 'continue' }, !deliveryBusy, 'primary')}
    {control.continuation === 'stopped' && control.active_executions === 0 && available.has('resume') && button(t(deliveryBusy ? 'chat.workflowStopping' : 'chat.workflowContinue'), { kind: 'resume' }, !deliveryBusy, 'primary')}
  </>;
}
