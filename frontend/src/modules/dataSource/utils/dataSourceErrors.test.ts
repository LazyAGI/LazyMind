import { describe, expect, it } from "vitest";
import {
  getDataSourceErrorMessage,
  isKnowledgeBaseNameDuplicatedError,
} from "./dataSourceErrors";

describe("getDataSourceErrorMessage", () => {
  it("joins array-form detail messages", () => {
    const error = {
      response: { data: { detail: [{ message: "A" }, { msg: "B" }, "C"] } },
    };
    expect(getDataSourceErrorMessage(error)).toBe("A；B；C");
  });

  it("falls back to message field when detail is not usable", () => {
    const error = { response: { data: { message: "boom" } } };
    expect(getDataSourceErrorMessage(error)).toBe("boom");
  });

  it("falls back to error.message when there is no response payload", () => {
    const error = new Error("network down");
    expect(getDataSourceErrorMessage(error)).toBe("network down");
  });

  it("returns an empty string when nothing is resolvable", () => {
    expect(getDataSourceErrorMessage({})).toBe("");
  });
});

describe("isKnowledgeBaseNameDuplicatedError", () => {
  it("detects the specific duplicate error code", () => {
    const error = { response: { data: { code: "2001102" } } };
    expect(isKnowledgeBaseNameDuplicatedError(error)).toBe(true);
  });

  it("detects the duplicate error via message text case-insensitively", () => {
    const error = { response: { data: { message: "Dataset Name Already Exists" } } };
    expect(isKnowledgeBaseNameDuplicatedError(error)).toBe(true);
  });

  it("returns false for unrelated errors", () => {
    const error = { response: { data: { code: "500", message: "server error" } } };
    expect(isKnowledgeBaseNameDuplicatedError(error)).toBe(false);
  });
});
