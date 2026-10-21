import { afterEach, describe, expect, it, vi } from "vitest";
import { migratePaperLibrary } from "./libraryModel";
import { emptyLibrary, sampleLibrary } from "./model";
import { isPaperLibrary, loadPaperLibraryV3, markPaperLibrary, savePaperLibraryV3 } from "./store";

afterEach(() => { vi.restoreAllMocks(); localStorage.clear(); });

describe("library kind resolution", () => {
  it("recognizes a server paper library with no browser-local marker", () => {
    expect(isPaperLibrary("server-paper", "paper")).toBe(true);
    localStorage.clear();
    expect(isPaperLibrary("server-paper", "paper")).toBe(true);
  });

  it("lets the server document kind override a stale local paper marker", () => {
    markPaperLibrary("regular-library");
    expect(isPaperLibrary("regular-library", "document")).toBe(false);
  });

  it("uses local markers only for legacy datasets without a server kind", () => {
    markPaperLibrary("legacy-paper");
    expect(isPaperLibrary("legacy-paper")).toBe(true);
    expect(isPaperLibrary("unmarked-library")).toBe(false);
    expect(isPaperLibrary()).toBe(false);
  });
});

it("rolls back a partial library write so a failed import does not appear after reload", () => {
  const before = migratePaperLibrary(emptyLibrary());
  savePaperLibraryV3("write-test", before);
  const write = localStorage.setItem.bind(localStorage);
  vi.spyOn(localStorage, "setItem").mockImplementation((key, value) => {
    if (key.includes(".v2.") && value.includes("Attention Is All You Need")) throw new Error("storage quota exceeded");
    write(key, value);
  });
  expect(() => savePaperLibraryV3("write-test", migratePaperLibrary(sampleLibrary()))).toThrow("storage quota exceeded");
  expect(loadPaperLibraryV3("write-test")).toEqual(before);
});

describe("new library persistence failures", () => {
  it("does not leave new paper records behind when the legacy mirror write fails", () => {
    const write = localStorage.setItem.bind(localStorage);
    vi.spyOn(localStorage, "setItem").mockImplementation((key, value) => {
      if (key.includes(".v2.")) throw new Error("storage unavailable");
      write(key, value);
    });
    expect(() => savePaperLibraryV3("new-test", migratePaperLibrary(sampleLibrary()))).toThrow("storage unavailable");
    expect(loadPaperLibraryV3("new-test").papers).toEqual([]);
  });
});
