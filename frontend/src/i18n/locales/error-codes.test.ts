import { describe, expect, it } from "vitest";
import { enUSErrorMessages, zhCNErrorMessages } from "./error-codes";

describe("error-codes", () => {
  it("uses non-empty alphanumeric/underscore error code keys", () => {
    const codePattern = /^[A-Za-z0-9_]+$/;
    for (const code of Object.keys(zhCNErrorMessages)) {
      expect(code).toMatch(codePattern);
    }
    for (const code of Object.keys(enUSErrorMessages)) {
      expect(code).toMatch(codePattern);
    }
  });

  it("has no empty message values in either locale", () => {
    for (const [code, message] of Object.entries(zhCNErrorMessages)) {
      expect(message, `zh-CN[${code}]`).toBeTruthy();
    }
    for (const [code, message] of Object.entries(enUSErrorMessages)) {
      expect(message, `en-US[${code}]`).toBeTruthy();
    }
  });

  it("defines the exact same set of error codes for zh-CN and en-US", () => {
    const zhCodes = Object.keys(zhCNErrorMessages).sort();
    const enCodes = Object.keys(enUSErrorMessages).sort();
    expect(enCodes).toEqual(zhCodes);
  });

  it("contains at least one known generic request error code", () => {
    expect(zhCNErrorMessages).toHaveProperty("1000000");
    expect(enUSErrorMessages).toHaveProperty("1000000");
  });
});
