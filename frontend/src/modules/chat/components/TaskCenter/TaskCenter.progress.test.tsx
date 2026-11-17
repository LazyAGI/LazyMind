import { act, cleanup, render, screen } from '@testing-library/react';
import i18n from '@/i18n';
import { ordinary } from './ordinaryTestFixtures';
import type { PublicProcessStep } from '@/modules/chat/types/ordinaryTask';
import { afterEach, it, expect, vi } from 'vitest';
import { useTaskCenterStore } from '@/modules/chat/store/taskCenter';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import TaskCenter from './index';

vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => <div /> }));
afterEach(cleanup);

it.each(['paper-writing', 'ppt-workflow', 'writer-workflow'])('keeps %s indeterminate in developer mode despite an old percentage', workflow => {
  useWorkflowStore.setState({ sessionByConversation: {} });
  useTaskCenterStore.setState({ tasksByConversation: { progress: [{
    task_id: 'analysis', title: `${workflow}:analysis`, agent_type: 'workflow_step',
    mode: 'manual', status: 'running', progress_pct: 90, current_phase: '执行中...',
    artifacts: [], sources: [], artifact_streams: [], execution_log: [],
  }] } });
  const view = render(<TaskCenter sessionId='progress' developerMode />);
  expect(screen.queryByText('90%')).not.toBeInTheDocument();
  const indicator = view.container.querySelector('progress');
  expect(indicator).toBeInTheDocument();
  expect(indicator).not.toHaveAttribute('value');
  expect(indicator).toHaveClass('is-indeterminate');
});

it.each([
  ['paper-writing', 'literature_review'],
  ['ppt-workflow', 'collect_materials'],
  ['writer-workflow', 'write_document'],
])('shows an honest waiting state and real execution updates for %s', (workflow, step) => {
  useWorkflowStore.setState({ sessionByConversation: {} });
  useTaskCenterStore.setState({ tasksByConversation: {}, runsByConversation: {} });
  const initial = ordinary(`${workflow}:${step}`, {
    conversation_id: 'progress', agent_type: 'workflow_step', workflow_step_id: step,
    status: 'running', progress_pct: 90,
  });
  const store = useTaskCenterStore.getState();
  store.applyOrdinarySnapshot('progress', initial);
  const view = render(<TaskCenter sessionId='progress' />);
  expect(screen.getByText(i18n.t('taskCenter.ordinaryProcessingWithoutProgress'))).toBeInTheDocument();
  expect(view.container.querySelector('[aria-current="step"]')).toBeNull();

  act(() => store.applyOrdinarySnapshot('progress', { ...initial, revision: 2,
    plan_steps: ['Screen sources', 'Read materials', 'Write result'],
  }));
  expect(screen.getByText('Screen sources')).toBeInTheDocument();
  expect(screen.getByText(i18n.t('taskCenter.ordinaryLegacyPlanDescription'))).toBeInTheDocument();
  expect(screen.getByText(i18n.t('taskCenter.ordinaryProcessingWithoutProgress'))).toBeInTheDocument();
  expect(view.container.querySelector('[aria-current="step"]')).toBeNull();
  expect(view.container.querySelectorAll('.ordinary-plan-item.is-waiting')).toHaveLength(3);
  expect(view.container.querySelector('.ordinary-plan-progress')).not.toHaveTextContent(/—|90%/);
  expect(screen.getByRole('progressbar', { name: i18n.t('taskCenter.ordinaryPlan') })).not.toHaveAttribute('value');

  const process: PublicProcessStep[] = [
    { step_id: 'screen', revision: 1, order: 1, title: 'Sources selected', status: 'succeeded',
      started_at: null, finished_at: null, elapsed_ms: null },
    { step_id: 'read', revision: 1, order: 2, title: 'Reading selected materials', status: 'running',
      started_at: null, finished_at: null, elapsed_ms: null },
  ];
  act(() => store.applyOrdinarySnapshot('progress', { ...initial, revision: 3,
    process_state: 'available', process_steps: process,
  }));
  expect(screen.getByText('Sources selected').closest('li')).toHaveClass('is-complete');
  expect(screen.getByText('Reading selected materials').closest('li')).toHaveAttribute('aria-current', 'step');
  expect(screen.queryByText(i18n.t('taskCenter.ordinaryProcessingWithoutProgress'))).not.toBeInTheDocument();
  expect(view.container.querySelector('.ordinary-plan-progress')).toBeNull();

  act(() => store.applyOrdinarySnapshot('progress', { ...initial, revision: 4, status: 'succeeded',
    process_state: 'available', process_steps: process.map<PublicProcessStep>(item => ({ ...item, revision: 2, status: 'succeeded' })),
  }));
  expect(view.container.querySelector('[aria-current="step"]')).toBeNull();
  expect(view.container.querySelectorAll('.ordinary-thinking-item.is-complete')).toHaveLength(2);
});
