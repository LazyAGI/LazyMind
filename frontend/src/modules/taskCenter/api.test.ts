import { describe, expect, it, vi, beforeEach } from "vitest";

vi.mock("@/components/request", () => ({
  axiosInstance: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  BASE_URL: "http://localhost",
}));

import { axiosInstance } from "@/components/request";
import {
  batchCreateAutomationGroup,
  cancelSchedule,
  cancelTask,
  createAutomationGroup,
  createSchedule,
  deleteAutomationGroup,
  deleteSchedule,
  enableSchedule,
  listAutomationGroups,
  listSchedules,
  listScheduleTasks,
  listTasks,
  moveSchedule,
  removeTask,
  runScheduleNow,
  updateSchedule,
} from "./api";

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedPut = axiosInstance.put as unknown as ReturnType<typeof vi.fn>;
const mockedDelete = axiosInstance.delete as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset();
  mockedPost.mockReset();
  mockedPut.mockReset();
  mockedDelete.mockReset();
});

describe("listTasks", () => {
  it("builds a query string from only the provided filters", async () => {
    mockedGet.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 10 } });
    await listTasks({ status: "running", page: 2 });
    expect(mockedGet).toHaveBeenCalledWith(
      "http://localhost/api/core/task-center/tasks?status=running&page=2",
    );
  });

  it("omits filters that are not provided", async () => {
    mockedGet.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 10 } });
    await listTasks({});
    expect(mockedGet).toHaveBeenCalledWith("http://localhost/api/core/task-center/tasks?");
  });

  it("returns the response data", async () => {
    const payload = { items: [{ id: "t1" }], total: 1, page: 1, page_size: 10 };
    mockedGet.mockResolvedValue({ data: payload });
    const result = await listTasks({});
    expect(result).toEqual(payload);
  });
});

describe("task actions", () => {
  it("cancels a task via the :cancel endpoint", async () => {
    mockedPost.mockResolvedValue({});
    await cancelTask("t1");
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/task-center/tasks/t1:cancel");
  });

  it("removes a task via the :remove endpoint", async () => {
    mockedPost.mockResolvedValue({});
    await removeTask("t1");
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/task-center/tasks/t1:remove");
  });
});

describe("schedules", () => {
  it("lists schedules without the include_disabled param by default", async () => {
    mockedGet.mockResolvedValue({ data: { items: [], total: 0 } });
    await listSchedules();
    expect(mockedGet).toHaveBeenCalledWith("http://localhost/api/core/schedules");
  });

  it("lists schedules with include_disabled=true when requested", async () => {
    mockedGet.mockResolvedValue({ data: { items: [], total: 0 } });
    await listSchedules(true);
    expect(mockedGet).toHaveBeenCalledWith(
      "http://localhost/api/core/schedules?include_disabled=true",
    );
  });

  it("creates a schedule with the given payload", async () => {
    const req = { cron_expr: "0 0 * * *", prompt_template: "hi", timezone: "UTC" };
    mockedPost.mockResolvedValue({ data: { id: "s1" } });
    const result = await createSchedule(req);
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/schedules", req);
    expect(result).toEqual({ id: "s1" });
  });

  it("cancels, enables, and runs a schedule via action suffix endpoints", async () => {
    mockedPost.mockResolvedValue({ data: {} });
    await cancelSchedule("s1");
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1:cancel");

    await enableSchedule("s1");
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1:enable");

    await runScheduleNow("s1");
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1:run-now");
  });

  it("updates a schedule via PUT", async () => {
    mockedPut.mockResolvedValue({ data: { id: "s1" } });
    await updateSchedule("s1", { cron_expr: "* * * * *" });
    expect(mockedPut).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1", {
      cron_expr: "* * * * *",
    });
  });

  it("deletes a schedule via DELETE", async () => {
    mockedDelete.mockResolvedValue({});
    await deleteSchedule("s1");
    expect(mockedDelete).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1");
  });

  it("lists schedule tasks with pagination params", async () => {
    mockedGet.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 5 } });
    await listScheduleTasks("s1", 2, 5);
    expect(mockedGet).toHaveBeenCalledWith(
      "http://localhost/api/core/task-center/schedules/s1/tasks?page=2&page_size=5",
    );
  });

  it("moves a schedule to a group and position, defaulting group_id to null", async () => {
    mockedPost.mockResolvedValue({});
    await moveSchedule("s1");
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1:move", {
      group_id: null,
      position: 0,
    });

    await moveSchedule("s1", "group-1", 3);
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/schedules/s1:move", {
      group_id: "group-1",
      position: 3,
    });
  });
});

describe("automation groups", () => {
  it("lists automation groups", async () => {
    mockedGet.mockResolvedValue({ data: { items: [], total: 0 } });
    await listAutomationGroups();
    expect(mockedGet).toHaveBeenCalledWith("http://localhost/api/core/automation-groups");
  });

  it("creates an automation group", async () => {
    mockedPost.mockResolvedValue({ data: { id: "g1" } });
    await createAutomationGroup({ name: "组1" });
    expect(mockedPost).toHaveBeenCalledWith("http://localhost/api/core/automation-groups", {
      name: "组1",
    });
  });

  it("deletes an automation group", async () => {
    mockedDelete.mockResolvedValue({});
    await deleteAutomationGroup("g1");
    expect(mockedDelete).toHaveBeenCalledWith("http://localhost/api/core/automation-groups/g1");
  });

  it("batch-creates a group with schedules", async () => {
    const req = {
      group: { name: "组1", timezone: "UTC" },
      tasks: [{ client_key: "k1", name: "任务1", cron_expr: "* * * * *", prompt_template: "p" }],
    };
    mockedPost.mockResolvedValue({ data: { group_id: "g1", schedule_ids: {} } });
    await batchCreateAutomationGroup(req);
    expect(mockedPost).toHaveBeenCalledWith(
      "http://localhost/api/core/automation-groups:batch-create",
      req,
    );
  });
});
