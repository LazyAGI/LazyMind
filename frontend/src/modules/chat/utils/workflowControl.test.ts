import { describe, expect, it, vi } from 'vitest';
import { controlActions, deliveryPending, ReviewRefreshRequired, type WorkflowControlView } from './workflowControl';

const state = (): WorkflowControlView => ({
  protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 3, continuation: 'awaiting_user',
  admission: { can_begin: false }, active_executions: 0, active_execution_ids: [], binding: { bound: true, generation: 1 },
  reviews: [{ id: 'review-1', execution_id: 'attempt-1', step_id: 'write', status: 'pending', version: 1, manifest_hash: 'hash-1' }],
  delivery: null, available_actions: ['confirm', 'confirm_and_continue', 'stop'],
});

describe('typed workflow user commands', () => {
  it('never silently confirms content changed by saving or another editor', async () => {
    const displayed = state();
    const fresh = state(); fresh.reviews[0] = { ...fresh.reviews[0], version: 2, manifest_hash: 'hash-2' };
    const write = vi.fn();
    const actions = controlActions(async () => fresh, write);
    await expect(actions.execute({ kind: 'confirm_and_continue', review: displayed.reviews[0] })).rejects.toBeInstanceOf(ReviewRefreshRequired);
    expect(write).not.toHaveBeenCalled();
  });
  it('reuses the exact command after an uncertain response', async () => {
    const current = state();
    const write = vi.fn().mockRejectedValueOnce(new Error('connection lost')).mockResolvedValue(undefined);
    const id = vi.fn(() => 'command-1');
    const actions = controlActions(async () => current, write, id);
    const intent = { kind: 'confirm' as const, review: { ...current.reviews[0] } };
    await expect(actions.execute(intent)).rejects.toThrow('connection lost');
    current.state_version = 99;
    await actions.execute(intent);
    expect(write.mock.calls[0][0]).toEqual(write.mock.calls[1][0]);
    expect(write.mock.calls[1][0].expected_state_version).toBe(3);
    expect(id).toHaveBeenCalledOnce();
  });
  it('shares an in-flight click instead of generating a second command', async () => {
    let finish!: () => void;
    const write = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    const actions = controlActions(async () => state(), write, () => 'command-1');
    const first = actions.execute({ kind: 'stop' });
    const second = actions.execute({ kind: 'stop' });
    expect(first).toBe(second);
    await Promise.resolve(); finish(); await first;
    expect(write).toHaveBeenCalledOnce();
  });
});


it('allows lifecycle resume after cancellation is accepted, without resending accepted continuation', () => {
  const control = state();
  control.delivery = { id: 'cancel', kind: 'cancel', status: 'accepted' };
  expect(deliveryPending(control)).toBe(false);
  control.delivery.kind = 'continue';
  expect(deliveryPending(control)).toBe(true);
  control.delivery.consumed_at = '2026-09-08T00:00:00Z';
  expect(deliveryPending(control)).toBe(false);
});

it('keeps panel controls available after a settled execution notification is delivered', () => {
  const control = state();
  control.delivery = { id: 'native-result', kind: 'continue', status: 'accepted', execution_id: 'native-1' };
  control.active_execution_ids = [];
  expect(deliveryPending(control)).toBe(false);
  control.active_execution_ids = ['native-1'];
  expect(deliveryPending(control)).toBe(true);
  control.delivery.status = 'unknown';
  control.active_execution_ids = [];
  expect(deliveryPending(control)).toBe(true);
});
