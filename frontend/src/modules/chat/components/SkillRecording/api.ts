import { axiosInstance, BASE_URL } from "@/components/request";
export interface RecordingFrame { image: string; seconds: number }
export interface SkillRecording {
  id: string; conversation_id: string; skill_id: string;
  status: "generating" | "needs_input" | "failed" | "pending" | "kept" | "discarded";
  name: string; description: string; error: string;
}
const url = `${BASE_URL}/api/core/skill-recordings`;
function unwrap<T>(body: T | { code?: number; message?: string; data: T }): T {
  if (body && typeof body === "object" && "data" in body) {
    if (body.code) throw new Error(body.message || "Request failed");
    return body.data;
  }
  return body as T;
}
export async function listRecordings(filter: { conversation_id?: string; skill_id?: string }) {
  return unwrap<{ items: SkillRecording[] }>((await axiosInstance.get(url, { params: filter })).data).items || [];
}
export async function submitRecording(payload: { id?: string; conversation_id?: string; frames?: RecordingFrame[]; evidence?: RecordingEvidence; notes?: string }) {
  return unwrap<SkillRecording>((await axiosInstance.post(url, payload)).data);
}
export async function decideRecording(id: string, keep: boolean) {
  unwrap((await axiosInstance.post(`${url}/decision`, { id, keep })).data);
  window.dispatchEvent(new Event("skill-recording-updated"));
}

export async function recordingSetup(install = false) {
  const response = install ? await axiosInstance.post(`${url}/setup`) : await axiosInstance.get(`${url}/setup`);
  return unwrap<{ installed: boolean; skill_id?: string }>(response.data);
}

export interface RecordingEvidence {
  events: Array<{ seconds: number; kind: "click" | "keydown" | "keyup" | "mousedown" | "mouseup" | "mousemove" | "wheel" | "input" | "dom"; [key: string]: unknown }>;
  limitations: string[];
}
export interface RecordingTarget { device_id: string; tab_id: string; title: string; origin: string }
export async function recordingBrowser<T>(action: string, target: { device_id: string; tab_id?: string; session_id?: string; started_at?: number }) {
  return unwrap<T>((await axiosInstance.post(`${url}/browser`, { action, ...target })).data);
}
export async function recordingTargets(): Promise<RecordingTarget[]> {
  const devices = unwrap<{ devices: Array<{ id: string; online: boolean }> }>((await axiosInstance.get(`${BASE_URL}/api/core/browser/manage/devices`)).data).devices;
  const results = await Promise.allSettled(devices.filter((device) => device.online).map(async (device) => {
    const result = await recordingBrowser<{ targets: Omit<RecordingTarget, "device_id">[] }>("targets", { device_id: device.id });
    return result.targets.map((target) => ({ ...target, device_id: device.id }));
  }));
  return results.flatMap((result) => result.status === "fulfilled" ? result.value : []);
}
