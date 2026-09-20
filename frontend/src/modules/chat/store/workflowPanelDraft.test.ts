import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { draftStore, useWorkflowStore } from './workflowPanel';

const sessionId = 'session-draft';
const slotId = 'document';
const listIndex = 0;

describe('workflow slot draft persistence', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
  });

  afterEach(() => {
    draftStore.cancelDraft(sessionId, slotId, listIndex);
    vi.useRealTimers();
  });

  it('forwards the stored revision and draft-version baseline', async () => {
    const patchSlotItemValue = vi.fn().mockResolvedValue(7);
    useWorkflowStore.setState({ patchSlotItemValue });
    draftStore.setDraft(
      sessionId, slotId, listIndex, { text: '# Draft' }, -1, 7, 3,
    );

    await expect(draftStore.flushDraft(sessionId, slotId, listIndex, -1)).resolves.toBe(true);

    expect(patchSlotItemValue).toHaveBeenCalledWith(
      sessionId, slotId, -1, { text: '# Draft' }, undefined, 'checkpoint', 7, 3,
    );
    expect(draftStore.getLocalDraft(sessionId, slotId, listIndex)).toBeNull();
  });

  it('preserves the local draft when the server rejects its baseline', async () => {
    const patchSlotItemValue = vi.fn().mockRejectedValue(new Error('conflict'));
    useWorkflowStore.setState({ patchSlotItemValue });
    draftStore.setDraft(
      sessionId, slotId, listIndex, { text: '# Unsaved' }, -1, 7, 3,
    );

    await expect(draftStore.flushDraft(sessionId, slotId, listIndex, -1)).resolves.toBe(false);

    expect(draftStore.getLocalDraft(sessionId, slotId, listIndex)).toEqual({
      text: '# Unsaved',
    });
  });
});
