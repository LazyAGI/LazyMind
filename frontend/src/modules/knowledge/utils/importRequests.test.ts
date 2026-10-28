import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ start: vi.fn(), resume: vi.fn() }));
vi.mock("@/api/generated/core-client", () => ({
  TasksApiFactory: () => ({
    apiCoreDatasetsDatasetTasksStartPost: api.start,
    apiCoreDatasetsDatasetTasksTaskResumePost: api.resume,
  }),
  DefaultApiFactory: () => ({}),
}));
vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: { defaults: {}, interceptors: { request: { use: vi.fn() } } },
  localizeErrorCode: (_code: string, fallback = "") => fallback,
}));

import { TaskServiceApi } from "./request";

const failedResult = {
  data: {
    requested_count: 2, started_count: 1, failed_count: 1,
    tasks: [
      { task_id: "ok", status: "STARTED" },
      { task_id: "bad", display_name: "broken.pdf", status: "FAILED", submit_status: "REJECTED", message: "parser unavailable", detail: "upstream returned 503" },
    ],
  },
};

describe("import request failures", () => {
  beforeEach(() => vi.clearAllMocks());

  it("rejects HTTP-success task batches with failed items and preserves the failure reason", async () => {
    api.start.mockResolvedValue(failedResult);
    await expect(TaskServiceApi().startTasks("library", { task_ids: ["ok", "bad"] })).rejects.toThrow("broken.pdf：parser unavailable（upstream returned 503）");
  });

  it("rejects a failed retry instead of returning a successful response", async () => {
    api.resume.mockResolvedValue(failedResult);
    await expect(TaskServiceApi().resumeTask("library", "bad")).rejects.toThrow("parser unavailable");
  });

  it("rejects a partially started batch even when the response lacks failure details", async () => {
    api.start.mockResolvedValue({ data: { requested_count: 2, started_count: 1, failed_count: 1, tasks: [] } });
    await expect(TaskServiceApi().startTasks("library", { task_ids: ["ok", "bad"] })).rejects.toThrow();
  });

  it.each([undefined, {}, { tasks: [], requested_count: 1, started_count: 1, failed_count: 0 }])("rejects missing task outcomes: %j", async (data) => {
    api.start.mockResolvedValue({ data });
    await expect(TaskServiceApi().startTasks("library", { task_ids: ["ok"] })).rejects.toThrow();
  });

  it("rejects REJECTED submission status even if the overall status says STARTED", async () => {
    api.start.mockResolvedValue({ data: { requested_count: 1, started_count: 1, failed_count: 0, tasks: [{ task_id: "bad", status: "STARTED", submit_status: "REJECTED", message: "submission rejected" }] } });
    await expect(TaskServiceApi().startTasks("library", { task_ids: ["bad"] })).rejects.toThrow("submission rejected");
  });

  it("keeps the successful response for a fully started batch", async () => {
    const response = { data: { requested_count: 1, started_count: 1, failed_count: 0, tasks: [{ task_id: "ok", status: "STARTED" }] } };
    api.start.mockResolvedValue(response);
    expect(await TaskServiceApi().startTasks("library", { task_ids: ["ok"] })).toBe(response);
  });

  it("propagates rejected network requests unchanged", async () => {
    const failure = new Error("connection reset");
    api.start.mockRejectedValue(failure);
    await expect(TaskServiceApi().startTasks("library", { task_ids: ["ok"] })).rejects.toBe(failure);
  });
});
