export const MEDIA_CAPABILITY_DEPENDENCY_MISSING =
  "MEDIA_CAPABILITY_DEPENDENCY_MISSING";

export interface MissingMediaCapability {
  id: string;
  label: string;
  available: false;
  settings_url: string;
  reason: string;
}

export interface MediaCapabilityDependencyDetail {
  status: "blocked";
  workflow: string;
  required: string[];
  missing: MissingMediaCapability[];
  message: string;
  failure_id?: string;
  conversation_id?: string;
}

const MODEL_CAPABILITY_TARGETS: Record<string, string> = {
  image_generator: "image_generator",
  image_editor: "image_editor",
  video_generator: "video_generator",
};

export function buildCapabilitySettingsUrl(
  capability: Pick<MissingMediaCapability, "id" | "settings_url">,
  returnTo?: string,
): string {
  const url = new URL(capability.settings_url, window.location.origin);
  if (
    url.pathname === "/settings" &&
    url.searchParams.get("section") === "models" &&
    !url.searchParams.has("target")
  ) {
    const target = MODEL_CAPABILITY_TARGETS[capability.id];
    if (target) url.searchParams.set("target", target);
  }
  if (returnTo?.startsWith("/") && !returnTo.startsWith("//")) {
    url.searchParams.set("return_to", returnTo);
  }
  return `${url.pathname}${url.search}${url.hash}`;
}

export function mediaCapabilityDependencySignature(
  detail: MediaCapabilityDependencyDetail,
): string {
  if (detail.failure_id) return detail.failure_id;
  return [
    detail.conversation_id || "",
    detail.workflow,
    ...detail.missing.map((item) => item.id).sort(),
  ].join("|");
}

function objectFromMarker(text: string): Record<string, unknown> | null {
  const markerIndex = text.indexOf(MEDIA_CAPABILITY_DEPENDENCY_MISSING);
  if (markerIndex < 0) return null;
  const tail = text.slice(markerIndex + MEDIA_CAPABILITY_DEPENDENCY_MISSING.length);
  const start = tail.indexOf("{");
  if (start < 0) return null;

  let depth = 0;
  let quoted = false;
  let escaped = false;
  for (let index = start; index < tail.length; index += 1) {
    const char = tail[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (char === "\\") {
      escaped = true;
      continue;
    }
    if (char === '"') {
      quoted = !quoted;
      continue;
    }
    if (quoted) continue;
    if (char === "{") depth += 1;
    if (char !== "}") continue;
    depth -= 1;
    if (depth !== 0) continue;
    try {
      const value = JSON.parse(tail.slice(start, index + 1));
      return value && typeof value === "object"
        ? (value as Record<string, unknown>)
        : null;
    } catch {
      return null;
    }
  }
  return null;
}

function candidateStrings(value: unknown, depth = 0): string[] {
  if (depth > 5 || value == null) return [];
  if (typeof value === "string") {
    const values = [value];
    try {
      const parsed = JSON.parse(value);
      if (parsed !== value) values.push(...candidateStrings(parsed, depth + 1));
    } catch {
      // Plain tool errors are expected here.
    }
    return values;
  }
  if (Array.isArray(value)) {
    return value.flatMap((item) => candidateStrings(item, depth + 1));
  }
  if (typeof value === "object") {
    return Object.values(value).flatMap((item) => candidateStrings(item, depth + 1));
  }
  return [];
}

function candidatePayloads(
  value: unknown,
  depth = 0,
): Array<Record<string, unknown>> {
  if (depth > 5 || value == null || typeof value !== "object") return [];
  if (Array.isArray(value)) {
    return value.flatMap((item) => candidatePayloads(item, depth + 1));
  }
  const payload = value as Record<string, unknown>;
  const direct = payload.status === "blocked" && Array.isArray(payload.missing)
    ? [payload]
    : [];
  return [
    ...direct,
    ...Object.values(payload).flatMap((item) => candidatePayloads(item, depth + 1)),
  ];
}

function detailFromPayload(
  payload: Record<string, unknown>,
): MediaCapabilityDependencyDetail | null {
  const rawMissing = Array.isArray(payload.missing) ? payload.missing : [];
  const missing = rawMissing.flatMap((item): MissingMediaCapability[] => {
    if (!item || typeof item !== "object") return [];
    const row = item as Record<string, unknown>;
    const id = String(row.id || "").trim();
    const label = String(row.label || "").trim();
    const settingsUrl = String(row.settings_url || "").trim();
    const reason = String(row.reason || "").trim();
    const safeSettingsUrl =
      settingsUrl === "/settings" ||
      settingsUrl.startsWith("/settings?") ||
      settingsUrl.startsWith("/settings#");
    if (!id || !label || !safeSettingsUrl) {
      return [];
    }
    return [{ id, label, available: false, settings_url: settingsUrl, reason }];
  });
  if (missing.length === 0) return null;
  return {
    status: "blocked",
    workflow: String(payload.workflow || ""),
    required: Array.isArray(payload.required)
      ? payload.required.map((item) => String(item))
      : [],
    missing,
    message: String(payload.message || ""),
    failure_id: typeof payload.failure_id === "string"
      ? payload.failure_id
      : undefined,
    conversation_id: typeof payload.conversation_id === "string"
      ? payload.conversation_id
      : undefined,
  };
}

export function parseMediaCapabilityDependency(
  value: unknown,
): MediaCapabilityDependencyDetail | null {
  for (const payload of candidatePayloads(value)) {
    const detail = detailFromPayload(payload);
    if (detail) return detail;
  }
  for (const candidate of candidateStrings(value)) {
    const payload = objectFromMarker(candidate);
    if (!payload) continue;
    const detail = detailFromPayload(payload);
    if (detail) return detail;
  }
  return null;
}
