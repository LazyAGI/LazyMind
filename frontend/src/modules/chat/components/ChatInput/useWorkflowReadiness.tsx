import { useCallback, useEffect, useRef, useState } from 'react';
import { Modal, message } from 'antd';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { getWorkflowReadiness, type WorkflowReadiness } from '@/modules/workflow/workflowDraftApi';
import type { ChatMention } from './MentionEditor';
import { localWorkflowRepairPath } from './workflowRepairNavigation';

export function useWorkflowReadiness(conversationId: string | undefined, preserveDraft: () => void, runInBackground = false) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [blocked, setBlocked] = useState<WorkflowReadiness>();
  const [checking, setChecking] = useState(false);
  const request = useRef<AbortController>();
  const blockedRef = useRef(false);
  useEffect(() => {
    blockedRef.current = false;
    setBlocked(undefined);
    setChecking(false);
    return () => { request.current?.abort(); request.current = undefined; };
  }, [conversationId, runInBackground]);
  const cancel = () => { blockedRef.current = false; setBlocked(undefined); };
  const check = useCallback(async (mentions: ChatMention[]) => {
    if (request.current || blockedRef.current) return false;
    const workflow = mentions.find(mention => mention.type === 'workflow');
    if (!workflow) return true;
    const controller = new AbortController();
    request.current = controller;
    setChecking(true);
    try {
      const result = await getWorkflowReadiness(workflow.resource_id,
        conversationId && !conversationId.startsWith('temp_') ? conversationId : undefined, controller.signal);
      if (controller.signal.aborted) return false;
      if (!result.ready) {
        blockedRef.current = true;
        setBlocked({ ...result, name: result.name || workflow.display_name });
        return false;
      }
      return true;
    } catch {
      if (!controller.signal.aborted) message.error(t('chat.workflowReadinessCheckFailed'));
      return false;
    } finally {
      if (request.current === controller) { request.current = undefined; setChecking(false); }
    }
  }, [conversationId, t]);
  const repairURL = localWorkflowRepairPath(blocked?.issues.find(issue => issue.repair_url)?.repair_url || blocked?.repair_url);
  const repair = () => {
    if (!repairURL) return;
    preserveDraft();
    const target = new URL(repairURL, window.location.origin);
    target.searchParams.set('workflow_return_to', location.pathname + location.search);
    cancel();
    navigate(target.pathname + target.search + target.hash);
  };
  const dialog = <Modal open={Boolean(blocked)} title={t('chat.workflowReadinessTitle')}
    closable maskClosable={false} onCancel={cancel} onOk={repair}
    okText={t('chat.workflowReadinessRepair')} cancelText={t('common.cancel')}
    okButtonProps={{ disabled: !repairURL }}>
    <p>{blocked?.name}</p>
    <ul>{blocked?.issues.map((issue, index) => <li key={`${issue.code}:${index}`}>
      {issue.node_id && <strong>{t('chat.workflowReadinessNode', { node: issue.node_id })} </strong>}
      {t(`selfEvolutionRun.validationErrors.${issue.code}`, { ...issue.details, defaultValue: issue.message })}
    </li>)}</ul>
    <p>{t(repairURL?.startsWith('/settings') ? 'chat.workflowReadinessSettingsHint'
      : blocked?.editable ? 'chat.workflowReadinessPublishHint' : 'chat.workflowReadinessReadonlyHint')}</p>
  </Modal>;
  return { check, busy: checking || Boolean(blocked), dialog };
}
