import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useWorkflowTabNavigation } from './useWorkflowTabNavigation';

const tabs = [{ id: 'analysis', step_id: 'analyze' }, { id: 'slides', step_id: 'generate' }];
const defaults = { tabs, sessionId: 'session', terminal: false, persist: vi.fn() };
describe('workflow viewing position', () => {
  it('follows execution until the user starts reading, then stays on the viewed step', () => {
    const { result, rerender } = renderHook(({ step }) => useWorkflowTabNavigation({ ...defaults, currentStepId: step }), { initialProps: { step: 'analyze' } });
    expect(result.current.index).toBe(0);
    act(() => result.current.preserve());
    rerender({ step: 'generate' });
    expect(result.current.index).toBe(0);
    act(() => result.current.select('slides'));
    expect(result.current.index).toBe(1);
  });
  it('follows runtime before any reading gesture', () => {
    const { result, rerender } = renderHook(({ step }) => useWorkflowTabNavigation({ ...defaults, currentStepId: step }), { initialProps: { step: 'analyze' } });
    rerender({ step: 'generate' });
    expect(result.current.index).toBe(1);
  });
  it('keeps the same tab when an earlier tab disappears', () => {
    const { result, rerender } = renderHook(({ visible }) => useWorkflowTabNavigation({ ...defaults, tabs: visible }), { initialProps: { visible: tabs } });
    act(() => result.current.select('slides'));
    rerender({ visible: [tabs[1]] });
    expect(result.current.index).toBe(0);
  });
  it('falls back to the current step when the selected tab is removed', () => {
    const { result, rerender } = renderHook(({ visible }) => useWorkflowTabNavigation({ ...defaults, tabs: visible, currentStepId: 'generate' }), { initialProps: { visible: tabs } });
    act(() => result.current.select('analysis'));
    rerender({ visible: [tabs[1]] });
    expect(result.current.index).toBe(0);
  });
  it('restores persisted focus without following a later execution', () => {
    const { result } = renderHook(() => useWorkflowTabNavigation({ ...defaults, currentStepId: 'generate', persistedTab: 'analysis' }));
    expect(result.current.index).toBe(0);
  });
});
