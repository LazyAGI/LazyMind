import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import {
  checkFFmpegDependency,
  getFFmpegDependencyStatus,
  installFFmpegDependency,
  updateFFmpegDependency,
} from "./systemDependencies";

vi.mock("@/components/request", () => ({
  BASE_URL: "http://core",
  axiosInstance: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPut = axiosInstance.put as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset();
  mockedPut.mockReset();
  mockedPost.mockReset();
});

describe("getFFmpegDependencyStatus", () => {
  it("unwraps an enveloped response", async () => {
    mockedGet.mockResolvedValue({ data: { data: { installed: true, source: "bundled" } } });
    const result = await getFFmpegDependencyStatus();
    expect(mockedGet).toHaveBeenCalledWith("http://core/api/core/system-dependencies/ffmpeg");
    expect(result).toEqual({ installed: true, source: "bundled" });
  });

  it("returns a raw (non-enveloped) payload unchanged", async () => {
    mockedGet.mockResolvedValue({ data: { installed: false, source: "system" } });
    const result = await getFFmpegDependencyStatus();
    expect(result).toEqual({ installed: false, source: "system" });
  });
});

describe("updateFFmpegDependency", () => {
  it("PUTs the custom path payload and returns the unwrapped status", async () => {
    mockedPut.mockResolvedValue({ data: { installed: true, source: "custom" } });
    const result = await updateFFmpegDependency({ source: "custom", customPath: "/usr/bin/ffmpeg" });
    expect(mockedPut).toHaveBeenCalledWith(
      "http://core/api/core/system-dependencies/ffmpeg",
      { source: "custom", customPath: "/usr/bin/ffmpeg" },
    );
    expect(result.source).toBe("custom");
  });
});

describe("checkFFmpegDependency / installFFmpegDependency", () => {
  it("posts to the check endpoint", async () => {
    mockedPost.mockResolvedValue({ data: { installed: false, source: "auto" } });
    await checkFFmpegDependency();
    expect(mockedPost).toHaveBeenCalledWith(
      "http://core/api/core/system-dependencies/ffmpeg:check",
    );
  });

  it("posts to the install endpoint with an extended timeout", async () => {
    mockedPost.mockResolvedValue({ data: { installed: true, source: "bundled" } });
    await installFFmpegDependency();
    expect(mockedPost).toHaveBeenCalledWith(
      "http://core/api/core/system-dependencies/ffmpeg:install",
      undefined,
      { timeout: 30 * 60 * 1000 },
    );
  });
});
