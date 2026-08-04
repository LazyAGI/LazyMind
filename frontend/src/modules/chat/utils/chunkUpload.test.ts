import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  axiosInstance: {
    post: vi.fn(),
    put: vi.fn(),
  },
}));

vi.mock("@/i18n", () => ({
  default: {
    t: (key: string) => key,
  },
}));

vi.mock("@/runtime/apiBase", () => ({
  coreApiUrl: (path: string) => `/api/core/${path}`,
}));

import { axiosInstance } from "@/components/request";
import { abortUpload, uploadFileInChunks } from "./chunkUpload";

const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedPut = axiosInstance.put as unknown as ReturnType<typeof vi.fn>;

function makeFile(size: number, name = "big.bin"): File {
  const content = new Uint8Array(size);
  return new File([content], name, { type: "application/octet-stream" });
}

describe("uploadFileInChunks", () => {
  beforeEach(() => {
    mockedPost.mockReset();
    mockedPut.mockReset();
  });

  it("uploads all parts sequentially and returns the stored path", async () => {
    mockedPost.mockImplementation((url: string) => {
      if (url.includes("initUpload")) {
        return Promise.resolve({
          data: { upload_id: "up-1", part_size: 10, total_parts: 2 },
        });
      }
      if (url.includes(":complete")) {
        return Promise.resolve({ data: { stored_path: "/tmp/stored.bin" } });
      }
      return Promise.reject(new Error(`unexpected post ${url}`));
    });
    mockedPut.mockResolvedValue({ data: {} });

    const onProgress = vi.fn();
    const result = await uploadFileInChunks(makeFile(20), {
      chunkSize: 10,
      onProgress,
    });

    expect(result).toBe("/tmp/stored.bin");
    expect(mockedPut).toHaveBeenCalledTimes(2);
    expect(onProgress).toHaveBeenCalledTimes(2);
    expect(onProgress).toHaveBeenLastCalledWith({
      uploadedParts: 2,
      totalParts: 2,
      uploadedBytes: 20,
      totalBytes: 20,
      percentage: 100,
    });
  });

  it("falls back to the requested chunkSize and computed total parts when init response omits them", async () => {
    mockedPost.mockImplementation((url: string) => {
      if (url.includes("initUpload")) {
        return Promise.resolve({ data: { upload_id: "up-2" } });
      }
      if (url.includes(":complete")) {
        return Promise.resolve({ data: { stored_path: "/tmp/stored2.bin" } });
      }
      return Promise.reject(new Error(`unexpected post ${url}`));
    });
    mockedPut.mockResolvedValue({ data: {} });

    const result = await uploadFileInChunks(makeFile(25), { chunkSize: 10 });

    expect(result).toBe("/tmp/stored2.bin");
    // ceil(25/10) = 3 parts
    expect(mockedPut).toHaveBeenCalledTimes(3);
  });

  it("throws and skips the upload loop when init response has no upload_id", async () => {
    mockedPost.mockResolvedValueOnce({ data: {} });

    await expect(uploadFileInChunks(makeFile(10))).rejects.toThrow();
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("throws when the complete response has no stored_path", async () => {
    mockedPost.mockImplementation((url: string) => {
      if (url.includes("initUpload")) {
        return Promise.resolve({
          data: { upload_id: "up-3", part_size: 10, total_parts: 1 },
        });
      }
      if (url.includes(":complete")) {
        return Promise.resolve({ data: {} });
      }
      return Promise.reject(new Error(`unexpected post ${url}`));
    });
    mockedPut.mockResolvedValue({ data: {} });

    await expect(uploadFileInChunks(makeFile(5))).rejects.toThrow();
  });

  it("propagates errors raised while uploading a part", async () => {
    mockedPost.mockResolvedValueOnce({
      data: { upload_id: "up-4", part_size: 10, total_parts: 1 },
    });
    mockedPut.mockRejectedValueOnce(new Error("network down"));

    await expect(uploadFileInChunks(makeFile(5))).rejects.toThrow("network down");
  });
});

describe("abortUpload", () => {
  beforeEach(() => {
    mockedPost.mockReset();
  });

  it("posts to the abort endpoint with the encoded upload id", async () => {
    mockedPost.mockResolvedValueOnce({ data: {} });

    await abortUpload("up with space");

    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining(encodeURIComponent("up with space")),
      {},
    );
  });

  it("propagates errors from the abort request", async () => {
    mockedPost.mockRejectedValueOnce(new Error("abort failed"));

    await expect(abortUpload("up-5")).rejects.toThrow("abort failed");
  });
});
