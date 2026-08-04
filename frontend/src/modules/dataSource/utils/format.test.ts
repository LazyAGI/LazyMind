import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatDateTime,
  resolveParsedDocumentCount,
  resolveStorageUsed,
} from "./format";

describe("formatDateTime", () => {
  it("returns '-' for empty input", () => {
    expect(formatDateTime(undefined)).toBe("-");
    expect(formatDateTime("")).toBe("-");
  });

  it("returns the raw value when it cannot be parsed", () => {
    expect(formatDateTime("not-a-date")).toBe("not-a-date");
  });

  it("formats a valid ISO string into YYYY-MM-DD HH:mm", () => {
    const iso = new Date(2026, 2, 15, 8, 30).toISOString();
    expect(formatDateTime(iso)).toMatch(/^2026-\d{2}-\d{2} \d{2}:\d{2}$/);
  });
});

describe("formatBytes", () => {
  it("returns 0 B for falsy or negative values", () => {
    expect(formatBytes(undefined)).toBe("0 B");
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(-5)).toBe("0 B");
  });

  it("keeps whole numbers for byte-scale values", () => {
    expect(formatBytes(512)).toBe("512 B");
  });

  it("scales up through KB/MB/GB and rounds appropriately", () => {
    expect(formatBytes(2048)).toBe("2.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1024 * 1024 * 3)).toBe("3.0 MB");
  });

  it("caps at TB for extremely large values", () => {
    expect(formatBytes(1024 ** 5)).toMatch(/TB$/);
  });
});

describe("resolveStorageUsed", () => {
  it("uses storage_bytes when it is a number", () => {
    expect(resolveStorageUsed({ storage_bytes: 2048 })).toBe("2.0 KB");
  });

  it("falls back through the alternate snake/camel case keys", () => {
    expect(resolveStorageUsed({ storageBytes: 1024 })).toBe("1.0 KB");
    expect(resolveStorageUsed({ storage_used_bytes: 1024 })).toBe("1.0 KB");
    expect(resolveStorageUsed({ storageUsedBytes: 1024 })).toBe("1.0 KB");
  });

  it("parses numeric strings", () => {
    expect(resolveStorageUsed({ storage_bytes: "1024" })).toBe("1.0 KB");
  });

  it("returns the fallback (or 0 B) when nothing is resolvable", () => {
    expect(resolveStorageUsed(undefined, "N/A")).toBe("N/A");
    expect(resolveStorageUsed({})).toBe("0 B");
  });
});

describe("resolveParsedDocumentCount", () => {
  it("reads parsed_document_count or parsedDocumentCount", () => {
    expect(resolveParsedDocumentCount({ parsed_document_count: 5 })).toBe(5);
    expect(resolveParsedDocumentCount({ parsedDocumentCount: 7 })).toBe(7);
  });

  it("parses numeric strings and clamps to non-negative integers", () => {
    expect(resolveParsedDocumentCount({ parsed_document_count: "3.9" })).toBe(3);
    expect(resolveParsedDocumentCount({ parsed_document_count: -2 })).toBe(0);
  });

  it("returns the fallback when the value is not finite", () => {
    expect(resolveParsedDocumentCount({}, 9)).toBe(9);
    expect(resolveParsedDocumentCount({ parsed_document_count: "nope" }, 4)).toBe(4);
  });
});
