import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, it, expect, vi } from 'vitest';
import { useTaskCenterStore } from '@/modules/chat/store/taskCenter';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import TaskCenter from './index';

vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => <div /> }));
afterEach(cleanup);

it('keeps a running workflow indeterminate despite an old 90 percent snapshot', () => {
  useWorkflowStore.setState({ sessionByConversation: {} });
  useTaskCenterStore.setState({ tasksByConversation: { progress: [{
    task_id: 'analysis', title: 'paper-writing:data_analysis', agent_type: 'workflow_step',
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
