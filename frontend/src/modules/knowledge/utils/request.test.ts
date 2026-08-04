import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  datasetsGet: vi.fn(),
  datasetGet: vi.fn(),
  datasetPost: vi.fn(),
  datasetPatch: vi.fn(),
  datasetDelete: vi.fn(),
  algosGet: vi.fn(),
  tagsGet: vi.fn(),
  batchAddMember: vi.fn(),
  membersGet: vi.fn(),
  memberGroupDelete: vi.fn(),
  memberUserDelete: vi.fn(),
  memberGroupPatch: vi.fn(),
  memberUserPatch: vi.fn(),
  uploadsPost: vi.fn(),
  checkHashesPost: vi.fn(),
  tasksPost: vi.fn(),
  initUploadPost: vi.fn(),
  uploadPartPut: vi.fn(),
  completeUploadPost: vi.fn(),
  abortUploadPost: vi.fn(),
  documentGet: vi.fn(),
  documentSearchPost: vi.fn(),
  documentPatch: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {
    defaults: {},
    interceptors: { request: { use: vi.fn() } },
  },
  BASE_URL: "https://api.example.com",
}));

vi.mock("@/api/generated/knowledge-client", () => ({
  Configuration: class {},
  DatasetMemberServiceApiFactory: () => ({}),
  DocumentServiceApiFactory: () => ({
    documentServiceBatchUpdateDocumentTags: vi.fn(),
  }),
  JobServiceApiFactory: () => ({ jobServiceStartJob: vi.fn() }),
  SegmentServiceApiFactory: () => ({ segmentServiceListSegments: vi.fn() }),
}));

vi.mock("@/api/generated/authservice-client", () => ({
  UsersApiFactory: () => ({ usersGet: vi.fn() }),
  GroupsApiFactory: () => ({ groupsGet: vi.fn() }),
}));

vi.mock("@/api/generated/core-client", () => ({
  DefaultApiFactory: () => ({
    apiCoreDatasetAlgosGet: apiMocks.algosGet,
    apiCoreDatasetTagsGet: apiMocks.tagsGet,
    apiCoreDatasetsDatasetBatchAddMemberPost: apiMocks.batchAddMember,
    apiCoreDatasetsDatasetMembersGet: apiMocks.membersGet,
    apiCoreDatasetsDatasetMembersGroupsGroupIdDelete: apiMocks.memberGroupDelete,
    apiCoreDatasetsDatasetMembersUserIdDelete: apiMocks.memberUserDelete,
    apiCoreDatasetsDatasetMembersGroupsGroupIdPatch: apiMocks.memberGroupPatch,
    apiCoreDatasetsDatasetMembersUserIdPatch: apiMocks.memberUserPatch,
    apiCoreDatasetsDatasetUploadsPost: apiMocks.uploadsPost,
  }),
  DatasetsApiFactory: () => ({
    apiCoreDatasetsGet: apiMocks.datasetsGet,
    apiCoreDatasetsDatasetGet: apiMocks.datasetGet,
    apiCoreDatasetsPost: apiMocks.datasetPost,
    apiCoreDatasetsDatasetPatch: apiMocks.datasetPatch,
    apiCoreDatasetsDatasetDelete: apiMocks.datasetDelete,
  }),
  DocumentsApiFactory: () => ({
    apiCoreDatasetsDatasetDocumentsDocumentGet: apiMocks.documentGet,
    apiCoreDatasetsDatasetDocumentsSearchPost: apiMocks.documentSearchPost,
    apiCoreDatasetsDatasetDocumentsDocumentPatch: apiMocks.documentPatch,
  }),
  TasksApiFactory: () => ({
    apiCoreDatasetsDatasetUploadsCheckHashesPost: apiMocks.checkHashesPost,
    apiCoreDatasetsDatasetTasksPost: apiMocks.tasksPost,
    apiCoreDatasetsDatasetUploadsInitUploadPost: apiMocks.initUploadPost,
    apiCoreDatasetsDatasetUploadsUploadIdPartsPartNumberPut: apiMocks.uploadPartPut,
    apiCoreDatasetsDatasetUploadsUploadIdCompletePost: apiMocks.completeUploadPost,
    apiCoreDatasetsDatasetUploadsUploadIdAbortPost: apiMocks.abortUploadPost,
  }),
  Configuration: class {},
}));

