import { describe, expect, it } from "vitest";
import {
  DATA_SOURCE_FILE_TYPE_OPTIONS,
  DEFAULT_DATA_SOURCE_FILE_TYPES,
  DEFAULT_SCAN_TENANT_ID,
  FEISHU_DEFAULT_SCOPES,
  FEISHU_EXCLUDE_PATTERNS,
} from "./options";

describe("dataSource constants/options", () => {
  it("every file type option has a unique value and a matching i18n key convention", () => {
    const values = DATA_SOURCE_FILE_TYPE_OPTIONS.map((option) => option.value);
    expect(new Set(values).size).toBe(values.length);
    DATA_SOURCE_FILE_TYPE_OPTIONS.forEach((option) => {
      expect(option.i18nKey.startsWith("admin.dataSourceFileType")).toBe(true);
      expect(option.extensions.length).toBeGreaterThan(0);
    });
  });

  it("default file types are all present in the full option list", () => {
    const values = new Set(DATA_SOURCE_FILE_TYPE_OPTIONS.map((option) => option.value));
    DEFAULT_DATA_SOURCE_FILE_TYPES.forEach((type) => {
      expect(values.has(type)).toBe(true);
    });
  });

  it("exposes the expected default scan tenant id", () => {
    expect(DEFAULT_SCAN_TENANT_ID).toBe("tenant-demo");
  });

  it("feishu default scopes include offline_access for refresh tokens", () => {
    expect(FEISHU_DEFAULT_SCOPES).toContain("offline_access");
    expect(FEISHU_DEFAULT_SCOPES.length).toBeGreaterThan(0);
  });

  it("feishu exclude patterns filter out temp lock files", () => {
    expect(FEISHU_EXCLUDE_PATTERNS).toContain("**/~$*");
  });
});
