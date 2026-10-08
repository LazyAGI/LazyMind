import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useSlotFooterActions } from './useSlotFooterActions';

describe('document footer action registration', () => {
  it('does not let stale cleanup remove a replacement registration', () => {
    const { result } = renderHook(useSlotFooterActions);
    const oldAction = { label: 'Old copy', onClick: vi.fn() };
    const newAction = { label: 'New copy', onClick: vi.fn() };
    let unregisterOld!: () => void;
    let unregisterNew!: () => void;
    act(() => { unregisterOld = result.current.registerFooterAction('copy', oldAction); });
    act(() => { unregisterNew = result.current.registerFooterAction('copy', newAction); });
    act(() => unregisterOld());
    expect(result.current.footerActions.get('copy')).toBe(newAction);
    act(() => unregisterNew());
    act(() => unregisterNew());
    expect(result.current.footerActions.size).toBe(0);
  });

  it('supports explicit removal without letting its cleanup remove a later action', () => {
    const { result } = renderHook(useSlotFooterActions);
    const action = { label: 'Copy', onClick: vi.fn() };
    let unregisterRemoval!: () => void;
    act(() => { result.current.registerFooterAction('copy', action); });
    act(() => { unregisterRemoval = result.current.registerFooterAction('copy', null); });
    expect(result.current.footerActions.size).toBe(0);
    act(() => { result.current.registerFooterAction('copy', action); });
    act(() => unregisterRemoval());
    expect(result.current.footerActions.get('copy')).toBe(action);
  });

  it('keeps scopes independent and preserves new registrations after a session reset', () => {
    const parent = renderHook(useSlotFooterActions);
    const inline = renderHook(useSlotFooterActions);
    const action = { label: 'Copy', onClick: vi.fn() };
    const replacement = { label: 'New session copy', onClick: vi.fn() };
    let unregisterOld!: () => void;
    const register = parent.result.current.registerFooterAction;
    act(() => { unregisterOld = register('copy', action); });
    act(() => { inline.result.current.registerFooterAction('copy', action); });
    act(() => parent.result.current.clearFooterActions());
    expect(parent.result.current.footerActions.size).toBe(0);
    expect(inline.result.current.footerActions.get('copy')).toBe(action);
    expect(parent.result.current.registerFooterAction).toBe(register);
    act(() => { register('copy', replacement); });
    act(() => unregisterOld());
    expect(parent.result.current.footerActions.get('copy')).toBe(replacement);
  });
});
