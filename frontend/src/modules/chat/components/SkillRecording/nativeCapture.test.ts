import { afterEach, expect, it, vi } from 'vitest';
import { captureNative, type NativeRecordingEvent } from './nativeCapture';
afterEach(() => { delete window.lazymindDesktop; });
function setup() {
  let listener: (event: NativeRecordingEvent) => void = () => {};
  const remove = vi.fn(), cancel = vi.fn().mockResolvedValue(undefined);
  const result = { session_id: 'one', frames: [{ image: 'frame', seconds: 1 }], evidence: { events: [], limitations: [] } };
  window.lazymindDesktop = { recordingNativeStart: vi.fn().mockResolvedValue({ session_id: 'one', startedAt: 1 }), recordingNativeStop: vi.fn().mockResolvedValue(result), recordingNativeCancel: cancel, onRecordingNativeEvent: fn => { listener = fn; return remove; } } as typeof window.lazymindDesktop;
  return { emit: (e: NativeRecordingEvent) => listener(e), result, cancel, remove };
}
it('combines native frames and actions once, ignoring events from other sessions', async () => {
  const app = setup(), ended = vi.fn(), tick = vi.fn();
  const capture = await captureNative(ended, tick, vi.fn());
  app.emit({ event: 'tick', session_id: 'old', seconds: 9 }); expect(tick).not.toHaveBeenCalled();
  app.emit({ event: 'tick', session_id: 'one', seconds: 2 }); expect(tick).toHaveBeenCalledWith(2);
  capture.stop(); capture.stop(); await vi.waitFor(() => expect(ended).toHaveBeenCalledOnce());
  expect(ended).toHaveBeenCalledWith(app.result); expect(app.remove).toHaveBeenCalledOnce();
});
it('cancellation releases the helper without uploading', async () => {
  const app = setup(), ended = vi.fn(); const capture = await captureNative(ended, vi.fn(), vi.fn()); capture.cancel();
  app.emit({ event: 'ended', result: app.result }); expect(ended).not.toHaveBeenCalled(); expect(app.cancel).toHaveBeenCalledOnce();
});

it('does not return a live controller after the helper fails during startup', async () => {
  const app = setup();
  let finish!: (value: { session_id: string; startedAt: number }) => void;
  window.lazymindDesktop!.recordingNativeStart = vi.fn(() => new Promise(resolve => { finish = resolve; }));
  const capture = captureNative(vi.fn(), vi.fn(), vi.fn());
  const rejected = expect(capture).rejects.toThrow('recording-helper-disconnected');
  app.emit({ event: 'failed', error: 'recording-helper-disconnected' });
  finish({ session_id: 'one', startedAt: 1 });
  await rejected;
  expect(app.cancel).toHaveBeenCalledOnce();
});
