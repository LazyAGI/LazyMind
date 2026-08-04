import { describe, expect, it } from "vitest";
import { formatFileSize } from "./index";

describe("formatFileSize", () => {
  it("formats bytes without conversion", () => {
    expect(formatFileSize(500)).toBe("500.00 B");
  });

  it("formats kilobytes", () => {
    expect(formatFileSize(2048)).toBe("2.00 KB");
  });

  it("formats megabytes", () => {
    expect(formatFileSize(1024 * 1024 * 3)).toBe("3.00 MB");
  });

  it("rounds to two decimal places", () => {
    expect(formatFileSize(1500)).toBe("1.46 KB");
  });
});
