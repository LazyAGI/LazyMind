import { Alert, Button } from 'antd';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { localWorkflowRepairPath } from './workflowRepairNavigation';

export default function WorkflowRepairReturn() {
  const { t } = useTranslation();
  const { search } = useLocation();
  const navigate = useNavigate();
  const returnTo = localWorkflowRepairPath(new URLSearchParams(search).get('workflow_return_to'));
  if (!returnTo) return null;
  return <div style={{ position: 'fixed', bottom: 24, right: 24, zIndex: 900, maxWidth: 420 }}>
    <Alert type="info" showIcon message={t('chat.workflowReadinessReturnHint')}
      action={<Button onClick={() => navigate(returnTo, { replace: true })}>{t('chat.workflowReadinessReturn')}</Button>} />
  </div>;
}
