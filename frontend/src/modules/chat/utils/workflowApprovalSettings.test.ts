import { beforeEach, expect, it, vi } from 'vitest';
import { applyWorkflowApprovalSettings } from './workflowApprovalSettings';
import type { WorkflowSession } from '../store/workflowPanel';
const api = vi.hoisted(() => ({ setApprovalPreference: vi.fn(), getControl: vi.fn(), control: vi.fn() }));
vi.mock('./request', () => ({ WorkflowSessionApi: () => api }));
const session = { session_id: 'run', state_version: 7, controller_host: 'lazymind',
  projection: { display: { status: 'waiting', approval_step_id: 'outline' } },
} as WorkflowSession;
beforeEach(() => {
  vi.resetAllMocks();
  api.setApprovalPreference.mockResolvedValue({ data: { data: { state_version: 8 } } });
});
it('releases a native pending review using the version after saving policy', async () => {
  const send = vi.fn().mockResolvedValue(true);
  await applyWorkflowApprovalSettings(session, 'auto', false, send);
  expect(send).toHaveBeenCalledWith(expect.any(String), {
    session_id: 'run', kind: 'approve', step_id: 'outline', expected_state_version: 8,
  });
});
it.each(['running', 'failed', 'completed', 'paused'])('does not continue or retry a %s run', async status => {
  const send = vi.fn();
  await applyWorkflowApprovalSettings({ ...session, projection: { display: { status } } } as WorkflowSession, 'auto', false, send);
  expect(send).not.toHaveBeenCalled();
});
it('keeps the pending review when restoring dynamic mode', async () => {
  const send = vi.fn();
  await applyWorkflowApprovalSettings(session, 'dynamic', true, send);
  expect(send).not.toHaveBeenCalled();
});
it('never approves if the versioned policy write fails', async () => {
  api.setApprovalPreference.mockRejectedValue(new Error('conflict'));
  const send = vi.fn();
  await expect(applyWorkflowApprovalSettings(session, 'auto', false, send)).rejects.toThrow('conflict');
  expect(send).not.toHaveBeenCalled();
});
it('reports a refused native submission instead of claiming success', async () => {
  await expect(applyWorkflowApprovalSettings(session, 'auto', false, vi.fn().mockResolvedValue(false))).rejects.toThrow();
});
it('uses external confirmation and continuation with the exact reviewed checkpoint', async () => {
  api.getControl.mockResolvedValue({ data: { data: { control: {
    state_version: 8, reviews: [{ id: 'review', step_id: 'outline', status: 'pending', version: 2, manifest_hash: 'hash' }],
  } } } });
  const send = vi.fn();
  await applyWorkflowApprovalSettings({ ...session, controller_host: 'external-agent' }, 'auto', true, send);
  expect(api.control).toHaveBeenCalledWith('run', expect.objectContaining({ kind: 'confirm_and_continue',
    expected_state_version: 8, review_id: 'review', review_version: 2, manifest_hash: 'hash', step_id: 'outline' }));
  expect(send).not.toHaveBeenCalled();
});
