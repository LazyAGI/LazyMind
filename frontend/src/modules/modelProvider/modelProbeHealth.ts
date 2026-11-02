import type { ModelProbeResult } from "./modelProbe";

const records = new Map<string, { configurationKey: string; result: ModelProbeResult }>();
const listeners = new Set<() => void>();
let revision = 0;

function emit() {
  revision += 1;
  listeners.forEach((listener) => listener());
}

/** Session health shared by the model list and default selectors. Search filters must not drop it. */
export function subscribeProbeHealth(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function probeHealthVersion() {
  return revision;
}

export function readModelProbeHealth(modelId: string) {
  return records.get(modelId)?.result;
}

export function takeProbeHealth(modelId: string, configurationKey: string) {
  const entry = records.get(modelId);
  if (!entry) return undefined;
  if (entry.configurationKey === configurationKey) return entry.result;
  records.delete(modelId);
  emit();
  return undefined;
}

export function saveProbeHealth(modelId: string, configurationKey: string, result: ModelProbeResult) {
  records.set(modelId, { configurationKey, result });
  emit();
}

export function resetProbeHealthForTests() {
  records.clear();
  emit();
}
