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

export function parseMediaCapabilityDependency(
  value: unknown,
): MediaCapabilityDependencyDetail | null {
  for (const candidate of candidateStrings(value)) {
    const payload = objectFromMarker(candidate);
    if (!payload) continue;
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
    };
  }
  return null;
}
