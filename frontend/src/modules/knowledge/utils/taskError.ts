const FFMPEG_MISSING_PATTERN =
  /(?:ffmpeg|ffprobe).*(?:not found|not installed|missing|not on path)/i;

export function isFFmpegDependencyError(error: unknown): boolean {
  const value = String(error ?? "").trim();
  return value === "2000731" || FFMPEG_MISSING_PATTERN.test(value);
}
