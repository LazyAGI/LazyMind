import type { DatasetItemFormValues } from "../shared";

export type RequiredDatasetItemMessages = {
  question: string;
  question_type: string;
  ground_truth: string;
};

export function splitListField(value?: string | string[]) {
  if (Array.isArray(value)) {
    return value.map((item) => `${item}`.trim()).filter(Boolean);
  }
  return `${value || ""}`
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function joinListField(value?: string[]) {
  return (value || []).join(", ");
}

export function parseBooleanLike(value: unknown) {
  if (typeof value === "boolean") {
    return value;
  }
  const normalized = `${value ?? ""}`.trim().toLowerCase();
  if (!normalized) {
    return false;
  }
  if (["true", "1", "yes", "y", "是"].includes(normalized)) {
    return true;
  }
  if (["false", "0", "no", "n", "否"].includes(normalized)) {
    return false;
  }
  return undefined;
}

export function normalizeItemFormValues(values: DatasetItemFormValues) {
  return {
    case_id: `${values.case_id || ""}`.trim(),
    question: `${values.question || ""}`.trim(),
    question_type: `${values.question_type || ""}`.trim(),
    ground_truth: `${values.ground_truth || ""}`.trim(),
    key_points: `${values.key_points || ""}`.trim(),
    reference_context: `${values.reference_context || ""}`.trim(),
    reference_doc: `${values.reference_doc || ""}`.trim(),
    reference_doc_ids: splitListField(values.reference_doc_ids),
    reference_chunk_ids: splitListField(values.reference_chunk_ids),
    generate_reason: `${values.generate_reason || ""}`.trim(),
    is_deleted: Boolean(values.is_deleted),
  };
}

export function validateRequiredDatasetItem(
  values: Partial<DatasetItemFormValues>,
  messages: RequiredDatasetItemMessages,
) {
  const errors: string[] = [];
  if (!`${values.question || ""}`.trim()) {
    errors.push(messages.question);
  }
  if (!`${values.question_type || ""}`.trim()) {
    errors.push(messages.question_type);
  }
  if (!`${values.ground_truth || ""}`.trim()) {
    errors.push(messages.ground_truth);
  }
  return errors;
}
