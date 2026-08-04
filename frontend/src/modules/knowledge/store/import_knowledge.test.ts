import { describe, it, expect } from "vitest";
import { act } from "@testing-library/react";
import { useImportKnowledgeStore } from "./import_knowledge";

describe("useImportKnowledgeStore", () => {
  it("has an empty fileList and taskList by default", () => {
    expect(useImportKnowledgeStore.getState().fileList).toEqual([]);
    expect(useImportKnowledgeStore.getState().taskList).toEqual([]);
  });

  it("updates fileList via setFileList", () => {
    const files = [{ uid: "f1" }, { uid: "f2" }];

    act(() => {
      useImportKnowledgeStore.getState().setFileList(files);
    });

    expect(useImportKnowledgeStore.getState().fileList).toEqual(files);
  });

  it("updates taskList via setTaskList", () => {
    const tasks = [{ task_id: "t1" }];

    act(() => {
      useImportKnowledgeStore.getState().setTaskList(tasks);
    });

    expect(useImportKnowledgeStore.getState().taskList).toEqual(tasks);
  });

  it("can reset fileList and taskList back to empty arrays", () => {
    act(() => {
      useImportKnowledgeStore.getState().setFileList([{ uid: "f1" }]);
      useImportKnowledgeStore.getState().setTaskList([{ task_id: "t1" }]);
      useImportKnowledgeStore.getState().setFileList([]);
      useImportKnowledgeStore.getState().setTaskList([]);
    });

    expect(useImportKnowledgeStore.getState().fileList).toEqual([]);
    expect(useImportKnowledgeStore.getState().taskList).toEqual([]);
  });
});
