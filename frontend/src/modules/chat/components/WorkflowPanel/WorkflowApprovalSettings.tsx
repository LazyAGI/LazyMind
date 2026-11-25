import { useEffect, useState } from 'react';
import { Button, Checkbox, Popover, Radio, message, type RadioChangeEvent, type CheckboxChangeEvent } from 'antd';
import { WorkflowToolbarIcon } from './WorkflowToolbarIcon';
import { useTranslation } from 'react-i18next';
import { getLocalizedErrorMessage } from '@/components/request';
import type { WorkflowSession } from '../../store/workflowPanel';
import { WorkflowSessionApi } from '../../utils/request';

export function WorkflowApprovalSettings({ session, onRefresh, disabled }: {
  session: WorkflowSession;
  onRefresh(): Promise<void>;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState(session.approval_mode ?? session.workflow_mode);
  const [remember, setRemember] = useState(false);
  const [saving, setSaving] = useState(false);
  const currentMode = session.approval_mode || session.workflow_mode;
  useEffect(() => { setOpen(false); }, [session.session_id]);
  async function save() {
    setSaving(true);
    try {
      await WorkflowSessionApi().setApprovalPreference(session.session_id, {
        scope: remember ? 'workflow' : 'session', approval_required: mode === 'dynamic',
        expected_state_version: session.state_version ?? 0,
      });
      await onRefresh();
      setOpen(false);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally { setSaving(false); }
  }
  return <Popover trigger='click' placement='bottomRight' open={open}
    onOpenChange={(next: boolean) => { if (saving) return; setOpen(next); if (next) { setMode(currentMode); setRemember(false); } }}
    content={<div className='workflow-approval-settings'>
      <Radio.Group value={mode} onChange={(event: RadioChangeEvent) => setMode(event.target.value)} disabled={saving}>
        <Radio value='dynamic'>{t('chat.workflowApprovalOnDemand')}</Radio>
        <Radio value='auto'>{t('chat.workflowApprovalAutomatic')}</Radio>
      </Radio.Group>
      <Checkbox checked={remember} onChange={(event: CheckboxChangeEvent) => setRemember(event.target.checked)} disabled={saving}>
        {t('chat.workflowApprovalRemember')}
      </Checkbox>
      <p>{t('chat.workflowApprovalScopeHint')}</p>
      <Button type='primary' size='small' onClick={() => void save()} loading={saving}>{t('chat.workflowApprovalApply')}</Button>
    </div>}>
    <button type='button' className='workflow-panel__tool' disabled={disabled || saving}
      aria-expanded={open} aria-label={t('chat.workflowApprovalSettings')}>
      <WorkflowToolbarIcon name='approval' />{t(currentMode === 'auto' ? 'chat.workflowApprovalAutomatic' : 'chat.workflowApprovalOnDemand')}
      <WorkflowToolbarIcon name='chevron' />
    </button>
  </Popover>;
}
