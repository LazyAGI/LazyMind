export function formatDateTime(value?: string) {
  if (!value) {
    return "-";
  }

  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  const year = parsed.getFullYear();
  const month = `${parsed.getMonth() + 1}`.padStart(2, "0");
  const day = `${parsed.getDate()}`.padStart(2, "0");
  const hour = `${parsed.getHours()}`.padStart(2, "0");
  const minute = `${parsed.getMinutes()}`.padStart(2, "0");
  return `${year}-${month}-${day} ${hour}:${minute}`;
}

export function formatBytes(bytes?: number) {
  if (!bytes || bytes < 0) {
    return "0 B";
  }

  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }

  return `${value.toFixed(value >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

export function resolveStorageUsed(
  summary?: Record<string, any>,
  fallback?: string,
) {
  const bytes =
    summary?.storage_bytes ??
    summary?.storageBytes ??
    summary?.storage_used_bytes ??
    summary?.storageUsedBytes;

  if (typeof bytes === "number") {
    return formatBytes(bytes);
  }

  const parsedBytes =
    typeof bytes === "string" && bytes.trim() ? Number(bytes) : Number.NaN;
  if (Number.isFinite(parsedBytes)) {
    return formatBytes(parsedBytes);
  }

  return fallback || "0 B";
}

export function resolveParsedDocumentCount(
  summary?: Record<string, any>,
  fallback = 0,
) {
  const value =
    summary?.parsed_document_count ??
    summary?.parsedDocumentCount;
  const parsed =
    typeof value === "number"
      ? value
      : typeof value === "string" && value.trim()
        ? Number(value)
        : Number.NaN;

  if (Number.isFinite(parsed)) {
    return Math.max(0, Math.trunc(parsed));
  }
  return fallback;
}
