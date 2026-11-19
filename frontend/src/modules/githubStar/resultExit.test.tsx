import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useArtifactPreviewController } from '@/modules/chat/components/ArtifactPreview/context';
import { GITHUB_STAR_OPPORTUNITY_EVENT } from './api';

const legacyTarget = { key: 'final-report', conversationId: 'conversation-1', filename: 'report.md',
  content: 'Final work result', starOpportunity: { task_id: 'completed-root', conversation_id: 'conversation-1',
    status: 'succeeded', result_ready: true, source: 'final_result' } };
afterEach(cleanup);

it('closing a final preview never creates a Work completion opportunity, including legacy target data', () => {
  const received = vi.fn(), closed = vi.fn();
  window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    const hook = renderHook(() => useArtifactPreviewController('conversation-1'));
    act(() => hook.result.current.open(legacyTarget, { onClose: closed }));
    expect(received).not.toHaveBeenCalled();
    expect(hook.result.current.target?.key).toBe('final-report');
    act(() => hook.result.current.close());
    expect(hook.result.current.target).toBeNull();
    expect(closed).toHaveBeenCalledTimes(1);
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('ordinary files and stage results do not produce a Work completion opportunity', () => {
  const received = vi.fn();
  window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    const hook = renderHook(() => useArtifactPreviewController('conversation-1'));
    act(() => hook.result.current.open({ key: 'stage', filename: 'draft.md', content: 'draft' }));
    act(() => hook.result.current.close());
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('changing conversations cannot promote an abandoned preview to a Work completion', () => {
  const received = vi.fn();
  window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    const hook = renderHook(({ id }) => useArtifactPreviewController(id), { initialProps: { id: 'conversation-1' } });
    act(() => hook.result.current.open(legacyTarget));
    hook.rerender({ id: 'conversation-2' });
    act(() => hook.result.current.close());
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});
