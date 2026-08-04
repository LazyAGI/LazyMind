import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { uploadSkillTempFile } from "./skillUpload";

vi.mock("@/components/request", () => ({
  axiosInstance: { post: vi.fn(), put: vi.fn() },
}));

vi.mock("@/runtime/apiBase", () => ({
  coreApiUrl: (path: string) => `/api/core/${path}`,
}));

const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedPut = axiosInstance.put as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedPost.mockReset();
  mockedPut.mockReset();
});

const buildFile = (content: string, name = "skill.md") =>
  new File([content], name, { type: "text/markdown" });

describe("uploadSkillTempFile", () => {
  it("uploads a single-part file and returns the stored path/url", async () => {
    mockedPost
      .mockResolvedValueOnce({ data: { upload_id: "up-1", part_size: 1024, total_parts: 1 } })
      .mockResolvedValueOnce({ data: { stored_path: "/tmp/skill.md", file_url: "http://file" } });
    mockedPut.mockResolvedValue({ data: {} });

    const file = buildFile("small content");
    const result = await uploadSkillTempFile(file);

    expect(mockedPost).toHaveBeenNthCalledWith(
      1,
      "/api/core/temp/uploads:initUpload",
      expect.objectContaining({ filename: "skill.md", file_size: file.size }),
      expect.any(Object),
    );
    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(result).toEqual({
      uploadId: "up-1",
      storedPath: "/tmp/skill.md",
      fileUrl: "http://file",
    });
  });

  it("splits the file into multiple parts based on chunkSize", async () => {
    mockedPost
      .mockResolvedValueOnce({ data: { upload_id: "up-2", part_size: 10, total_parts: 3 } })
      .mockResolvedValueOnce({ data: { stored_path: "/tmp/big.md" } });
    mockedPut.mockResolvedValue({ data: {} });

    const file = buildFile("x".repeat(25), "big.md");
    await uploadSkillTempFile(file, { chunkSize: 10 });

    expect(mockedPut).toHaveBeenCalledTimes(3);
    expect(mockedPut).toHaveBeenNthCalledWith(
      1,
      "/api/core/temp/uploads/up-2/parts/1",
      expect.anything(),
      expect.any(Object),
    );
  });

  it("throws when the init response is missing an upload id", async () => {
    mockedPost.mockResolvedValueOnce({ data: {} });

    await expect(uploadSkillTempFile(buildFile("content"))).rejects.toThrow(
      "Missing upload_id from temp upload init",
    );
  });

  it("throws when the complete response is missing a stored path", async () => {
    mockedPost
      .mockResolvedValueOnce({ data: { upload_id: "up-3", part_size: 1024, total_parts: 1 } })
      .mockResolvedValueOnce({ data: {} });
    mockedPut.mockResolvedValue({ data: {} });

    await expect(uploadSkillTempFile(buildFile("content"))).rejects.toThrow(
      "Missing stored_path from temp upload complete",
    );
  });
});
