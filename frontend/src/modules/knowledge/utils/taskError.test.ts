import { describe, expect, it } from "vitest";
import { isFFmpegDependencyError } from "./taskError";

describe("isFFmpegDependencyError", () => {
  it("returns true for the well-known ffmpeg error code", () => {
    expect(isFFmpegDependencyError("2000731")).toBe(true);
  });

  it("returns true for messages mentioning ffmpeg/ffprobe not found", () => {
    expect(isFFmpegDependencyError("ffmpeg not found")).toBe(true);
    expect(isFFmpegDependencyError("FFPROBE is not installed on this machine")).toBe(true);
    expect(isFFmpegDependencyError("ffmpeg missing")).toBe(true);
    expect(isFFmpegDependencyError("ffprobe not on path")).toBe(true);
  });

  it("returns false for unrelated errors", () => {
    expect(isFFmpegDependencyError("network timeout")).toBe(false);
    expect(isFFmpegDependencyError("2000509")).toBe(false);
  });

  it("handles null/undefined/Error objects gracefully", () => {
    expect(isFFmpegDependencyError(undefined)).toBe(false);
    expect(isFFmpegDependencyError(null)).toBe(false);
    expect(isFFmpegDependencyError(new Error("ffmpeg not found"))).toBe(true);
  });
});
