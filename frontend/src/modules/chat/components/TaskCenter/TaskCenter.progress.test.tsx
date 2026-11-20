import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, it, expect, vi } from 'vitest';
import i18n from '@/i18n';
import { useTaskCenterStore } from '@/modules/chat/store/taskCenter';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import type { PublicProcessStep } from '@/modules/chat/types/ordinaryTask';
import { ordinary } from './ordinaryTestFixtures';
import TaskCenter from './index';

vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => <div /> }));
afterEach(cleanup);

it.each(['paper-writing', 'ppt-workflow', 'writer-workflow'])('shows %s numeric progress in developer mode', workflow => {
  useWorkflowStore.setState({ sessionByConversation: {} });
  useTaskCenterStore.setState({ tasksByConversation: { progress: [{
    task_id: 'analysis', title: `${workflow}:analysis`, agent_type: 'workflow_step',
    mode: 'manual', status: 'running', progress_pct: 35,
    artifacts: [], sources: [], artifact_streams: [], execution_log: [],
  }] } });
  const view = render(<TaskCenter sessionId='progress' developerMode />);
  expect(screen.getByText('35%')).toBeInTheDocument();
  expect(view.container.querySelector('.ant-progress-bg')).toHaveStyle({ width: '35%' });
});

it.each([
  ['paper-writing', 'literature_review'],
  ['ppt-workflow', 'collect_materials'],
  ['writer-workflow', 'write_document'],
])('advances %s plan and bar from live snapshots before completion', (workflow, step) => {
  useWorkflowStore.setState({ sessionByConversation: {} });
  useTaskCenterStore.setState({ tasksByConversation: {}, runsByConversation: {} });
  const initial = ordinary(`${workflow}:${step}`, {
    conversation_id: 'progress', agent_type: 'workflow_step', workflow_step_id: step,
    status: 'running', progress_pct: 5, plan_steps: ['Read', 'Select', 'Analyze', 'Write', 'Deliver'],
  });
  const store = useTaskCenterStore.getState();
  store.applyOrdinarySnapshot('progress', initial);
  const view = render(<TaskCenter sessionId='progress' />);
  const bar = () => screen.getByRole('progressbar', { name: i18n.t('taskCenter.ordinaryPlan') });
  expect(bar()).toHaveAttribute('value', '5');
  expect(screen.getByText('Read').closest('li')).toHaveAttribute('aria-current', 'step');
  for (const [revision, percent, title, completed] of [[2, 20, 'Select', 1], [3, 50, 'Analyze', 2], [4, 90, 'Deliver', 4]] as const) {
    act(() => store.applyOrdinarySnapshot('progress', { ...initial, revision, progress_pct: percent }));
    expect(bar()).toHaveAttribute('value', String(percent));
    expect(screen.getByText(title).closest('li')).toHaveAttribute('aria-current', 'step');
    expect(view.container.querySelectorAll('.ordinary-plan-item.is-complete')).toHaveLength(completed);
    expect(view.container.querySelector('.ordinary-plan-progress')).not.toHaveTextContent('—');
  }
  act(() => store.applyOrdinarySnapshot('progress', { ...initial, revision: 5, status: 'succeeded', progress_pct: 100 }));
  expect(bar()).toHaveAttribute('value', '100');
  expect(view.container.querySelectorAll('.ordinary-plan-item.is-complete')).toHaveLength(5);
  expect(view.container.querySelector('[aria-current="step"]')).toBeNull();
});

it.each(['paper-writing', 'ppt-workflow', 'writer-workflow'])('preserves %s real process steps instead of estimating their status', workflow => {
  useWorkflowStore.setState({ sessionByConversation: {} });
  useTaskCenterStore.setState({ tasksByConversation: {}, runsByConversation: {} });
  const process: PublicProcessStep[] = [
    { step_id: 'read', revision: 1, order: 1, title: 'Read actual sources', status: 'succeeded', started_at: null, finished_at: null, elapsed_ms: null },
    { step_id: 'write', revision: 1, order: 2, title: 'Write actual result', status: 'running', started_at: null, finished_at: null, elapsed_ms: null },
  ];
  useTaskCenterStore.getState().applyOrdinarySnapshot('progress', ordinary(workflow, {
    conversation_id: 'progress', agent_type: 'workflow_step', workflow_step_id: 'write', status: 'running', progress_pct: 90,
    process_state: 'available', process_steps: process,
  }));
  const view = render(<TaskCenter sessionId='progress' />);
  expect(screen.getByText('Read actual sources').closest('li')).toHaveClass('is-complete');
  expect(screen.getByText('Write actual result').closest('li')).toHaveAttribute('aria-current', 'step');
  expect(view.container.querySelector('.ordinary-plan-progress')).toBeNull();
});
