import type { RecordingEvidence, RecordingFrame } from './api';
export interface NativeRecordingResult { session_id: string; frames: RecordingFrame[]; evidence: RecordingEvidence }
export type NativeRecordingEvent = { event: 'tick'; session_id: string; seconds: number } | { event: 'ended'; result: NativeRecordingResult } | { event: 'failed'; session_id?: string; error: string };
export function nativeCaptureAvailable() { return Boolean(window.lazymindDesktop?.recordingNativeStart); }
export async function captureNative(onEnded: (result: NativeRecordingResult) => void, onTick: (seconds: number) => void, onError: (error: Error) => void) {
  const bridge = window.lazymindDesktop!;
  let closed = false, id: string | undefined;
  const unsubscribe = bridge.onRecordingNativeEvent!((message) => {
    if (closed) return;
    if (message.event === 'tick' && message.session_id === id) onTick(message.seconds);
    if (message.event === 'ended' && message.result.session_id === id) { closed = true; unsubscribe(); onEnded(message.result); }
    if (message.event === 'failed' && (!message.session_id || message.session_id === id)) { closed = true; unsubscribe(); onError(new Error(message.error)); }
  });
  try {
    const session = await bridge.recordingNativeStart!();
    if (closed) { await bridge.recordingNativeCancel!(); throw new Error('recording-helper-disconnected'); }
    id = session.session_id;
    return {
      startedAt: session.startedAt,
      stop: () => {
        if (closed) return; closed = true; unsubscribe();
        void bridge.recordingNativeStop!(session.session_id).then(onEnded).catch((error) => onError(error));
      },
      cancel: () => { if (closed) return; closed = true; unsubscribe(); void bridge.recordingNativeCancel!().catch(() => undefined); },
    };
  } catch (error) { closed = true; unsubscribe(); throw error; }
}
