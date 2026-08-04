import { describe, expect, it, vi } from "vitest";
import {
  formatNow,
  getDirectoryLabel,
  getDocumentType,
  isDocumentNeedSync,
} from "./detailHelpers";

describe("isDocumentNeedSync", () => {
  it("flags new/changed/deleted as needing sync", () => {
    expect(isDocumentNeedSync("new")).toBe(true);
    expect(isDocumentNeedSync("changed")).toBe(true);
    expect(isDocumentNeedSync("deleted")).toBe(true);
  });

  it("does not flag unchanged", () => {
    expect(isDocumentNeedSync("unchanged")).toBe(false);
  });
});

describe("formatNow", () => {
  it("formats the current time as YYYY-MM-DD HH:mm", () => {
    vi.setSystemTime(new Date(2026, 0, 5, 9, 3));
    expect(formatNow()).toBe("2026-01-05 09:03");
    vi.useRealTimers();
  });
});

describe("getDirectoryLabel", () => {
  it("falls back to source name for single-segment paths", () => {
    expect(getDirectoryLabel("file.txt", "MySource")).toBe("MySource");
    expect(getDirectoryLabel("", "MySource")).toBe("MySource");
  });

  it("returns the parent directory for nested paths", () => {
    expect(getDirectoryLabel("a/b/c.txt", "MySource")).toBe("b");
  });

  it("returns the first segment for exactly two segments", () => {
    expect(getDirectoryLabel("a/c.txt", "MySource")).toBe("a");
  });
});

describe("getDocumentType", () => {
  it("returns the lowercase extension of the last dot", () => {
    expect(getDocumentType("Report.PDF")).toBe("pdf");
    expect(getDocumentType("archive.tar.gz")).toBe("gz");
  });

  it("returns unknown when there is no extension", () => {
    expect(getDocumentType("README")).toBe("unknown");
  });
});
