import { describe, expect, it } from "vitest";
import { formatDateTime, formatFileSize } from "./shared";

describe("formatDateTime", () => {
  it("returns a placeholder for empty input", () => {
    expect(formatDateTime()).toBe("-");
    expect(formatDateTime("")).toBe("-");
  });

  it("replaces the T separator and truncates to minutes", () => {
    expect(formatDateTime("2026-05-27T10:30:45")).toBe("2026-05-27 10:30");
  });

  it("truncates strings that are already shorter than 16 chars without breaking", () => {
    expect(formatDateTime("2026-05-27")).toBe("2026-05-27");
  });
});

describe("formatFileSize", () => {
  it("returns a placeholder when size is missing or zero", () => {
    expect(formatFileSize()).toBe("-");
    expect(formatFileSize(0)).toBe("-");
  });

  it("formats bytes below 1024 as B", () => {
    expect(formatFileSize(512)).toBe("512 B");
  });

  it("formats sizes below 1MB as KB with one decimal", () => {
    expect(formatFileSize(2048)).toBe("2.0 KB");
    expect(formatFileSize(1536)).toBe("1.5 KB");
  });

  it("formats sizes at or above 1MB as MB with one decimal", () => {
    expect(formatFileSize(1024 * 1024 * 2.5)).toBe("2.5 MB");
  });
});
