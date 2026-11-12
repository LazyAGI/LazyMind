import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useSkillImportTask } from './useSkillImportTask';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe('skill import lifetime', () => {
  it('cancels the active request and ignores its late result without disturbing a retry', async () => {
    const old = deferred<string>();
    const retry = deferred<string>();
    const onSuccess = vi.fn();
    const onError = vi.fn();
    const { result } = renderHook(() => useSkillImportTask({ onSuccess, onError }));
    let oldSignal!: AbortSignal;
    let first!: Promise<void>;
    act(() => { first = result.current.start(({ signal, setUploadProgress }) => {
      oldSignal = signal;
      setUploadProgress(25);
      return old.promise;
    }); });
    expect(result.current.pending).toBe(true);
    expect(result.current.uploadProgress).toBe(25);
    act(() => result.current.cancel());
    expect(oldSignal.aborted).toBe(true);
    expect(result.current.pending).toBe(false);
    let second!: Promise<void>;
    act(() => { second = result.current.start(() => retry.promise); });
    await act(async () => { old.resolve('old'); await first; });
    expect(onSuccess).not.toHaveBeenCalled();
    expect(result.current.pending).toBe(true);
    await act(async () => { retry.resolve('new'); await second; });
    expect(onSuccess).toHaveBeenCalledWith('new');
    expect(onError).not.toHaveBeenCalled();
    expect(result.current.pending).toBe(false);
  });

  it('uses the latest success callback after filters change during import', async () => {
    const mutation = deferred<string>();
    const refreshedFilters: string[] = [];
    const { result, rerender } = renderHook(({ filter }) => useSkillImportTask({
      onSuccess: () => refreshedFilters.push(filter), onError: vi.fn(),
    }), { initialProps: { filter: 'old' } });
    let request!: Promise<void>;
    act(() => { request = result.current.start(() => mutation.promise); });
    rerender({ filter: 'current' });
    await act(async () => { mutation.resolve('skill'); await request; });
    expect(refreshedFilters).toEqual(['current']);
  });

  it('aborts on unmount and does not notify after completion', async () => {
    const mutation = deferred<string>();
    const onSuccess = vi.fn();
    const { result, unmount } = renderHook(() => useSkillImportTask({ onSuccess, onError: vi.fn() }));
    let signal!: AbortSignal;
    let request!: Promise<void>;
    act(() => { request = result.current.start(context => { signal = context.signal; return mutation.promise; }); });
    unmount();
    expect(signal.aborted).toBe(true);
    mutation.resolve('skill');
    await request;
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('prevents duplicate submissions before pending state has rendered', async () => {
    const mutation = deferred<string>();
    const execute = vi.fn(() => mutation.promise);
    const { result } = renderHook(() => useSkillImportTask({ onSuccess: vi.fn(), onError: vi.fn() }));
    let request!: Promise<void>;
    act(() => { request = result.current.start(execute); void result.current.start(execute); });
    expect(execute).toHaveBeenCalledTimes(1);
    await act(async () => { mutation.resolve('skill'); await request; });
  });
});
