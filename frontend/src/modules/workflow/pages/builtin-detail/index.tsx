import { useState, useEffect } from 'react';
import { useParams, useNavigate, useOutletContext } from 'react-router-dom';
import { Breadcrumb, Skeleton, Alert } from 'antd';
import { useTranslation } from 'react-i18next';
import { getBuiltinWorkflow } from '../../workflowDraftApi';
import type { BuiltinWorkflow } from '../../workflowDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';
import { localizeErrorCode } from '@/components/request';

export default function BuiltinWorkflowDetailPage() {
  const { workflowId } = useParams<{ workflowId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const { isMenuCollapsed, toggleMenu } = useOutletContext<{
    isMenuCollapsed: boolean;
    toggleMenu: () => void;
  }>();

  const [workflow, setWorkflow] = useState<BuiltinWorkflow | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!workflowId) return;
    setLoading(true);
    setError(null);
    getBuiltinWorkflow(workflowId)
      .then((data) => setWorkflow(data))
      .catch(() => setError(localizeErrorCode('2000509')))
      .finally(() => setLoading(false));
  }, [workflowId]);

  // Collapse side menu when entering detail page (same pattern as WorkflowDetailPage)
  useEffect(() => {
    if (!isMenuCollapsed) toggleMenu();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (loading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active />
      </div>
    );
  }

  if (error || !workflow) {
    return (
      <div style={{ padding: 24 }}>
        <Alert
          type="error"
          message={error ?? t('selfEvolutionRun.builtinWorkflowNotFound')}
          action={
            <button
              style={{ cursor: 'pointer', background: 'none', border: 'none', color: '#1677ff' }}
              onClick={() => navigate('/memory-management/skills?skillView=workflows')}
            >
              {t('selfEvolutionRun.builtinWorkflowBackToList')}
            </button>
          }
        />
      </div>
    );
  }

  const workflowName = (
    <Breadcrumb
      items={[
      { title: <a onClick={() => navigate('/memory-management/skills?skillView=workflows')}>{t('selfEvolutionRun.builtinWorkflowListBreadcrumb')}</a> },
        { title: workflow.name || workflow.id },
      ]}
    />
  );

  let stateYaml = workflow.state_yaml_raw;
  if (stateYaml && workflow.layout_raw) {
    try {
      const layout = JSON.parse(workflow.layout_raw) as Record<string, unknown>;
      if (Object.keys(layout).length > 0) {
        stateYaml = `x-layout:\n${Object.entries(layout)
          .map(([id, value]) => `  ${JSON.stringify(id)}: ${JSON.stringify(value)}`)
          .join('\n')}\n${stateYaml}`;
      }
    } catch {
      // Ignore malformed optional layout data and keep the state definition usable.
    }
  }

  return (
    <StateGraphEditor
      initialWorkflowYaml={workflow.workflow_yaml_raw}
      initialStateYaml={stateYaml}
      initialScenarioContent={workflow.scenario_raw}
      workflowName={workflowName}
      readonly={true}
      showEmptyHint={false}
      onClose={() => navigate('/memory-management/skills?skillView=workflows')}
    />
  );
}
