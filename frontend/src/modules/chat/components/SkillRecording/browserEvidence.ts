import { recordingBrowser, type RecordingEvidence, type RecordingTarget } from "./api";

export async function collectBrowserEvidence(target: RecordingTarget, startedAt: number) {
  const session = await recordingBrowser<{ session_id: string; limitations?: string[] }>("start", { ...target, started_at: startedAt });
  const request = { device_id: target.device_id, session_id: session.session_id };
  const evidence: RecordingEvidence = { events: [], limitations: session.limitations || [] };
  let closed = false;
  let bytes = 0;
  let pending: Promise<void> | null = null;
  const limit = (reason: string) => { if (!evidence.limitations.includes(reason)) evidence.limitations.push(reason); };
  const read = async (action: "read" | "stop") => {
    try {
      const result = await recordingBrowser<{ events: RecordingEvidence["events"]; gaps?: boolean }>(action, request);
      if (result.gaps) limit("events_or_dom_missing_during_navigation");
      for (const event of result.events || []) {
        const size = new TextEncoder().encode(JSON.stringify(event)).length;
        if (evidence.events.length >= 1500 || bytes + size > 1500000) { limit("evidence_size_limit"); break; }
        evidence.events.push(event); bytes += size;
      }
    } catch { limit("browser_disconnected_or_page_permission_missing"); }
  };
  const timer = setInterval(() => {
    if (closed || pending) return;
    pending = read("read").finally(() => { pending = null; });
  }, 500);
  return {
    async finish(): Promise<RecordingEvidence> {
      if (!closed) {
        const endedSeconds = Math.max(0, (Date.now() - startedAt) / 1000);
        closed = true; clearInterval(timer);
        const stopping = read("stop");
        await pending; await stopping;
        evidence.events = evidence.events.filter((event) => event.seconds <= endedSeconds);
      }
      evidence.events.sort((a, b) => a.seconds - b.seconds);
      return evidence;
    },
    async cancel() {
      if (closed) return;
      closed = true; clearInterval(timer);
      const stopping = recordingBrowser("cancel", request).catch(() => undefined);
      await pending; await stopping;
      evidence.events.length = 0;
    },
  };
}
