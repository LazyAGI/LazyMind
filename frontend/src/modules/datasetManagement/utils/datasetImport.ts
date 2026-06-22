import * as XLSX from "xlsx";
import type {
  DatasetImportRow,
  DatasetItem,
  DatasetItemField,
  FieldMapping,
} from "../shared";
import {
  datasetItemFields,
  requiredDatasetItemFields,
} from "../shared";
import { parseBooleanLike, splitListField } from "./datasetValidation";

export type DatasetImportMessages = {
  numbersUnsupported: string;
  fileUnsupported: string;
  jsonFormatInvalid: string;
  deletedFieldInvalid: string;
  required: {
    question: string;
    question_type: string;
    ground_truth: string;
  };
};

const fieldAliases: Record<DatasetItemField, string[]> = {
  case_id: ["case id", "caseid", "case_id", "编号"],
  question: ["question", "query", "问题", "用户问题"],
  question_type: ["question_type", "question type", "问题类型"],
  ground_truth: ["ground_truth", "ground truth", "answer", "标准答案", "答案"],
  key_points: ["key_points", "key points", "答案要点"],
  reference_context: ["reference_context", "reference context", "参考上下文"],
  reference_doc: ["reference_doc", "reference doc", "参考文档"],
  reference_doc_ids: ["reference_doc_ids", "reference doc ids", "参考文档id"],
  reference_chunk_ids: [
    "reference_chunk_ids",
    "reference chunks",
    "reference_chunks",
    "chunk_ids",
    "参考片段id",
  ],
  generate_reason: ["generate_reason", "generate reason", "生成依据"],
  is_deleted: ["is_deleted", "is deleted", "是否删除"],
};

function normalizeHeader(value: string) {
  return value.trim().toLowerCase().replace(/\s+/g, " ");
}

export function getFileKind(file: File) {
  const name = file.name.toLowerCase();
  if (name.endsWith(".xlsx")) return "xlsx";
  if (name.endsWith(".xls")) return "xls";
  if (name.endsWith(".csv")) return "csv";
  if (name.endsWith(".json")) return "json";
  if (name.endsWith(".numbers")) return "numbers";
  return "unknown";
}

export async function parseDatasetFile(file: File, messages: DatasetImportMessages) {
  const kind = getFileKind(file);
  if (kind === "numbers") {
    throw new Error(messages.numbersUnsupported);
  }
  if (kind === "unknown") {
    throw new Error(messages.fileUnsupported);
  }

  if (kind === "json") {
    const text = await file.text();
    const parsed = JSON.parse(text) as unknown;
    const rows: unknown[] | null = Array.isArray(parsed)
      ? parsed
      : typeof parsed === "object" && parsed && Array.isArray((parsed as any).items)
        ? (parsed as any).items
        : null;

    if (!rows) {
      throw new Error(messages.jsonFormatInvalid);
    }
    return rows.map((row) =>
      typeof row === "object" && row ? (row as Record<string, unknown>) : {},
    );
  }

  if (kind === "csv") {
    const text = await file.text();
    const workbook = XLSX.read(text, { type: "string" });
    const sheet = workbook.Sheets[workbook.SheetNames[0]];
    return XLSX.utils.sheet_to_json<Record<string, unknown>>(sheet, { defval: "" });
  }

  const buffer = await file.arrayBuffer();
  const workbook = XLSX.read(buffer, { type: "array" });
  const sheet = workbook.Sheets[workbook.SheetNames[0]];
  return XLSX.utils.sheet_to_json<Record<string, unknown>>(sheet, { defval: "" });
}

export function createAutoFieldMapping(sourceFields: string[]) {
  const mapping: FieldMapping = {};

  sourceFields.forEach((sourceField) => {
    const normalizedSource = normalizeHeader(sourceField);
    const matchedField = datasetItemFields.find((field) => {
      if (normalizeHeader(field) === normalizedSource) {
        return true;
      }
      return fieldAliases[field].some((alias) => normalizeHeader(alias) === normalizedSource);
    });
    mapping[sourceField] = matchedField || "";
  });

  return mapping;
}

function normalizeCellValue(value: unknown) {
  if (value === undefined || value === null) {
    return "";
  }
  return `${value}`.trim();
}

export function buildImportPreview(
  rawRows: Record<string, unknown>[],
  mapping: FieldMapping,
  messages: DatasetImportMessages,
) {
  return rawRows.map<DatasetImportRow>((raw, index) => {
    const normalized: Partial<DatasetItem> = {};
    const errors: string[] = [];

    Object.entries(mapping).forEach(([sourceField, targetField]) => {
      if (!targetField) {
        return;
      }
      const value = raw[sourceField];

      if (targetField === "reference_doc_ids" || targetField === "reference_chunk_ids") {
        normalized[targetField] = splitListField(value as string);
        return;
      }
      if (targetField === "is_deleted") {
        const parsed = parseBooleanLike(value);
        if (parsed === undefined) {
          errors.push(messages.deletedFieldInvalid);
        } else {
          normalized.is_deleted = parsed;
        }
        return;
      }
      (normalized as Record<string, unknown>)[targetField] = normalizeCellValue(value);
    });

    requiredDatasetItemFields.forEach((field) => {
      const value = normalized[field];
      if (!`${value || ""}`.trim()) {
        errors.push(messages.required[field]);
      }
    });

    return {
      rowIndex: index + 1,
      raw,
      normalized,
      errors,
    };
  });
}

export function getMissingRequiredMappings(mapping: FieldMapping) {
  const mappedTargets = new Set(Object.values(mapping).filter(Boolean));
  return requiredDatasetItemFields.filter((field) => !mappedTargets.has(field));
}

export function createTemplateRows(sample: {
  question: string;
  question_type: string;
  ground_truth: string;
  key_points: string;
  reference_context: string;
  reference_doc: string;
  generate_reason: string;
}) {
  return [
    {
      question: sample.question,
      question_type: sample.question_type,
      ground_truth: sample.ground_truth,
      key_points: sample.key_points,
      reference_context: sample.reference_context,
      reference_doc: sample.reference_doc,
      reference_doc_ids: "doc_001",
      reference_chunk_ids: "chunk_001, chunk_002",
      generate_reason: sample.generate_reason,
      is_deleted: false,
    },
  ];
}
