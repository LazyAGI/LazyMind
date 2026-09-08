import { describe, expect, it } from "vitest";
import { parseMediaCapabilityDependency } from "./mediaCapabilityDependency";

const payload = {
  status: "blocked",
  workflow: "CREATE_ANIMATED",
  required: ["image_generator", "video_generator", "ffmpeg"],
  missing: [
    {
      id: "video_generator",
      label: "视频生成模型",
      available: false,
      settings_url: "/settings?section=models",
      reason: "未配置视频生成模型。",
    },
    {
      id: "ffmpeg",
      label: "FFmpeg",
      available: false,
      settings_url: "/settings?section=system_tools#ffmpeg-dependency",
      reason: "未检测到 FFmpeg/FFprobe。",
    },
  ],
  message: "当前任务缺少：视频生成模型、FFmpeg。",
};

describe("parseMediaCapabilityDependency", () => {
  it("reads the structured workflow tool failure", () => {
    const detail = parseMediaCapabilityDependency(
      `MEDIA_CAPABILITY_DEPENDENCY_MISSING ${JSON.stringify(payload)}`,
    );
    expect(detail?.workflow).toBe("CREATE_ANIMATED");
    expect(detail?.missing.map((item) => item.id)).toEqual([
      "video_generator",
      "ffmpeg",
    ]);
    expect(detail?.missing[1]?.settings_url).toContain("ffmpeg-dependency");
  });

  it("finds a marker nested in a serialized tool result", () => {
    const nested = {
      ok: false,
      value: JSON.stringify({
        error: `MEDIA_CAPABILITY_DEPENDENCY_MISSING ${JSON.stringify(payload)}`,
      }),
    };
    expect(parseMediaCapabilityDependency(nested)?.missing).toHaveLength(2);
  });

  it("ignores malformed or unsafe jump targets", () => {
    const invalid = {
      ...payload,
      missing: [{ ...payload.missing[0], settings_url: "https://example.com" }],
    };
    expect(
      parseMediaCapabilityDependency(
        `MEDIA_CAPABILITY_DEPENDENCY_MISSING ${JSON.stringify(invalid)}`,
      ),
    ).toBeNull();
  });
});
