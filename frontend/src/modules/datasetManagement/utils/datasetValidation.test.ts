import { describe, expect, it } from "vitest";
import {
  joinListField,
  normalizeItemFormValues,
  parseBooleanLike,
  splitListField,
  validateRequiredDatasetItem,
} from "./datasetValidation";

describe("splitListField", () => {
  it("splits a comma separated string and trims each entry", () => {
    expect(splitListField("doc_001, doc_002 ,doc_003")).toEqual([
      "doc_001",
      "doc_002",
      "doc_003",
    ]);
  });

  it("filters out empty segments", () => {
    expect(splitListField("doc_001,, ,doc_002")).toEqual(["doc_001", "doc_002"]);
  });

  it("passes through and trims array input", () => {
    expect(splitListField([" doc_001 ", "doc_002"])).toEqual(["doc_001", "doc_002"]);
  });

  it("returns an empty array for undefined/empty input", () => {
    expect(splitListField(undefined)).toEqual([]);
    expect(splitListField("")).toEqual([]);
  });
});

describe("joinListField", () => {
  it("joins array values with a comma and space", () => {
    expect(joinListField(["a", "b", "c"])).toBe("a, b, c");
  });

  it("returns an empty string for undefined/empty array", () => {
    expect(joinListField(undefined)).toBe("");
    expect(joinListField([])).toBe("");
  });
});

describe("parseBooleanLike", () => {
  it("returns booleans unchanged", () => {
    expect(parseBooleanLike(true)).toBe(true);
    expect(parseBooleanLike(false)).toBe(false);
  });

  it("returns false for empty/missing values", () => {
    expect(parseBooleanLike(undefined)).toBe(false);
    expect(parseBooleanLike("")).toBe(false);
    expect(parseBooleanLike("  ")).toBe(false);
  });

  it("parses truthy string tokens case-insensitively", () => {
    expect(parseBooleanLike("TRUE")).toBe(true);
    expect(parseBooleanLike("1")).toBe(true);
    expect(parseBooleanLike("yes")).toBe(true);
    expect(parseBooleanLike("是")).toBe(true);
  });

  it("parses falsy string tokens case-insensitively", () => {
    expect(parseBooleanLike("FALSE")).toBe(false);
    expect(parseBooleanLike("0")).toBe(false);
    expect(parseBooleanLike("no")).toBe(false);
    expect(parseBooleanLike("否")).toBe(false);
  });

  it("returns undefined for unrecognized tokens", () => {
    expect(parseBooleanLike("maybe")).toBeUndefined();
  });
});

describe("normalizeItemFormValues", () => {
  it("trims strings and splits list fields", () => {
    const result = normalizeItemFormValues({
      case_id: " case_1 ",
      question: " q ",
      question_type: " t ",
      ground_truth: " gt ",
      reference_doc_ids: "doc_1, doc_2",
      reference_chunk_ids: "chunk_1",
      is_deleted: true,
    });
    expect(result).toEqual({
      case_id: "case_1",
      question: "q",
      question_type: "t",
      ground_truth: "gt",
      key_points: "",
      reference_context: "",
      reference_doc: "",
      reference_doc_ids: ["doc_1", "doc_2"],
      reference_chunk_ids: ["chunk_1"],
      generate_reason: "",
      is_deleted: true,
    });
  });

  it("defaults missing fields to empty strings/false", () => {
    const result = normalizeItemFormValues({
      question: "q",
      question_type: "t",
      ground_truth: "gt",
    });
    expect(result.case_id).toBe("");
    expect(result.reference_doc_ids).toEqual([]);
    expect(result.is_deleted).toBe(false);
  });
});

describe("validateRequiredDatasetItem", () => {
  const messages = {
    question: "question required",
    question_type: "question_type required",
    ground_truth: "ground_truth required",
  };

  it("returns no errors when all required fields are present", () => {
    expect(
      validateRequiredDatasetItem(
        { question: "q", question_type: "t", ground_truth: "gt" },
        messages,
      ),
    ).toEqual([]);
  });

  it("collects an error for each missing required field", () => {
    expect(validateRequiredDatasetItem({}, messages)).toEqual([
      messages.question,
      messages.question_type,
      messages.ground_truth,
    ]);
  });

  it("treats whitespace-only values as missing", () => {
    expect(
      validateRequiredDatasetItem(
        { question: "   ", question_type: "t", ground_truth: "gt" },
        messages,
      ),
    ).toEqual([messages.question]);
  });
});
