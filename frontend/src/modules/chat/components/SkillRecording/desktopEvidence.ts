import type { RecordingEvidence } from "./api";

export function desktopInputAvailable() {
  const bridge = window.lazymindDesktop;
  return Boolean(bridge?.recordingInputStart && bridge?.recordingInputStop && bridge?.recordingInputCancel && bridge?.recordingInputPermission);
}
export async function prepareDesktopInput() {
  if (!desktopInputAvailable()) return;
  const permission = await window.lazymindDesktop!.recordingInputPermission!();
  if (!permission.granted) throw new Error("recording-input-permission");
}
export async function collectDesktopEvidence(startedAt: number) {
  const bridge = window.lazymindDesktop!;
  const { session_id: id } = await bridge.recordingInputStart!(startedAt);
  let closed = false;
  return {
    async finish(): Promise<RecordingEvidence> {
      if (closed) throw new Error("recording-session-expired");
      closed = true;
      const endedSeconds = Math.max(0, (Date.now() - startedAt) / 1000);
      try {
        const result = await bridge.recordingInputStop!(id);
        return { ...result, events: result.events.filter((event) => event.seconds <= endedSeconds).sort((a, b) => a.seconds - b.seconds) };
      } finally { await bridge.recordingInputCancel!(id).catch(() => undefined); }
    },
    async cancel() {
      if (closed) return;
      closed = true;
      await bridge.recordingInputCancel!(id).catch(() => undefined);
    },
  };
}
