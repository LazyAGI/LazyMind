import { fireEvent, render, screen, within, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';
import { WorkflowControlActions } from './WorkflowControlActions';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

const review = { id: 'review-1', step_id: 'script', execution_id: 'attempt-1', status: 'pending' as const, version: 1, manifest_hash: 'hash-1' };

function control(overrides: Partial<WorkflowControlView> = {}): WorkflowControlView {
  return {
    protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 3, continuation: 'awaiting_user',
    admission: { can_begin: false }, active_executions: 0, active_execution_ids: [], binding: { bound: true, generation: 1 },
    reviews: [review], delivery: null,
    available_actions: ['save', 'confirm', 'confirm_and_continue', 'retry', 'rewind', 'stop'],
    ...overrides,
  };
}

function session(status: string): WorkflowSession {
  return {
    steps: [{ step_id: 'script', attempt: 1, status, validity: 'effective' }],
  } as WorkflowSession;
}

function renderActions(options?: { dirty?: boolean; stepStatus?: string; control?: WorkflowControlView }) {
  const act = vi.fn(async () => undefined);
  const runAction = vi.fn(async (action: () => Promise<void>) => { await action(); });
  render(<WorkflowControlActions
    control={options?.control ?? control()}
    act={act}
    context={{
      session: session(options?.stepStatus ?? 'succeeded'),
      stepId: 'script',
      stepIds: ['script'],
      pending: false,
      dirty: options?.dirty,
      runAction,
    }}
  />);
  return { act, runAction };
}

describe('WorkflowControlActions review footer', () => {
  it('shows review and regeneration actions without stop while awaiting review', () => {
    renderActions();
    expect(screen.getByRole('button', { name: 'chat.workflowControlConfirmContinue' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'chat.workflowControlRegenerate' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowStop' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowControlConfirm' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowControlSave' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowRetry' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowSkipThisApproval' })).not.toBeInTheDocument();
  });

  it('leaves saving to the shared panel footer', () => {
    renderActions({ dirty: true });
    expect(screen.queryByRole('button', { name: 'chat.workflowControlSave' })).not.toBeInTheDocument();
  });

  it('falls back to confirm-only when the host cannot continue from the panel', () => {
    renderActions({
      control: control({ available_actions: ['save', 'confirm', 'retry', 'rewind', 'stop'] }),
    });
    expect(screen.getByRole('button', { name: 'chat.workflowControlConfirm' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowControlConfirmContinue' })).not.toBeInTheDocument();
  });

  it('shows retry instead of regenerate after the current step failed', () => {
    renderActions({ stepStatus: 'failed' });
    expect(screen.getByRole('button', { name: 'chat.workflowRetry' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'chat.workflowControlRegenerate' })).not.toBeInTheDocument();
  });

  it('sends skip-approval as confirm-and-continue with a step preference', async () => {
    const { act } = renderActions();
    fireEvent.click(screen.getByRole('checkbox', { name: 'chat.workflowSkipThisApproval' }));
    fireEvent.click(screen.getByRole('button', { name: 'chat.workflowControlConfirmContinue' }));
    expect(act).toHaveBeenCalledWith({
      kind: 'confirm_and_continue',
      review,
      preferenceScope: 'step',
    });
  });

  it('rewinds the current succeeded step from regenerate', async () => {
    const { act } = renderActions();
    fireEvent.click(screen.getByRole('button', { name: 'chat.workflowControlRegenerate' }));
    expect(act).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('tooltip');
    expect(within(dialog).getByText('chat.workflowRegenerateConfirm')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'chat.workflowControlRegenerate' }));
    await waitFor(() => expect(act).toHaveBeenCalledWith({ kind: 'rewind', stepId: 'script' }));
  });
});

it('does not regenerate when confirmation is cancelled', async () => {
  const { act } = renderActions();
  fireEvent.click(screen.getByRole('button', { name: 'chat.workflowControlRegenerate' }));
  const dialog = await screen.findByRole('tooltip');
  fireEvent.click(within(dialog).getByRole('button', { name: 'chat.workflowRegenerateCancel' }));
  expect(act).not.toHaveBeenCalled();
});
it('regenerates a past step while a later step awaits review', async () => {
  const { act } = renderActions({ control: control({ reviews: [{ ...review, step_id: 'later_step' }] }) });
  expect(screen.getByRole('button', { name: 'chat.workflowControlRegenerate' })).toBeInTheDocument();
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  expect(screen.queryByText('later_step')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'chat.workflowControlRegenerate' }));
  const dialog = await screen.findByRole('tooltip');
  fireEvent.click(within(dialog).getByRole('button', { name: 'chat.workflowControlRegenerate' }));
  await waitFor(() => expect(act).toHaveBeenCalledWith({ kind: 'rewind', stepId: 'script' }));
});


describe('workflow action visibility across execution phases', () => {
  const allActions = ['continue', 'confirm', 'confirm_and_continue', 'rewind', 'retry', 'stop', 'resume'];
  it.each([
    ['initial', 'queued', 'continue', 0],
    ['external execution', 'running', 'continue', 1],
    ['native execution', 'running', 'awaiting_executor', 1],
  ])('shows only stop during %s even with broad server capabilities', (_label, stepStatus, continuation, active) => {
    renderActions({ stepStatus: String(stepStatus), control: control({
      continuation: String(continuation), active_executions: Number(active), reviews: [], available_actions: allActions,
    }) });
    expect(screen.getAllByRole('button').map(button => button.textContent)).toEqual(['chat.workflowStop']);
  });
  it('shows resume only after stopping', () => {
    renderActions({ stepStatus: 'cancelled', control: control({ continuation: 'stopped', reviews: [], available_actions: allActions }) });
    expect(screen.getAllByRole('button').map(button => button.textContent)).toEqual(['chat.workflowContinue']);
  });
  it('shows regeneration without continue or stop after completion', () => {
    renderActions({ control: control({ continuation: 'completed', reviews: [], available_actions: allActions }) });
    expect(screen.getAllByRole('button').map(button => button.textContent)).toEqual(['chat.workflowControlRegenerate']);
  });
  it('keeps regeneration available for a completed step during delivery', () => {
    renderActions({ control: control({ continuation: 'continue', reviews: [], available_actions: allActions,
      delivery: { id: 'action', kind: 'continue', status: 'dispatching', execution_id: '', consumed_at: undefined, last_error: '' },
    }) });
    expect(screen.getAllByRole('button').map(button => button.textContent)).toEqual(['chat.workflowControlRegenerate', 'chat.workflowStop']);
  });
});

it('confirms interruption before regenerating a completed step while a later step runs', async () => {
  const { act } = renderActions({ control: control({ continuation: 'awaiting_executor', reviews: [], active_executions: 1,
    available_actions: ['stop', 'rewind'],
  }) });
  fireEvent.click(screen.getByRole('button', { name: 'chat.workflowControlRegenerate' }));
  expect(act).not.toHaveBeenCalled();
  const dialog = await screen.findByRole('tooltip');
  expect(within(dialog).getByText('chat.workflowRegenerateRunningConfirm')).toBeInTheDocument();
  fireEvent.click(within(dialog).getByRole('button', { name: 'chat.workflowControlRegenerate' }));
  await waitFor(() => expect(act).toHaveBeenCalledWith({ kind: 'rewind', stepId: 'script' }));
});

it('explains why continuing is disabled until cancellation is acknowledged', () => {
  renderActions({ control: control({ continuation: 'stopped', reviews: [], available_actions: ['resume'],
    delivery: { id: 'cancel', kind: 'cancel', status: 'dispatching', execution_id: '', consumed_at: undefined, last_error: '' },
  }) });
  expect(screen.getByRole('button', { name: 'chat.workflowStopping' })).toBeDisabled();
});
it('sends resume when the user clicks continue after stopping', () => {
  const { act } = renderActions({ control: control({ continuation: 'stopped', reviews: [], available_actions: ['resume'] }) });
  fireEvent.click(screen.getByRole('button', { name: 'chat.workflowContinue' }));
  expect(act).toHaveBeenCalledWith({ kind: 'resume' });
});
