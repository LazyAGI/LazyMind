export interface DesktopUpdateState {
  revision: number;
  phase: "idle" | "checking" | "disabled" | "available" | "up_to_date" | "blocked" | "downloading" | "cancelling" | "downloaded" | "error";
  currentVersion: string;
  reason?: string;
  channel?: string;
  releaseId?: string;
  nextVersion?: string;
  targetVersion?: string;
  route?: string[];
  downloadedBytes?: number;
  totalBytes?: number;
  canInstall: boolean;
}

type UpdateBridge = {
  updateStatus?: () => Promise<unknown>;
  updateCheck?: () => Promise<unknown>;
  updateDownload?: (releaseId: string) => Promise<unknown>;
  updateCancel?: () => Promise<unknown>;
  updateReveal?: () => Promise<unknown>;
  onUpdateState?: (listener: (state: unknown) => void) => (() => void);
};

const phases = new Set(["idle", "checking", "disabled", "available", "up_to_date", "blocked", "downloading", "cancelling", "downloaded", "error"]);
function bridge(): UpdateBridge | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as Window & { lazymindDesktop?: UpdateBridge }).lazymindDesktop;
}
function state(value: unknown): DesktopUpdateState {
  const candidate = value as Partial<DesktopUpdateState> | null;
  if (!candidate || !Number.isSafeInteger(candidate.revision) || (candidate.revision as number) < 0
    || !phases.has(candidate.phase || "") || typeof candidate.currentVersion !== "string" || typeof candidate.canInstall !== "boolean") throw new Error("desktop_update_invalid_state");
  return candidate as DesktopUpdateState;
}
export async function desktopUpdateStatus(): Promise<DesktopUpdateState | null> {
  const api = bridge(); return api?.updateStatus ? state(await api.updateStatus()) : null;
}
export async function desktopUpdateCheck(): Promise<DesktopUpdateState> {
  const api = bridge(); if (!api?.updateCheck) throw new Error("desktop_update_unavailable");
  return state(await api.updateCheck());
}
export async function desktopUpdateDownload(releaseId: string): Promise<DesktopUpdateState> {
  const api = bridge(); if (!api?.updateDownload) throw new Error("desktop_update_unavailable");
  return state(await api.updateDownload(releaseId));
}
export async function desktopUpdateCancel(): Promise<DesktopUpdateState> {
  const api = bridge(); if (!api?.updateCancel) throw new Error("desktop_update_unavailable");
  return state(await api.updateCancel());
}
export async function desktopUpdateReveal(): Promise<void> {
  const api = bridge(); if (!api?.updateReveal) throw new Error("desktop_update_unavailable");
  await api.updateReveal();
}
export function onDesktopUpdateState(listener: (state: DesktopUpdateState) => void): () => void {
  return bridge()?.onUpdateState?.((value) => { try { listener(state(value)); } catch { /* Ignore malformed events; a later explicit status read can retry. */ } }) || (() => {});
}
