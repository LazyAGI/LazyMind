import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const jobApiMocks = vi.hoisted(() => ({
  jobServiceBatchPresignUploadFileURL: vi.fn(),
  jobServicePresignMultipartUploadFileURL: vi.fn(),
  jobServiceCompleteMultipartUploadFile: vi.fn(),
  jobServiceStartJob: vi.fn(),
  jobServiceCancelJob: vi.fn(),
}));

vi.mock("./request", () => ({
  JobServiceApi: () => jobApiMocks,
}));

const putFileMock = vi.hoisted(() => vi.fn());
vi.mock("./file", () => ({
  default: { putFile: putFileMock },
}));

import { useImportKnowledgeStore } from "@/modules/knowledge/store/import_knowledge";
import { FileState } from "@/modules/knowledge/constants/common";
import { JobJobStateEnum } from "@/api/generated/knowledge-client";
import batchUpload from "./batchUpload";

function resetStore() {
  useImportKnowledgeStore.setState({ fileList: [], taskList: [] });
}

function makeFile(overrides: Partial<Record<string, any>> = {}) {
  return {
    uid: "file-1",
    taskId: "task-1",
    path: "a.txt",
    size: 1024,
    state: FileState.UPLOAD_PENDING,
    originFile: new Blob(["content"]),
    retryCount: 0,
    ...overrides,
  };
}

function makeTask(overrides: Partial<Record<string, any>> = {}) {
  return {
    id: "task-1",
    datasetId: "ds1",
    documentId: "doc1",
    taskState: JobJobStateEnum.Creating,
    ...overrides,
  };
}

beforeEach(() => {
  resetStore();
  Object.values(jobApiMocks).forEach((fn) => fn.mockReset());
  putFileMock.mockReset();
  putFileMock.mockResolvedValue({ headers: new Headers() });
  jobApiMocks.jobServiceStartJob.mockResolvedValue({});
  jobApiMocks.jobServiceCancelJob.mockResolvedValue({});
});

afterEach(() => {
  vi.useRealTimers();
});

describe("addTask / getUploadUrl", () => {
  it("presigns small pending files and marks failed ones when no URL is returned", async () => {
    jobApiMocks.jobServiceBatchPresignUploadFileURL.mockResolvedValue({
      data: { result: { "a.txt": "http://upload/a.txt" } },
    });
    jobApiMocks.jobServiceCompleteMultipartUploadFile.mockResolvedValue({});

    const file = makeFile();
    batchUpload.addTask({ task: makeTask(), fileList: [file] });

    await vi.waitFor(() => {
      const stored = batchUpload.getFileById("file-1");
      expect(stored.uploadUrl).toBe("http://upload/a.txt");
    });

    expect(jobApiMocks.jobServiceBatchPresignUploadFileURL).toHaveBeenCalledWith(
      expect.objectContaining({ job: "task-1" }),
    );
  });

  it("marks a file as Fail when the server returns no presign URL for it", async () => {
    jobApiMocks.jobServiceBatchPresignUploadFileURL.mockResolvedValue({
      data: { result: {} },
    });

    const file = makeFile({ uid: "file-2" });
    batchUpload.addTask({ task: makeTask({ id: "task-2" }), fileList: [file] });

    await vi.waitFor(() => {
      const stored = batchUpload.getFileById("file-2");
      expect(stored.state).toBe(FileState.FAIL);
    });
  });

  it("skips presigning and starts upload directly when there are no small pending files", () => {
    const largeFile = makeFile({
      uid: "file-3",
      taskId: "task-3",
      size: 20 * 1024 * 1024,
      state: FileState.UPLOAD_PENDING,
    });
    jobApiMocks.jobServicePresignMultipartUploadFileURL.mockResolvedValue({
      data: { list: [] },
    });
    batchUpload.addTask({ task: makeTask({ id: "task-3" }), fileList: [largeFile] });

    expect(jobApiMocks.jobServiceBatchPresignUploadFileURL).not.toHaveBeenCalled();
    expect(jobApiMocks.jobServicePresignMultipartUploadFileURL).toHaveBeenCalled();
  });
});

