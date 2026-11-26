import { useEffect, useState } from 'react';
import { Button, Checkbox, Popover, Radio, Tooltip, message, type RadioChangeEvent, type CheckboxChangeEvent } from 'antd';
import { CheckOutlined } from '@ant-design/icons';
import { WorkflowToolbarIcon } from './WorkflowToolbarIcon';
import { useTranslation } from 'react-i18next';
import { getLocalizedErrorMessage } from '@/components/request';
import type { WorkflowSession } from '../../store/workflowPanel';
import { applyWorkflowApprovalSettings, workflowApprovalErrorMessage, type SendWorkflowMessage } from '../../utils/workflowApprovalSettings';
import { WorkflowSessionApi } from '../../utils/request';

export function WorkflowApprovalSettings({ session, onRefresh, disabled, onSendMessage }: {
  session: WorkflowSession;
  onRefresh(): Promise<void>;
  disabled: boolean;
  onSendMessage?: SendWorkflowMessage;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState(session.approval_mode ?? session.workflow_mode);
  const [remember, setRemember] = useState(Boolean(session.saved_approval_mode));
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const currentMode = session.approval_mode || session.workflow_mode;
  useEffect(() => { setOpen(false); }, [session.session_id]);
  async function save() {
    setSaving(true);
    try {
      await applyWorkflowApprovalSettings(session, mode, remember, onSendMessage);
      await onRefresh();
      message.success(t(remember ? 'chat.workflowApprovalSavedRemembered' : 'chat.workflowApprovalSaved', {
        mode: t(mode === 'auto' ? 'chat.workflowApprovalAutomatic' : 'chat.workflowApprovalOnDemand'),
      }));
      setOpen(false);
    } catch (error) {
      message.error(await workflowApprovalErrorMessage(error, onRefresh));
    } finally { setSaving(false); }
  }
  async function changeOpen(next: boolean) {
    if (saving || loading) return;
    setOpen(next);
    if (!next) return;
    setMode(currentMode);
    setLoading(true);
    try {
      const result = await WorkflowSessionApi().getSession(session.session_id, { silentError: true } as never);
      setRemember(Boolean(result.data.data.session.saved_approval_mode));
    } catch (error) {
      setOpen(false);
      message.error(getLocalizedErrorMessage(error));
    } finally { setLoading(false); }
  }
  return <Popover trigger='click' placement='bottomRight' open={open} rootClassName='workflow-approval-popover'
    onOpenChange={(next: boolean) => void changeOpen(next)}
    content={<div className='workflow-approval-settings'>
      <Radio.Group value={mode} onChange={(event: RadioChangeEvent) => setMode(event.target.value)} disabled={saving || loading}>
        <Radio value='dynamic'><span className='workflow-approval-settings__option'>
          {t('chat.workflowApprovalOnDemand')}<CheckOutlined aria-hidden />
        </span></Radio>
        <Radio value='auto'><span className='workflow-approval-settings__option'>
          {t('chat.workflowApprovalAutomatic')}<CheckOutlined aria-hidden />
        </span></Radio>
      </Radio.Group>
      <div className='workflow-approval-settings__footer'>
        <Tooltip title={t('chat.workflowApprovalRememberHint')} trigger={['hover', 'focus']}>
          <Checkbox checked={remember} onChange={(event: CheckboxChangeEvent) => setRemember(event.target.checked)} disabled={saving || loading}>
            {t('chat.workflowApprovalRemember')}
          </Checkbox>
        </Tooltip>
        <Button className='workflow-approval-settings__apply' type='text' size='small' autoInsertSpace={false}
          onClick={() => void save()} loading={saving} disabled={loading}>{t('chat.workflowApprovalApply')}</Button>
      </div>
    </div>}>
    <button type='button' className='workflow-panel__tool' disabled={disabled || saving}
      aria-expanded={open} aria-label={t('chat.workflowApprovalSettings')}>
      <WorkflowToolbarIcon name='approval' />{t(currentMode === 'auto' ? 'chat.workflowApprovalAutomatic' : 'chat.workflowApprovalOnDemand')}
      <WorkflowToolbarIcon name='chevron' />
    </button>
  </Popover>;
}