import {
  KnowledgeBaseServiceApi,
  MemberServiceApi,
  TaskServiceApi,
  normalizeProxyableUrl,
  uploadLargeFileToDataset,
} from "./request";

beforeEach(() => {
  Object.values(apiMocks).forEach((fn) => fn.mockReset());
  // @ts-expect-error test override
  import.meta.env.DEV = true;
});

describe("normalizeProxyableUrl", () => {
  it("returns empty string for falsy input", () => {
    expect(normalizeProxyableUrl()).toBe("");
    expect(normalizeProxyableUrl("")).toBe("");
  });

  it("strips the localhost origin in dev mode", () => {
    expect(normalizeProxyableUrl("http://localhost:8000/api/core/x?y=1")).toBe(
      "/api/core/x?y=1",
    );
  });

  it("leaves non-localhost absolute urls unchanged in dev mode", () => {
    expect(normalizeProxyableUrl("https://cdn.example.com/img.png")).toBe(
      "https://cdn.example.com/img.png",
    );
  });

  it("returns the raw string unchanged when it is not a valid URL", () => {
    expect(normalizeProxyableUrl("not-a-valid-url")).toBe("not-a-valid-url");
  });
});

describe("KnowledgeBaseServiceApi", () => {
  it("maps listDatasets request parameters through to the core client", async () => {
    apiMocks.datasetsGet.mockResolvedValue({ data: { datasets: [] } });
    await KnowledgeBaseServiceApi().datasetServiceListDatasets({
      pageSize: 10,
      keyword: "foo",
    });
    expect(apiMocks.datasetsGet).toHaveBeenCalledWith(
      {
        pageToken: undefined,
        pageSize: 10,
        orderBy: undefined,
        keyword: "foo",
        tags: undefined,
      },
      undefined,
    );
  });

  it("passes an update_mask param derived from updateMask on update", async () => {
    apiMocks.datasetPatch.mockResolvedValue({ data: {} });
    await KnowledgeBaseServiceApi().datasetServiceUpdateDataset({
      dataset: "ds1",
      dataset2: { name: "renamed" } as any,
      updateMask: "name",
    });
    expect(apiMocks.datasetPatch).toHaveBeenCalledWith(
      { dataset: "ds1", dataset2: { name: "renamed" } },
      expect.objectContaining({
        params: expect.objectContaining({ update_mask: "name" }),
        headers: expect.objectContaining({ "Content-Type": "application/json" }),
      }),
    );
  });
});

describe("MemberServiceApi.datasetMemberServiceDeleteDatasetMember", () => {
  it("routes to the group delete endpoint when a groupId is resolvable", async () => {
    apiMocks.memberGroupDelete.mockResolvedValue({ data: {} });
    await MemberServiceApi().datasetMemberServiceDeleteDatasetMember({
      dataset: "ds1",
      member: "datasets/ds1/members/groups/g1",
    });
    expect(apiMocks.memberGroupDelete).toHaveBeenCalledWith(
      { dataset: "ds1", groupId: "g1" },
      undefined,
    );
    expect(apiMocks.memberUserDelete).not.toHaveBeenCalled();
  });

  it("routes to the user delete endpoint when no groupId is resolvable", async () => {
    apiMocks.memberUserDelete.mockResolvedValue({ data: {} });
    await MemberServiceApi().datasetMemberServiceDeleteDatasetMember({
      dataset: "ds1",
      member: "datasets/ds1/members/id/u1",
    });
    expect(apiMocks.memberUserDelete).toHaveBeenCalledWith(
      { dataset: "ds1", userId: "u1" },
      undefined,
    );
  });
});

describe("TaskServiceApi.uploadFiles", () => {
  it("extracts files, document_pid, relative_path and joined tags from FormData", async () => {
    apiMocks.uploadsPost.mockResolvedValue({ data: { files: [] } });
    const formData = new FormData();
    const file = new File(["hi"], "a.txt", { type: "text/plain" });
    formData.append("files", file);
    formData.append("document_pid", "pid1");
    formData.append("relative_path", "folder/a.txt");
    formData.append("document_tags", "tag1");
    formData.append("document_tags", "tag2");

    await TaskServiceApi().uploadFiles("ds1", formData);

    expect(apiMocks.uploadsPost).toHaveBeenCalledWith(
      {
        dataset: "ds1",
        documentPid: "pid1",
        documentTags: "tag1,tag2",
        files: [file],
        relativePath: "folder/a.txt",
      },
      undefined,
    );
  });

  it("passes undefined documentTags and files when none are present", async () => {
    apiMocks.uploadsPost.mockResolvedValue({ data: { files: [] } });
    const formData = new FormData();
    await TaskServiceApi().uploadFiles("ds1", formData);
    expect(apiMocks.uploadsPost).toHaveBeenCalledWith(
      {
        dataset: "ds1",
        documentPid: undefined,
        documentTags: undefined,
        files: undefined,
        relativePath: undefined,
      },
      undefined,
    );
  });
});