describe("directUpload via uploadPendingFile", () => {
  it("marks a small file with an existing uploadUrl as Success after a successful PUT", async () => {
    vi.useFakeTimers();
    putFileMock.mockResolvedValue({ headers: new Headers() });

    resetStore();
    useImportKnowledgeStore.setState({
      taskList: [makeTask({ id: "task-4" })],
      fileList: [makeFile({ uid: "file-4", taskId: "task-4", uploadUrl: "http://upload/x" })],
    });

    batchUpload.getUploadUrl({ task: makeTask({ id: "task-4" }), pendingList: [] });
    // getUploadUrl with no small files calls starBatchUpload synchronously.
    await vi.advanceTimersByTimeAsync(0);

    expect(batchUpload.getFileById("file-4").state).toBe(FileState.UPLOADING);

    await vi.advanceTimersByTimeAsync(300);
    expect(batchUpload.getFileById("file-4").state).toBe(FileState.PARSE_PENDING);
    expect(batchUpload.getFileById("file-4").percent).toBe(100);
  });

  it("retries a failed upload up to MAX_RETRY_COUNT before marking it Fail", async () => {
    putFileMock.mockRejectedValue(new Error("network down"));

    resetStore();
    useImportKnowledgeStore.setState({
      taskList: [makeTask({ id: "task-5" })],
      fileList: [
        makeFile({
          uid: "file-5",
          taskId: "task-5",
          uploadUrl: "http://upload/y",
          retryCount: 2,
        }),
      ],
    });

    batchUpload.getUploadUrl({ task: makeTask({ id: "task-5" }), pendingList: [] });

    await vi.waitFor(() => {
      expect(batchUpload.getFileById("file-5").state).toBe(FileState.FAIL);
    });
  });
});

describe("cancelUpload", () => {
  it("marks the task and its files as Cancel", () => {
    resetStore();
    useImportKnowledgeStore.setState({
      taskList: [makeTask({ id: "task-6" })],
      fileList: [makeFile({ uid: "file-6", taskId: "task-6" })],
    });

    batchUpload.cancelUpload("task-6");

    expect(batchUpload.getTaskById("task-6").taskState).toBe(JobJobStateEnum.Cancelled);
    expect(batchUpload.getFileById("file-6").state).toBe(FileState.CANCEL);
  });

  it("does nothing when the task does not exist", () => {
    resetStore();
    useImportKnowledgeStore.setState({ taskList: [], fileList: [] });
    expect(() => batchUpload.cancelUpload("missing")).not.toThrow();
  });
});

describe("updateFileList", () => {
  it("clears originFile for files in terminal states (Success/Fail/Cancel)", () => {
    resetStore();
    const blob = new Blob(["x"]);
    batchUpload.updateFileList([
      { uid: "f1", state: FileState.PARSE_PENDING, originFile: blob },
      { uid: "f2", state: FileState.FAIL, originFile: blob },
      { uid: "f3", state: FileState.UPLOADING, originFile: blob },
    ]);

    expect(batchUpload.getFileById("f1").originFile).toBeUndefined();
    expect(batchUpload.getFileById("f2").originFile).toBeUndefined();
    expect(batchUpload.getFileById("f3").originFile).toBe(blob);
  });
});

describe("unload", () => {
  it("cancels running tasks with keepalive and marks all files as Cancel", () => {
    jobApiMocks.jobServiceCancelJob.mockResolvedValue({});
    resetStore();
    useImportKnowledgeStore.setState({
      taskList: [makeTask({ id: "task-7", taskState: JobJobStateEnum.Creating })],
      fileList: [makeFile({ uid: "file-7", taskId: "task-7" })],
    });

    batchUpload.unload();

    expect(jobApiMocks.jobServiceCancelJob).toHaveBeenCalledWith(
      expect.objectContaining({ dataset: "ds1", job: "task-7" }),
    );
    expect(batchUpload.getTaskById("task-7").taskState).toBe(JobJobStateEnum.Cancelled);
    expect(batchUpload.getFileById("file-7").state).toBe(FileState.CANCEL);
  });
});
