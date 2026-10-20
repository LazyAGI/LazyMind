import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';

const api = vi.hoisted(() => ({ summary: vi.fn(), artifact: vi.fn(), relay: vi.fn(), decide: vi.fn() }));
vi.mock('./productApi', () => ({ productApi: api, PRODUCT_STAGES: ['direction', 'competitive', 'design', 'prd', 'prototype', 'review', 'handoff'] }));
vi.mock('./SlotComponents', () => ({ SlotRenderer: () => null }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'request failed' }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ i18n: { language: 'en' } }) }));
import { ProductProject } from './ProductProject';

const summary = {
  supported: true, can_relay: true, state_version: 7, current_stage: 'design',
  next_stages: [{ id: 'prd', label: 'PRD' }], actions: ['continue', 'switch-stage', 'finish'],
  can_accept_current_artifact: false,
  project: { workspace_id: 'project', artifacts: [], drafts: [], can_update_decisions: true,
    decisions: [{ decision_id: 'decision-1', title: 'Share data between tenants', status: 'proposed',
      decision_hash: 'sha256:reviewed-content', confirmation_required: true,
      decision_content: { value: 'Only share after explicit consent' } }] },
};
const session = { session_id: 'session', state_version: 7, status: 'completed' } as WorkflowSession;
function show() {
  return render(<ProductProject session={session} disabled={false} beforeAction={async () => true} onRefresh={vi.fn()} />);
}

afterEach(cleanup);
beforeEach(() => {
  vi.clearAllMocks();
  api.summary.mockResolvedValue(summary);
  api.decide.mockResolvedValue({ state_version: 8 });
  api.relay.mockResolvedValue({ session_id: 'session' });
});

describe('Product decisions', () => {
  it('submits the version and hash the user explicitly reviewed', async () => {
    show();
    await screen.findByText('Share data between tenants');
    expect(api.decide).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Accept decision' }));
    await waitFor(() => expect(api.decide).toHaveBeenCalledTimes(1));
    expect(api.decide.mock.calls[0].slice(0, 2)).toEqual(['session', 'decision-1']);
    expect(api.decide.mock.calls[0][2]).toMatchObject({ action: 'accept', expected_state_version: 7, expected_decision_hash: 'sha256:reviewed-content' });
    expect(api.relay).not.toHaveBeenCalled();
  });

  it('defers without translating the action into acceptance', async () => {
    show();
    fireEvent.click(await screen.findByRole('button', { name: 'Defer decision' }));
    await waitFor(() => expect(api.decide).toHaveBeenCalledTimes(1));
    expect(api.decide.mock.calls[0][2].action).toBe('defer');
  });

  it('retries an uncertain response with the same exact decision command', async () => {
    api.decide.mockRejectedValueOnce(new Error('connection lost')).mockResolvedValueOnce({ state_version: 8 });
    show();
    fireEvent.click(await screen.findByRole('button', { name: 'Accept decision' }));
    await screen.findByText('request failed');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Accept decision' })).not.toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: 'Accept decision' }));
    await waitFor(() => expect(api.decide).toHaveBeenCalledTimes(2));
    expect(api.decide.mock.calls[1]).toEqual(api.decide.mock.calls[0]);
  });
});
