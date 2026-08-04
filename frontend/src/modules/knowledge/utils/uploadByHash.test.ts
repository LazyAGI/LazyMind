import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  checkHashes: vi.fn(),
  uploadFiles: vi.fn(),
  uploadLargeFileToDataset: vi.fn(),
  computeFileSha256: vi.fn(),
}));

vi.mock("@/modules/knowledge/utils/request", () => ({
  TaskServiceApi: () => ({
    checkHashes: mocks.checkHashes,
    uploadFiles: mocks.uploadFiles,
  }),
  uploadLargeFileToDataset: mocks.uploadLargeFileToDataset,
}));

vi.mock("@/modules/knowledge/utils/fileHash", () => ({
  CHECK_HASHES_BATCH_SIZE: 2,
  computeFileSha256: mocks.computeFileSha256,
}));

import { buildUploadTaskItems } from "./uploadByHash";

function fakeFile(name: string, size: number): File {
  const file = new File(["x"], name, { type: "text/plain" });
  Object.defineProperty(file, "size", { value: size });
  return file;
}

beforeEach(() => {
  Object.values(mocks).forEach((fn) => fn.mockReset());
});

describe("buildUploadTaskItems", () => {
  it("returns an empty array when there are no file items", async () => {
    const result = await buildUploadTaskItems({ datasetId: "ds1", fileItems: [] });
    expect(result).toEqual([]);
    expect(mocks.computeFileSha256).not.toHaveBeenCalled();
  });

  it("reuses content_hash without uploading when the hash already exists on the server", async () => {
    mocks.computeFileSha256.mockResolvedValue("hash-existing");
    mocks.checkHashes.mockResolvedValue({ data: { missing_hashes: [] } });

    const file = fakeFile("a.txt", 100);
    const result = await buildUploadTaskItems({
      datasetId: "ds1",
      fileItems: [{ originFile: file, path: "a.txt" }],
    });

    expect(mocks.uploadFiles).not.toHaveBeenCalled();
    expect(result).toEqual([
      { content_hash: "hash-existing", task: { display_name: "a.txt" } },
    ]);
  });

  it("uploads a small missing file via uploadFiles and assigns upload_file_id to the first occurrence", async () => {
    mocks.computeFileSha256.mockResolvedValue("hash-new");
    mocks.checkHashes.mockResolvedValue({ data: { missing_hashes: ["hash-new"] } });
    mocks.uploadFiles.mockResolvedValue({
      data: { files: [{ upload_file_id: "uf-1", content_hash: "hash-new" }] },
    });

    const file = fakeFile("b.txt", 100);
    const result = await buildUploadTaskItems({
      datasetId: "ds1",
      fileItems: [{ originFile: file, path: "b.txt" }],
      documentPid: "pid1",
      tags: ["tag1"],
    });

    expect(mocks.uploadFiles).toHaveBeenCalledTimes(1);
    expect(result).toEqual([
      {
        upload_file_id: "uf-1",
        task: {
          display_name: "b.txt",
          document_pid: "pid1",
          document_tags: ["tag1"],
        },
      },
    ]);
  });

  it("uploads a duplicate hash once and lets subsequent identical files reuse content_hash", async () => {
    mocks.computeFileSha256.mockResolvedValue("hash-dup");
    mocks.checkHashes.mockResolvedValue({ data: { missing_hashes: ["hash-dup"] } });
    mocks.uploadFiles.mockResolvedValue({
      data: { files: [{ upload_file_id: "uf-dup" }] },
    });

    const fileA = fakeFile("dup1.txt", 100);
    const fileB = fakeFile("dup2.txt", 100);
    const result = await buildUploadTaskItems({
      datasetId: "ds1",
      fileItems: [
        { originFile: fileA, path: "dup1.txt" },
        { originFile: fileB, path: "dup2.txt" },
      ],
    });

    expect(mocks.uploadFiles).toHaveBeenCalledTimes(1);
    expect(result).toEqual([
      { upload_file_id: "uf-dup", task: { display_name: "dup1.txt" } },
      { content_hash: "hash-dup", task: { display_name: "dup2.txt" } },
    ]);
  });

  it("routes files above the large-file threshold through uploadLargeFileToDataset", async () => {
    mocks.computeFileSha256.mockResolvedValue("hash-large");
    mocks.checkHashes.mockResolvedValue({ data: { missing_hashes: ["hash-large"] } });
    mocks.uploadLargeFileToDataset.mockResolvedValue({
      uploadFileId: "uf-large",
      contentHash: "hash-large",
    });

    const bigFile = fakeFile("big.bin", 11 * 1024 * 1024);
    const result = await buildUploadTaskItems({
      datasetId: "ds1",
      fileItems: [{ originFile: bigFile, path: "folder/big.bin" }],
      folderMode: true,
    });

    expect(mocks.uploadLargeFileToDataset).toHaveBeenCalledWith(
      "ds1",
      bigFile,
      expect.objectContaining({ relativePath: "folder/big.bin" }),
    );
    expect(mocks.uploadFiles).not.toHaveBeenCalled();
    expect(result).toEqual([
      {
        upload_file_id: "uf-large",
        task: { display_name: "big.bin", relative_path: "folder/big.bin" },
      },
    ]);
  });

  it("batches checkHashes calls according to CHECK_HASHES_BATCH_SIZE", async () => {
    mocks.computeFileSha256
      .mockResolvedValueOnce("h1")
      .mockResolvedValueOnce("h2")
      .mockResolvedValueOnce("h3");
    mocks.checkHashes.mockResolvedValue({ data: { missing_hashes: [] } });

    const files = [
      { originFile: fakeFile("1.txt", 10), path: "1.txt" },
      { originFile: fakeFile("2.txt", 10), path: "2.txt" },
      { originFile: fakeFile("3.txt", 10), path: "3.txt" },
    ];
    await buildUploadTaskItems({ datasetId: "ds1", fileItems: files });

    // Batch size mocked to 2: 3 unique hashes => two checkHashes calls.
    expect(mocks.checkHashes).toHaveBeenCalledTimes(2);
    expect(mocks.checkHashes).toHaveBeenNthCalledWith(1, "ds1", ["h1", "h2"]);
    expect(mocks.checkHashes).toHaveBeenNthCalledWith(2, "ds1", ["h3"]);
  });

  it("throws when the upload response is missing an upload_file_id", async () => {
    mocks.computeFileSha256.mockResolvedValue("hash-bad");
    mocks.checkHashes.mockResolvedValue({ data: { missing_hashes: ["hash-bad"] } });
    mocks.uploadFiles.mockResolvedValue({ data: { files: [{}] } });

    const file = fakeFile("bad.txt", 100);
    await expect(
      buildUploadTaskItems({
        datasetId: "ds1",
        fileItems: [{ originFile: file, path: "bad.txt" }],
      }),
    ).rejects.toThrow("upload_file_id");
  });
});
