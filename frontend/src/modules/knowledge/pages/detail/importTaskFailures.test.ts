import { describe, expect, it, vi } from "vitest";
import { inspectFinishedImportTasks, mergeTrackedImportTasks, reconcileImportTaskFailures } from "./importTaskFailures";

vi.mock("@/components/request", () => ({ localizeErrorCode: (_code: string, fallback: string) => fallback }));

describe("finished import task failure inspection", () => {
  it("tracks newly created tasks without dropping ongoing tasks or duplicating task IDs", () => {
    expect(mergeTrackedImportTasks(
      [{ task_id: "running", display_name: "existing.pdf" }, { task_id: "same", target_pid: "folder" }],
      [{ task_id: "new", display_name: "new.pdf" }, { task_id: "same", display_name: "updated.pdf" }, {}],
    )).toEqual([
      { task_id: "running", display_name: "existing.pdf" },
      { task_id: "same", target_pid: "folder", display_name: "updated.pdf" },
      { task_id: "new", display_name: "new.pdf" },
    ]);
  });

  it("reports ordinary parser failures and preserves the affected filename", async () => {
    const failures = await inspectFinishedImportTasks([{ task_id: "task-a", display_name: "study.pdf" }], async () => ({ task_state: "FAILED", err_msg: "PDF 解析失败：文件已损坏" }));
    expect(failures).toEqual([{ taskId: "task-a", message: "study.pdf：PDF 解析失败：文件已损坏", needsFFmpeg: false }]);
  });

  it("reports explicit task errors even when the task state is not FAILED", async () => {
    const failures = await inspectFinishedImportTasks([{ task_id: "task-b" }], async () => ({ task_state: "SUCCESS", err_msg: "部分页面处理失败" }));
    expect(failures[0]?.message).toContain("部分页面处理失败");
  });

  it.each(["SUCCESS", "SUCCEEDED"])("does not report the parser success acknowledgement for a %s task", async (task_state) => {
    const failures = await inspectFinishedImportTasks([{ task_id: "ok", display_name: "paper.pdf" }], async () => ({ task_state, err_msg: "  Success  " }));
    expect(failures).toEqual([]);
  });

  it("does not let a success acknowledgement hide a failed state", async () => {
    const failures = await inspectFinishedImportTasks([{ task_id: "failed" }], async () => ({ task_state: "FAILED", err_msg: "success" }));
    expect(failures).toHaveLength(1);
    expect(failures[0].taskId).toBe("failed");
  });

  it("keeps FFmpeg guidance and gives failed tasks without an error a useful fallback", async () => {
    const failures = await inspectFinishedImportTasks([{ task_id: "video" }, { task_id: "blank" }], async (id) => id === "video" ? { task_state: "FAILED", err_msg: "ffmpeg not found" } : { task_state: "FAILED" });
    expect(failures[0]).toMatchObject({ taskId: "video", needsFFmpeg: true });
    expect(failures[1]).toMatchObject({ taskId: "blank", message: "blank：任务处理失败，服务未返回具体原因", needsFFmpeg: false });
  });

  it("reports task inspection request errors instead of treating them as success", async () => {
    const failures = await inspectFinishedImportTasks([{ task_id: "task-c", display_name: "paper.pdf" }], async () => { throw { response: { data: { message: "任务服务不可用", detail: "连接超时" } } }; });
    expect(failures).toEqual([{ taskId: "task-c", message: "paper.pdf：无法确认导入结果：任务服务不可用（连接超时）", needsFFmpeg: false }]);
  });

  it("does not report successful tasks or request missing task identifiers", async () => {
    const load = vi.fn(async () => ({ task_state: "SUCCESS", err_msg: "" }));
    expect(await inspectFinishedImportTasks([{ task_id: "ok" }, {}], load)).toEqual([]);
    expect(load).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledWith("ok");
  });

  it("clears an earlier failure after the same task succeeds on retry", async () => {
    const tasks = [{ task_id: "retry", display_name: "paper.pdf" }];
    const previous = await inspectFinishedImportTasks(tasks, async () => ({ task_state: "FAILED", err_msg: "parser unavailable" }));
    const unrelated = { taskId: "other", message: "other file failed", needsFFmpeg: false };
    const finished = await inspectFinishedImportTasks(tasks, async () => ({ task_state: "SUCCESS", err_msg: "success" }));

    expect(reconcileImportTaskFailures([...previous, unrelated], tasks, finished)).toEqual([unrelated]);
  });

  it("replaces an earlier failure with the retry's latest failure", async () => {
    const tasks = [{ task_id: "retry", display_name: "paper.pdf" }];
    const previous = [{ taskId: "retry", message: "previous failure", needsFFmpeg: false }];
    const finished = await inspectFinishedImportTasks(tasks, async () => ({ task_state: "FAILED", err_msg: "file corrupted" }));

    expect(reconcileImportTaskFailures(previous, tasks, finished)).toEqual([{ taskId: "retry", message: "paper.pdf：file corrupted", needsFFmpeg: false }]);
  });

  it("keeps a visible uncertainty error when the retry result cannot be queried", async () => {
    const tasks = [{ task_id: "retry", display_name: "paper.pdf" }];
    const previous = [{ taskId: "retry", message: "previous failure", needsFFmpeg: false }];
    const finished = await inspectFinishedImportTasks(tasks, async () => { throw new Error("task service unavailable"); });

    const failures = reconcileImportTaskFailures(previous, tasks, finished);
    expect(failures).toHaveLength(1);
    expect(failures[0].message).toContain("无法确认导入结果：task service unavailable");
  });
});
