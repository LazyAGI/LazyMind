import { afterEach, describe, expect, it, vi } from "vitest";
import { migratePaperLibrary } from "./libraryModel";
import { emptyLibrary, sampleLibrary } from "./model";
import { isPaperLibrary, loadPreviewPaperLibrary, savePreviewPaperLibrary } from "./store";

afterEach(() => { vi.restoreAllMocks(); localStorage.clear(); });

describe("persisted scene resolution", () => {
 it("recognizes only the server paper scene", () => {
  expect(isPaperLibrary("academic_papers")).toBe(true);
  expect(isPaperLibrary("general")).toBe(false);
  localStorage.setItem("lazymind.paper-library.ids",JSON.stringify(["general"]));
  expect(isPaperLibrary("general")).toBe(false);
  expect(isPaperLibrary()).toBe(false);
 });
});

it("rolls back a partial library write so a failed import does not appear after reload", () => {
  const before = migratePaperLibrary(emptyLibrary());
  savePreviewPaperLibrary("write-test", before);
  const write = localStorage.setItem.bind(localStorage);
  vi.spyOn(localStorage, "setItem").mockImplementation((key, value) => {
    if (key.includes(".v2.") && value.includes("Attention Is All You Need")) throw new Error("storage quota exceeded");
    write(key, value);
  });
  expect(() => savePreviewPaperLibrary("write-test", migratePaperLibrary(sampleLibrary()))).toThrow("storage quota exceeded");
  expect(loadPreviewPaperLibrary("write-test")).toEqual(before);
});

describe("new library persistence failures", () => {
  it("does not leave new paper records behind when the legacy mirror write fails", () => {
    const write = localStorage.setItem.bind(localStorage);
    vi.spyOn(localStorage, "setItem").mockImplementation((key, value) => {
      if (key.includes(".v2.")) throw new Error("storage unavailable");
      write(key, value);
    });
    expect(() => savePreviewPaperLibrary("new-test", migratePaperLibrary(sampleLibrary()))).toThrow("storage unavailable");
    expect(loadPreviewPaperLibrary("new-test").papers).toEqual([]);
  });
});
