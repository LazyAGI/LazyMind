import { describe, expect, it } from "vitest";
import * as XLSX from "xlsx";
import {
  buildImportPreview,
  createAutoFieldMapping,
  createTemplateRows,
  getFileKind,
  getMissingRequiredMappings,
  parseDatasetFile,
  type DatasetImportMessages,
} from "./datasetImport";
import type { FieldMapping } from "../shared";

const messages: DatasetImportMessages = {
  numbersUnsupported: "numbers unsupported",
  fileUnsupported: "file unsupported",
  jsonFormatInvalid: "json format invalid",
  deletedFieldInvalid: "is_deleted invalid",
  required: {
    question: "question required",
    question_type: "question_type required",
    ground_truth: "ground_truth required",
  },
};

function makeFile(name: string, content: string, type = "text/plain") {
  return new File([content], name, { type });
}

describe("getFileKind", () => {
  it("recognizes supported extensions case-insensitively", () => {
    expect(getFileKind(makeFile("a.XLSX", ""))).toBe("xlsx");
    expect(getFileKind(makeFile("a.xls", ""))).toBe("xls");
    expect(getFileKind(makeFile("a.csv", ""))).toBe("csv");
    expect(getFileKind(makeFile("a.json", ""))).toBe("json");
  });

  it("flags .numbers files distinctly from unknown files", () => {
    expect(getFileKind(makeFile("a.numbers", ""))).toBe("numbers");
    expect(getFileKind(makeFile("a.txt", ""))).toBe("unknown");
  });
});

describe("parseDatasetFile", () => {
  it("throws the numbers-unsupported message for .numbers files", async () => {
    await expect(parseDatasetFile(makeFile("a.numbers", ""), messages)).rejects.toThrow(
      messages.numbersUnsupported,
    );
  });

  it("throws the file-unsupported message for unknown extensions", async () => {
    await expect(parseDatasetFile(makeFile("a.txt", ""), messages)).rejects.toThrow(
      messages.fileUnsupported,
    );
  });

  it("parses a JSON array file into row objects", async () => {
    const file = makeFile(
      "a.json",
      JSON.stringify([{ question: "q1" }, { question: "q2" }]),
      "application/json",
    );
    const rows = await parseDatasetFile(file, messages);
    expect(rows).toEqual([{ question: "q1" }, { question: "q2" }]);
  });

  it("parses a JSON object with an items array", async () => {
    const file = makeFile(
      "a.json",
      JSON.stringify({ items: [{ question: "q1" }] }),
      "application/json",
    );
    const rows = await parseDatasetFile(file, messages);
    expect(rows).toEqual([{ question: "q1" }]);
  });

  it("throws jsonFormatInvalid when the JSON shape is unsupported", async () => {
    const file = makeFile("a.json", JSON.stringify({ foo: "bar" }), "application/json");
    await expect(parseDatasetFile(file, messages)).rejects.toThrow(messages.jsonFormatInvalid);
  });

  it("parses a CSV file into row objects via XLSX", async () => {
    const file = makeFile("a.csv", "question,ground_truth\nq1,gt1\n", "text/csv");
    const rows = await parseDatasetFile(file, messages);
    expect(rows).toEqual([{ question: "q1", ground_truth: "gt1" }]);
  });

  it("parses an xlsx workbook via arrayBuffer", async () => {
    const worksheet = XLSX.utils.json_to_sheet([{ question: "q1", ground_truth: "gt1" }]);
    const workbook = XLSX.utils.book_new();
    XLSX.utils.book_append_sheet(workbook, worksheet, "Sheet1");
    const arrayBuffer = XLSX.write(workbook, { type: "array", bookType: "xlsx" }) as ArrayBuffer;
    const file = new File([arrayBuffer], "a.xlsx", {
      type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    });

    const rows = await parseDatasetFile(file, messages);
    expect(rows).toEqual([{ question: "q1", ground_truth: "gt1" }]);
  });
});

describe("createAutoFieldMapping", () => {
  it("matches source headers to fields via canonical names", () => {
    expect(createAutoFieldMapping(["question", "ground_truth"])).toEqual({
      question: "question",
      ground_truth: "ground_truth",
    });
  });

  it("matches source headers to fields via known aliases, ignoring case/spacing", () => {
    expect(createAutoFieldMapping(["Question Type", "答案"])).toEqual({
      "Question Type": "question_type",
      答案: "ground_truth",
    });
  });

  it("leaves unmatched headers mapped to an empty string", () => {
    expect(createAutoFieldMapping(["unrelated_column"])).toEqual({
      unrelated_column: "",
    });
  });
});

describe("buildImportPreview", () => {
  it("normalizes mapped fields and reports no errors for a complete row", () => {
    const mapping: FieldMapping = {
      question: "question",
      question_type: "question_type",
      ground_truth: "ground_truth",
      doc_ids: "reference_doc_ids",
    };
    const rows = buildImportPreview(
      [{ question: " q1 ", question_type: "t1", ground_truth: "gt1", doc_ids: "d1, d2" }],
      mapping,
      messages,
    );
    expect(rows).toHaveLength(1);
    expect(rows[0].rowIndex).toBe(1);
    expect(rows[0].errors).toEqual([]);
    expect(rows[0].normalized.question).toBe("q1");
    expect(rows[0].normalized.reference_doc_ids).toEqual(["d1", "d2"]);
  });

  it("collects an error per missing required field", () => {
    const mapping: FieldMapping = { question: "question" };
    const rows = buildImportPreview([{ question: "" }], mapping, messages);
    expect(rows[0].errors).toEqual(
      expect.arrayContaining([messages.required.question_type, messages.required.ground_truth]),
    );
  });

  it("reports deletedFieldInvalid when is_deleted cannot be parsed", () => {
    const mapping: FieldMapping = {
      question: "question",
      question_type: "question_type",
      ground_truth: "ground_truth",
      deleted: "is_deleted",
    };
    const rows = buildImportPreview(
      [{ question: "q", question_type: "t", ground_truth: "gt", deleted: "maybe" }],
      mapping,
      messages,
    );
    expect(rows[0].errors).toContain(messages.deletedFieldInvalid);
    expect(rows[0].normalized.is_deleted).toBeUndefined();
  });

  it("ignores source columns that are not mapped to any target field", () => {
    const mapping: FieldMapping = { question: "question", extra: "" };
    const rows = buildImportPreview([{ question: "q", extra: "ignored" }], mapping, messages);
    expect(rows[0].normalized).not.toHaveProperty("extra");
  });
});

describe("getMissingRequiredMappings", () => {
  it("returns required fields that have not been mapped", () => {
    expect(getMissingRequiredMappings({ a: "question" })).toEqual([
      "question_type",
      "ground_truth",
    ]);
  });

  it("returns an empty array once all required fields are mapped", () => {
    expect(
      getMissingRequiredMappings({
        a: "question",
        b: "question_type",
        c: "ground_truth",
      }),
    ).toEqual([]);
  });
});

describe("createTemplateRows", () => {
  it("builds a single template row embedding the provided sample text", () => {
    const rows = createTemplateRows({
      question: "示例问题",
      question_type: "事实问答",
      ground_truth: "示例答案",
      key_points: "要点",
      reference_context: "上下文",
      reference_doc: "文档",
      generate_reason: "依据",
    });
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      question: "示例问题",
      question_type: "事实问答",
      reference_doc_ids: "doc_001",
      reference_chunk_ids: "chunk_001, chunk_002",
      is_deleted: false,
    });
  });
});
