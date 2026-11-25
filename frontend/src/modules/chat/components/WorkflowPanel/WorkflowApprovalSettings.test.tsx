import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { WorkflowSession } from '../../store/workflowPanel';
import { WorkflowApprovalSettings } from './WorkflowApprovalSettings';

const save = vi.hoisted(() => vi.fn());
vi.mock('../../utils/request', () => ({ WorkflowSessionApi: () => ({ setApprovalPreference: save }) }));
afterEach(cleanup);
beforeEach(async () => { save.mockReset().mockResolvedValue({}); await i18n.changeLanguage('zh-CN'); });
const session = { session_id: 'run', state_version: 7, workflow_mode: 'auto', approval_mode: 'auto' } as WorkflowSession;

it('restores approvals for this run by default and refreshes authoritative state', async () => {
  const refresh = vi.fn(async () => {});
  render(<WorkflowApprovalSettings session={session} onRefresh={refresh} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '审批设置' }));
  fireEvent.click(await screen.findByRole('radio', { name: '按需审批' }));
  fireEvent.click(screen.getByRole('button', { name: /应\s*用/ }));
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
  expect(save).toHaveBeenCalledWith('run', { scope: 'session', approval_required: true, expected_state_version: 7 });
});

it('saves future-workflow policy only when explicitly checked', async () => {
  render(<WorkflowApprovalSettings session={session} onRefresh={vi.fn(async () => {})} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '审批设置' }));
  fireEvent.click(await screen.findByRole('checkbox', { name: '以后此工作流沿用此设置' }));
  fireEvent.click(screen.getByRole('button', { name: /应\s*用/ }));
  await waitFor(() => expect(save).toHaveBeenCalledWith('run', { scope: 'workflow', approval_required: false, expected_state_version: 7 }));
});

it('keeps the previous mode and settings open when saving fails', async () => {
  save.mockRejectedValue(new Error('save failed'));
  const refresh = vi.fn(async () => {});
  render(<WorkflowApprovalSettings session={session} onRefresh={refresh} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '审批设置' }));
  fireEvent.click(await screen.findByRole('radio', { name: '按需审批' }));
  fireEvent.click(screen.getByRole('button', { name: /应\s*用/ }));
  await waitFor(() => expect(screen.getByRole('button', { name: /应\s*用/ })).toBeEnabled());
  expect(screen.getByRole('button', { name: '审批设置' })).toHaveTextContent('自动执行');
  expect(refresh).not.toHaveBeenCalled();
});
