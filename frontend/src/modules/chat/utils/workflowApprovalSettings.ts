import i18n from '@/i18n';
import type { WorkflowSession } from '../store/workflowPanel';
import type { NativeWorkflowControl } from '../store/workflowStatus';
import { WorkflowSessionApi } from './request';
import { controlActions, externalPanelAction, type WorkflowControlView } from './workflowControl';

export type SendWorkflowMessage = (text: string, control?: NativeWorkflowControl) => void | boolean | Promise<void | boolean>;

/** Both settings surfaces use the same policy write and admitted continuation. */
export async function applyWorkflowApprovalSettings(
  session: WorkflowSession,
  mode: 'auto' | 'dynamic',
  remember: boolean,
  onSendMessage?: SendWorkflowMessage,
) {
  const api = WorkflowSessionApi();
  const stepId = session.projection?.display?.status === 'waiting'
    ? session.projection.display.approval_step_id : undefined;
  const release = mode === 'auto' && Boolean(stepId);
  let externalApproval;
  const readControl = async (): Promise<WorkflowControlView> => (await api.getControl(session.session_id)).data.data.control;
  if (release) {
    if (session.controller_host === 'external-agent') {
      externalApproval = externalPanelAction({ kind: 'approve', stepId }, session, await readControl());
    } else if (!onSendMessage) {
      throw new Error(i18n.t('chat.workflowApprovalContinueUnavailable'));
    }
  }
  const result = await api.setApprovalPreference(session.session_id, {
    scope: remember ? 'workflow' : 'session', remember,
    approval_required: mode === 'dynamic', expected_state_version: session.state_version ?? 0,
  });
  if (externalApproval) {
    await controlActions(readControl, command => api.control(session.session_id, command)).execute(externalApproval);
  } else if (release) {
    const submitted = await onSendMessage!(i18n.t('chat.workflowContinue'), {
      session_id: session.session_id, kind: 'approve', step_id: stepId,
      expected_state_version: result.data.data.state_version,
    });
    if (submitted === false) throw new Error(i18n.t('chat.workflowApprovalContinueUnavailable'));
  }
}
