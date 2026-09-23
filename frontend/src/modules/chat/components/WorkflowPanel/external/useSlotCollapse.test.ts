import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useSlotCollapse } from './useSlotCollapse';

describe('optional slot collapse', () => {
  it('collapses empty content and expands when output arrives', () => {
    const { result, rerender } = renderHook(({ present }) => useSlotCollapse(present, true), { initialProps: { present: false } });
    expect(result.current[0]).toBe(true);
    rerender({ present: true });
    expect(result.current[0]).toBe(false);
  });
  it('preserves an explicit expansion through empty refreshes', () => {
    const { result, rerender } = renderHook(() => useSlotCollapse(false, true));
    act(() => result.current[1]());
    rerender();
    expect(result.current[0]).toBe(false);
  });
  it('preserves an explicit collapse when new content arrives', () => {
    const { result, rerender } = renderHook(({ present }) => useSlotCollapse(present, true), { initialProps: { present: true } });
    act(() => result.current[1]());
    rerender({ present: false });
    rerender({ present: true });
    expect(result.current[0]).toBe(true);
  });
  it('leaves other empty slots expanded by default', () => {
    const { result } = renderHook(() => useSlotCollapse(false, false));
    expect(result.current[0]).toBe(false);
  });
});
