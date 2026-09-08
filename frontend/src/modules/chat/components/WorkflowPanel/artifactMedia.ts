const VIDEO_FILE_PATTERN = /\.(?:mp4|m4v|mov|webm|ogv)(?:[?#].*)?$/i;

export function isVideoArtifactValue(raw: Record<string, unknown> | undefined): boolean {
  if (!raw) return false;
  const mediaType = String(
    raw.mime_type ?? raw.mimeType ?? raw.content_type ?? raw.type ?? '',
  ).trim().toLowerCase();
  if (mediaType.startsWith('video/')) return true;

  return [raw.filename, raw.name, raw.url, raw.path]
    .some((candidate) => VIDEO_FILE_PATTERN.test(String(candidate ?? '').trim()));
}