describe("TaskServiceApi.checkHashes", () => {
  it("wraps hashes into a checkFileHashesRequest body with json headers", async () => {
    apiMocks.checkHashesPost.mockResolvedValue({ data: { missing_hashes: [] } });
    await TaskServiceApi().checkHashes("ds1", ["h1", "h2"]);
    expect(apiMocks.checkHashesPost).toHaveBeenCalledWith(
      { dataset: "ds1", checkFileHashesRequest: { hashes: ["h1", "h2"] } },
      expect.objectContaining({
        headers: expect.objectContaining({ "Content-Type": "application/json" }),
      }),
    );
  });
});

describe("uploadLargeFileToDataset", () => {
  it("uploads a file in chunks and completes, returning the uploadFileId", async () => {
    apiMocks.initUploadPost.mockResolvedValue({
      data: { upload_id: "u1", total_parts: 2, part_size: 5 },
    });
    apiMocks.uploadPartPut.mockResolvedValue({ data: {} });
    apiMocks.completeUploadPost.mockResolvedValue({
      data: { upload_file_id: "file-123", content_hash: "hash-abc" },
    });

    const bigFile = new File([new Uint8Array(10)], "big.bin", {
      type: "application/octet-stream",
    });
    const onProgress = vi.fn();

    const result = await uploadLargeFileToDataset("ds1", bigFile, { onProgress });

    expect(apiMocks.uploadPartPut).toHaveBeenCalledTimes(2);
    expect(apiMocks.completeUploadPost).toHaveBeenCalledWith({
      dataset: "ds1",
      uploadId: "u1",
    });
    expect(onProgress).toHaveBeenCalledTimes(2);
    expect(onProgress).toHaveBeenLastCalledWith(10, 10);
    expect(result).toEqual({ uploadFileId: "file-123", contentHash: "hash-abc" });
  });

  it("aborts the upload session and rethrows when a part upload fails", async () => {
    apiMocks.initUploadPost.mockResolvedValue({
      data: { upload_id: "u2", total_parts: 1, part_size: 10 },
    });
    apiMocks.uploadPartPut.mockRejectedValue(new Error("network error"));
    apiMocks.abortUploadPost.mockResolvedValue({ data: {} });

    const file = new File([new Uint8Array(5)], "small.bin");

    await expect(uploadLargeFileToDataset("ds1", file)).rejects.toThrow(
      "network error",
    );
    expect(apiMocks.abortUploadPost).toHaveBeenCalledWith({
      dataset: "ds1",
      uploadId: "u2",
    });
  });

  it("throws when the complete response has no upload_file_id", async () => {
    apiMocks.initUploadPost.mockResolvedValue({
      data: { upload_id: "u3", total_parts: 1, part_size: 10 },
    });
    apiMocks.uploadPartPut.mockResolvedValue({ data: {} });
    apiMocks.completeUploadPost.mockResolvedValue({ data: {} });
    apiMocks.abortUploadPost.mockResolvedValue({ data: {} });

    const file = new File([new Uint8Array(5)], "small.bin");

    await expect(uploadLargeFileToDataset("ds1", file)).rejects.toThrow(
      "upload_file_id",
    );
  });

  it("aborts immediately when the abort signal is already triggered", async () => {
    apiMocks.initUploadPost.mockResolvedValue({
      data: { upload_id: "u4", total_parts: 1, part_size: 10 },
    });
    apiMocks.abortUploadPost.mockResolvedValue({ data: {} });
    const controller = new AbortController();
    controller.abort();
    const file = new File([new Uint8Array(5)], "small.bin");

    await expect(
      uploadLargeFileToDataset("ds1", file, { signal: controller.signal }),
    ).rejects.toThrow("Upload aborted");
    expect(apiMocks.uploadPartPut).not.toHaveBeenCalled();
  });
});
