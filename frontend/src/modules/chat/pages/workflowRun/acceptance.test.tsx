import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import WorkflowRunPage from './index';
import type { WorkflowPanelControlAdapter } from '@/modules/chat/components/WorkflowPanel';

// Exercise the real run-page command/receipt/error path, without dispatching a
// live Agent. Panel controls themselves are covered by WorkflowPanel.layout.
const fixture = vi.hoisted(() => ({
  load: vi.fn(), control: vi.fn(), setSession: vi.fn(), reload: () => {},
  review: { id: 'review', step_id: 'analyze', execution_id: 'attempt', status: 'pending', version: 1, manifest_hash: 'test-hash' },
}));
vi.mock('react-router-dom', () => ({ useParams: () => ({ sessionId: 'acceptance-test' }) }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getUserInfo: () => ({ userId: 'test-user' }) } }));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({ control: fixture.control }) }));
vi.mock('@/modules/chat/store/workflowPanel', () => ({ useWorkflowStore: (select: any) => select({ setSession: fixture.setSession }) }));
vi.mock('./loadRun', () => ({
  loadWorkflowRunSnapshot: (...args: unknown[]) => fixture.load(...args),
  watchWorkflowRun: (_id: string, reload: () => void) => { fixture.reload = reload; return () => {}; },
}));
vi.mock('@/modules/chat/components/WorkflowPanel/external/useExecutionActivity', () => ({ useExecutionActivity: () => ({}) }));
vi.mock('@/modules/chat/components/WorkflowPanel', () => ({ WorkflowPanel: ({ controlAdapter }: { controlAdapter: WorkflowPanelControlAdapter }) =>
  <button onClick={() => controlAdapter.execute({ kind: 'approve', stepId: 'analyze' })}>确认测试审核</button>,
}));
beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  fixture.load.mockResolvedValue({
    session: { session_id: 'acceptance-test', status: 'waiting', steps: [] },
    control: { protocol: 'workflow.control.v1', session_id: 'acceptance-test', state_version: 1,
      continuation: 'awaiting_user', reviews: [fixture.review], admission: {}, binding: {}, delivery: null,
      active_executions: 0, active_execution_ids: [], available_actions: ['confirm'] },
  });
  fixture.control.mockImplementation(async (_id, command) => ({ data: { data: { receipt: { command_id: command.command_id } } } }));
});
afterEach(cleanup);

it('refreshes after the matching receipt without adding a routine confirmation banner', async () => {
  let acknowledge!: () => void;
  fixture.control.mockImplementation((_id, command) => new Promise(resolve => {
    acknowledge = () => resolve({ data: { data: { receipt: { command_id: command.command_id } } } });
  }));
  render(<WorkflowRunPage embedded />);
  fireEvent.click(await screen.findByRole('button', { name: '确认测试审核' }));
  await waitFor(() => expect(fixture.control).toHaveBeenCalledOnce());
  expect(fixture.control.mock.calls[0][1]).toMatchObject({ kind: 'confirm_and_continue', step_id: 'analyze', expected_state_version: 1, review_id: 'review', review_version: 1, manifest_hash: 'test-hash' });
  expect(fixture.load).toHaveBeenCalledTimes(2);
  act(() => acknowledge());
  await waitFor(() => expect(fixture.load).toHaveBeenCalledTimes(3));
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it.each(['missing', 'changed'])('keeps the approval unsubmitted when the displayed review is %s', async scenario => {
  const baseline = await fixture.load();
  fixture.load.mockClear();
  const fresh = structuredClone(baseline);
  if (scenario === 'missing') baseline.control.reviews = [];
  else fresh.control.reviews[0] = { ...fixture.review, version: 2, manifest_hash: 'changed' };
  fixture.load.mockResolvedValueOnce(baseline).mockResolvedValue(fresh);
  render(<WorkflowRunPage embedded />);
  fireEvent.click(await screen.findByRole('button', { name: '确认测试审核' }));
  expect(await screen.findByText('内容已保存或发生变化。请检查更新后的内容，再确认当前步骤。')).toBeVisible();
  expect(fixture.control).not.toHaveBeenCalled();
});

it.each(['STATE_VERSION_CONFLICT', 'REVIEW_VERSION_CONFLICT'])(
  'shows %s without claiming approval succeeded', async code => {
    fixture.control.mockRejectedValue({ response: { status: 409, data: { error: { code, message: 'State changed; refresh required' } } } });
    render(<WorkflowRunPage embedded />);
    fireEvent.click(await screen.findByRole('button', { name: '确认测试审核' }));
    expect(await screen.findByText(code === 'REVIEW_VERSION_CONFLICT'
      ? '内容已保存或发生变化。请检查更新后的内容，再确认当前步骤。' : 'State changed; refresh required')).toBeVisible();
    expect(screen.queryByText('当前步骤已确认。')).not.toBeInTheDocument();
    await waitFor(() => expect(fixture.load.mock.calls.length).toBeGreaterThanOrEqual(3));
  },
);

it('does not accept a mismatched receipt as success', async () => {
  fixture.control.mockResolvedValue({ data: { data: { receipt: { command_id: 'another-command' } } } });
  render(<WorkflowRunPage embedded />);
  fireEvent.click(await screen.findByRole('button', { name: '确认测试审核' }));
  expect(await screen.findByText('Workflow command acknowledgement is unavailable')).toBeVisible();
  expect(screen.queryByText('当前步骤已确认。')).not.toBeInTheDocument();
});

it('clears a failed authentication/read warning after a successful resynchronization', async () => {
  fixture.load.mockRejectedValueOnce(new Error('401 Unauthorized'));
  render(<WorkflowRunPage embedded />);
  expect(await screen.findByText('401 Unauthorized')).toBeVisible();
  act(() => fixture.reload());
  expect(await screen.findByRole('button', { name: '确认测试审核' })).toBeVisible();
  expect(screen.queryByText('401 Unauthorized')).not.toBeInTheDocument();
});
