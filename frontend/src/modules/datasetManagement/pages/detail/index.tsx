import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  CSSProperties,
  ClipboardEvent as ReactClipboardEvent,
  Key,
  KeyboardEvent as ReactKeyboardEvent,
  MouseEvent as ReactMouseEvent,
  ThHTMLAttributes,
  HTMLAttributes,
} from "react";
import {
  AutoComplete,
  Button,
  Card,
  Checkbox,
  Empty,
  Input,
  Modal,
  Popover,
  Select,
  Space,
  Table,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ArrowLeftOutlined,
  CloseOutlined,
  DeleteOutlined,
  ImportOutlined,
  PlusOutlined,
  SearchOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  batchDeleteDatasetItems,
  createDatasetItem,
  deleteDatasetItem,
  findKnowledgeBaseDocumentById,
  getDataset,
  importDatasetItems,
  listDatasetQuestionTypes,
  listDatasetItems,
  type KnowledgeDocumentOption,
  mergeKnowledgeDocumentOptions,
  searchKnowledgeBaseDocuments,
  updateDatasetItem,
} from "../../api";
import FileViewer from "@/modules/knowledge/components/FileViewer";
import {
  DocumentServiceApi,
  KnowledgeBaseServiceApi,
  SegmentServiceApi,
  normalizeProxyableUrl,
} from "@/modules/knowledge/utils/request";
import {
  ParserConfigTypeEnum,
  type Segment,
} from "@/api/generated/knowledge-client";
import DatasetImportModal from "../../components/DatasetImportModal";
import QuestionTypeSelect from "../../components/QuestionTypeSelect";
import SourceTypeTag from "../../components/SourceTypeTag";
import type {
  DatasetImportResultState,
  DatasetItem,
  DatasetItemFormValues,
  DatasetItemSource,
  DatasetListItem,
} from "../../shared";
import {
  datasetItemFieldI18nKeys,
  formatDateTime,
  sourceLabelI18nKeys,
} from "../../shared";
import {
  joinListField,
  validateRequiredDatasetItem,
} from "../../utils/datasetValidation";
import "../../index.scss";

const { TextArea } = Input;
const NEW_ITEM_ID = "__new_dataset_item__";
const MIN_COLUMN_WIDTH = 88;
const MIN_HEADER_HEIGHT = 40;
const MAX_HEADER_HEIGHT = 96;
const DEFAULT_HEADER_HEIGHT = 44;
const MIN_ROW_HEIGHT = 48;
const MAX_ROW_HEIGHT = 140;
const DEFAULT_ROW_HEIGHT = 64;
const DEFAULT_COLUMN_WIDTHS = {
  question: 240,
  question_type: 130,
  ground_truth: 240,
  key_points: 220,
  reference_context: 260,
  reference_doc: 160,
  is_deleted: 120,
  source: 100,
  updated_at: 150,
  actions: 90,
};

type ResizableColumnKey = keyof typeof DEFAULT_COLUMN_WIDTHS;
type EditableDatasetItemField =
  | "question"
  | "question_type"
  | "ground_truth"
  | "key_points"
  | "reference_context"
  | "reference_doc";
type ActiveEditableCell = {
  itemId: string;
  field: EditableDatasetItemField;
} | null;
type DocumentSearchState = {
  loading: boolean;
  keyword: string;
  options: KnowledgeDocumentOption[];
  nextPageToken?: string;
  totalSize?: number;
};
type ConfigurableColumnKey = Exclude<ResizableColumnKey, "actions" | "is_deleted">;
const REQUIRED_VISIBLE_COLUMN_KEYS: ConfigurableColumnKey[] = [
  "question",
  "question_type",
  "ground_truth",
];
type ReferenceDocumentPreview = {
  datasetId: string;
  documentId: string;
  name: string;
  fileUrl?: string;
};
type ReferenceChunkSelectorState = {
  open: boolean;
  loading: boolean;
  confirming: boolean;
  error: string;
  itemId: string;
  documentName: string;
  documentPreviewUrl: string;
  segmentGroup: string;
  chunks: Segment[];
  selectedChunkIds: string[];
  previewSegment?: Segment;
};
type ReferenceContextChunkPart = {
  type: "chunk";
  id: string;
  content: string;
};
type ReferenceContextTextPart = {
  type: "text";
  content: string;
};
type ReferenceContextPart = ReferenceContextChunkPart | ReferenceContextTextPart;
type ReferenceContextValue = {
  parts: ReferenceContextPart[];
};

function createReferenceChunkSelectorState(): ReferenceChunkSelectorState {
  return {
    open: false,
    loading: false,
    confirming: false,
    error: "",
    itemId: "",
    documentName: "",
    documentPreviewUrl: "",
    segmentGroup: "",
    chunks: [],
    selectedChunkIds: [],
  };
}

const CONFIGURABLE_COLUMN_OPTIONS: Array<{
  labelKey: string;
  value: ConfigurableColumnKey;
  disabled?: boolean;
}> = [
  { labelKey: datasetItemFieldI18nKeys.question, value: "question", disabled: true },
  { labelKey: datasetItemFieldI18nKeys.question_type, value: "question_type", disabled: true },
  { labelKey: datasetItemFieldI18nKeys.ground_truth, value: "ground_truth", disabled: true },
  { labelKey: datasetItemFieldI18nKeys.key_points, value: "key_points" },
  { labelKey: datasetItemFieldI18nKeys.reference_doc, value: "reference_doc" },
  { labelKey: datasetItemFieldI18nKeys.reference_context, value: "reference_context" },
  { labelKey: "datasetManagement.fields.source", value: "source" },
  { labelKey: "datasetManagement.fields.updatedAt", value: "updated_at" },
];

const DEFAULT_VISIBLE_COLUMN_KEYS = [
  ...CONFIGURABLE_COLUMN_OPTIONS.map((option) => option.value),
];

function normalizeVisibleColumnKeys(keys: ConfigurableColumnKey[]) {
  return Array.from(new Set([...REQUIRED_VISIBLE_COLUMN_KEYS, ...keys]));
}

const editableFieldColumnMap: Record<EditableDatasetItemField, ConfigurableColumnKey> = {
  question: "question",
  question_type: "question_type",
  ground_truth: "ground_truth",
  key_points: "key_points",
  reference_context: "reference_context",
  reference_doc: "reference_doc",
};

const renderRequiredColumnTitle = (title: string) => (
  <span className="dataset-required-column-title">
    <span className="dataset-required-column-mark" aria-hidden="true">*</span>
    {title}
  </span>
);

type ResizableHeaderCellProps = ThHTMLAttributes<HTMLTableCellElement> & {
  columnKey?: ResizableColumnKey;
  columnWidth?: number;
  headerHeight?: number;
  onResizeColumn?: (
    columnKey: ResizableColumnKey,
    startX: number,
    startWidth: number,
  ) => void;
  onResizeHeader?: (startY: number, startHeight: number) => void;
};

function ResizableHeaderCell({
  columnKey,
  columnWidth,
  headerHeight,
  onResizeColumn,
  onResizeHeader,
  children,
  style,
  ...rest
}: ResizableHeaderCellProps) {
  const handleColumnResizeStart = (event: ReactMouseEvent<HTMLSpanElement>) => {
    if (!columnKey || !columnWidth || !onResizeColumn) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    onResizeColumn(columnKey, event.clientX, columnWidth);
  };

  const handleHeaderResizeStart = (event: ReactMouseEvent<HTMLSpanElement>) => {
    if (!headerHeight || !onResizeHeader) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    onResizeHeader(event.clientY, headerHeight);
  };

  return (
    <th {...rest} style={{ ...style, height: headerHeight }}>
      <div className="dataset-resizable-header-content">{children}</div>
      {columnKey ? (
        <span
          aria-hidden="true"
          className="dataset-column-resize-handle"
          onMouseDown={handleColumnResizeStart}
        />
      ) : null}
      <span
        aria-hidden="true"
        className="dataset-header-height-resize-handle"
        onMouseDown={handleHeaderResizeStart}
      />
    </th>
  );
}

type ResizableBodyRowProps = HTMLAttributes<HTMLTableRowElement> & {
  rowHeight?: number;
  onResizeRow?: (startY: number, startHeight: number) => void;
};

function ResizableBodyRow({
  rowHeight,
  onResizeRow,
  children,
  className,
  style,
  ...rest
}: ResizableBodyRowProps) {
  const handleRowResizeStart = (event: ReactMouseEvent<HTMLTableRowElement>) => {
    if (!rowHeight || !onResizeRow) {
      return;
    }
    const rowRect = event.currentTarget.getBoundingClientRect();
    if (event.clientY < rowRect.bottom - 10) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    onResizeRow(event.clientY, rowHeight);
  };

  return (
    <tr
      {...rest}
      className={`${className || ""} dataset-resizable-row`.trim()}
      style={style}
      onMouseDown={handleRowResizeStart}
    >
      {children}
    </tr>
  );
}

const tableComponents = {
  header: {
    cell: ResizableHeaderCell,
  },
  body: {
    row: ResizableBodyRow,
  },
};

function normalizeReferenceContextText(value?: string) {
  return `${value || ""}`.replace(/\r\n/g, "\n").trim();
}

function parseReferenceContextValue(raw?: string): ReferenceContextValue {
  const value = `${raw || ""}`.trim();
  if (!value) {
    return { parts: [] };
  }
  try {
    const parsed = JSON.parse(value) as {
      type?: string;
      parts?: Array<Partial<ReferenceContextPart>>;
    };
    if (parsed?.type === "reference_context" && Array.isArray(parsed.parts)) {
      return {
        parts: parsed.parts
          .map((part): ReferenceContextPart | null => {
            if (part?.type === "chunk") {
              const content = normalizeReferenceContextText(part.content);
              if (!content) {
                return null;
              }
              return {
                type: "chunk",
                id: `${part.id || ""}`.trim(),
                content,
              };
            }
            if (part?.type === "text") {
              const content = normalizeReferenceContextText(part.content);
              return content ? { type: "text", content } : null;
            }
            return null;
          })
          .filter((part): part is ReferenceContextPart => Boolean(part)),
      };
    }
  } catch {
    // Old rows are plain text; keep them editable as user text.
  }
  return { parts: [{ type: "text", content: value }] };
}

function serializeReferenceContextValue(value: ReferenceContextValue) {
  const parts = value.parts
    .map((part): ReferenceContextPart | null => {
      if (part.type === "chunk") {
        const content = normalizeReferenceContextText(part.content);
        return content ? { type: "chunk", id: part.id, content } : null;
      }
      const content = normalizeReferenceContextText(part.content);
      return content ? { type: "text", content } : null;
    })
    .filter((part): part is ReferenceContextPart => Boolean(part));

  if (parts.length === 0) {
    return "";
  }
  if (parts.length === 1 && parts[0].type === "text") {
    return parts[0].content;
  }
  return JSON.stringify({
    type: "reference_context",
    version: 1,
    parts,
  });
}

function referenceContextEditorValue(raw?: string): ReferenceContextValue {
  const parts = parseReferenceContextValue(raw).parts;
  if (!parts.some((part) => part.type === "chunk")) {
    return { parts };
  }

  const editorParts: ReferenceContextPart[] = [];
  parts.forEach((part, index) => {
    if (part.type === "text") {
      editorParts.push(part);
      return;
    }
    const previousPart = editorParts[editorParts.length - 1];
    if (!previousPart || previousPart.type !== "text") {
      editorParts.push({ type: "text", content: "" });
    }
    editorParts.push(part);
    if (parts[index + 1]?.type !== "text") {
      editorParts.push({ type: "text", content: "" });
    }
  });
  return { parts: editorParts };
}

function buildReferenceContextWithChunks(raw: string | undefined, chunks: ReferenceContextChunkPart[]) {
  const parts = parseReferenceContextValue(raw).parts;
  const firstChunkIndex = parts.findIndex((part) => part.type === "chunk");
  if (firstChunkIndex < 0) {
    return serializeReferenceContextValue({ parts: [...parts, ...chunks] });
  }

  let lastChunkIndex = firstChunkIndex;
  parts.forEach((part, index) => {
    if (part.type === "chunk") {
      lastChunkIndex = index;
    }
  });
  const leadingParts = parts.slice(0, firstChunkIndex);
  const trailingParts = parts.slice(lastChunkIndex + 1);
  return serializeReferenceContextValue({
    parts: [...leadingParts, ...chunks, ...trailingParts],
  });
}

function buildReferenceContextWithChunksAtTextPart(
  raw: string | undefined,
  chunks: ReferenceContextChunkPart[],
  partIndex?: number,
) {
  const value = referenceContextEditorValue(raw);
  if (partIndex === undefined && !value.parts.some((part) => part.type === "chunk")) {
    return serializeReferenceContextValue({
      parts: [...value.parts, ...chunks, { type: "text", content: "" }],
    });
  }
  if (partIndex === undefined) {
    return buildReferenceContextWithChunks(raw, chunks);
  }
  if (value.parts[partIndex]?.type !== "text") {
    return buildReferenceContextWithChunks(raw, chunks);
  }
  const parts: ReferenceContextPart[] = [];
  value.parts.forEach((part, index) => {
    if (index !== partIndex) {
      parts.push(part);
      return;
    }
    if (part.content) {
      parts.push(part);
    }
    parts.push(...chunks, { type: "text", content: "" });
  });
  return serializeReferenceContextValue({ parts });
}

function referenceContextChunkIDs(raw?: string) {
  return parseReferenceContextValue(raw).parts
    .filter((part): part is ReferenceContextChunkPart => part.type === "chunk")
    .map((part) => part.id)
    .filter(Boolean);
}

function removeReferenceContextPart(raw: string | undefined, partIndex: number) {
  return serializeReferenceContextValue({
    parts: referenceContextEditorValue(raw).parts.filter((_, index) => index !== partIndex),
  });
}

function removeReferenceContextChunks(raw: string | undefined) {
  return serializeReferenceContextValue({
    parts: referenceContextEditorValue(raw).parts.filter((part) => part.type !== "chunk"),
  });
}

function renderReferenceContextParts(
  raw: string | undefined,
  placeholder: string,
  formatChunkLabel: (index: number) => string,
) {
  const value = parseReferenceContextValue(raw);
  if (value.parts.length === 0) {
    return <span className="dataset-inline-placeholder">{placeholder}</span>;
  }

  let chunkIndex = 0;
  return (
    <span className="dataset-reference-context-preview">
      {value.parts.map((part, index) => {
        if (part.type === "chunk") {
          chunkIndex += 1;
          return (
            <Popover
              key={`${part.id || index}-${chunkIndex}`}
              trigger="hover"
              placement="topLeft"
              content={
                <div className="dataset-reference-context-popover">
                  {part.content}
                </div>
              }
            >
              <span className="dataset-reference-context-chip">
                {formatChunkLabel(chunkIndex)}
              </span>
            </Popover>
          );
        }
        return (
          <span key={`text-${index}`} className="dataset-reference-context-text">
            {part.content}
          </span>
        );
      })}
    </span>
  );
}

function renderReferenceDocumentTag(value: string | undefined, placeholder: string) {
  const text = `${value || ""}`.trim();
  if (!text) {
    return <span className="dataset-inline-placeholder">{placeholder}</span>;
  }
  return <span className="dataset-reference-doc-tag">{text}</span>;
}

function referenceContextPartsFromEditor(root: HTMLDivElement): ReferenceContextPart[] {
  const parts: ReferenceContextPart[] = [];
  const appendText = (value: string) => {
    const content = value.replace(/\u200b/g, "");
    if (!content) {
      return;
    }
    const lastPart = parts[parts.length - 1];
    if (lastPart?.type === "text") {
      lastPart.content += content;
      return;
    }
    parts.push({ type: "text", content });
  };
  const appendLineBreak = () => {
    const lastPart = parts[parts.length - 1];
    if (lastPart?.type === "text" && !lastPart.content.endsWith("\n")) {
      lastPart.content += "\n";
    }
  };
  const walk = (node: ChildNode) => {
    if (node.nodeType === Node.TEXT_NODE) {
      appendText(node.textContent || "");
      return;
    }
    if (!(node instanceof HTMLElement)) {
      return;
    }
    if (node.dataset.referenceContextPart === "chunk") {
      const content = normalizeReferenceContextText(node.dataset.content);
      if (content) {
        parts.push({
          type: "chunk",
          id: `${node.dataset.id || ""}`.trim(),
          content,
        });
      }
      return;
    }
    if (node.tagName === "BR") {
      appendText("\n");
      return;
    }
    const isBlock = node.tagName === "DIV" || node.tagName === "P";
    if (isBlock && parts.length > 0) {
      appendLineBreak();
    }
    node.childNodes.forEach(walk);
    if (isBlock) {
      appendLineBreak();
    }
  };
  root.childNodes.forEach(walk);
  return parts;
}

function placeCaretAtEnd(element: HTMLElement) {
  const selection = window.getSelection();
  if (!selection) {
    return;
  }
  const range = document.createRange();
  range.selectNodeContents(element);
  range.collapse(false);
  selection.removeAllRanges();
  selection.addRange(range);
}

function getReferenceContextEditorValue(editor: HTMLDivElement) {
  return serializeReferenceContextValue({
    parts: referenceContextPartsFromEditor(editor),
  });
}

function escapeHtml(value?: string) {
  return `${value || ""}`
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function escapeHtmlAttribute(value?: string) {
  return escapeHtml(value)
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")
    .replace(/\r/g, "&#13;")
    .replace(/\n/g, "&#10;");
}

function buildReferenceContextEditorHtml(
  value: string,
  formatChunkLabel: (index: number) => string,
  formatDeleteChunkAria: (index: number) => string,
) {
  // contentEditable mutates children directly, so keep editor internals out of React reconciliation.
  const editorValue = referenceContextEditorValue(value);
  let chunkIndex = 0;

  return editorValue.parts
    .map((part, index) => {
      if (part.type === "chunk") {
        chunkIndex += 1;
        return [
          `<span contenteditable="false"`,
          ` data-reference-context-part="chunk"`,
          ` data-reference-context-part-index="${index}"`,
          ` data-id="${escapeHtmlAttribute(part.id)}"`,
          ` data-content="${escapeHtmlAttribute(part.content)}"`,
          ` title="${escapeHtmlAttribute(part.content)}"`,
          ` class="dataset-reference-context-chip">`,
          escapeHtml(formatChunkLabel(chunkIndex)),
          `<button type="button"`,
          ` class="dataset-reference-context-chip-remove"`,
          ` aria-label="${escapeHtmlAttribute(formatDeleteChunkAria(chunkIndex))}"`,
          ` data-reference-context-remove-part="${index}">&times;</button>`,
          `</span>`,
        ].join("");
      }

      return [
        `<span data-reference-context-part-index="${index}"`,
        ` data-reference-context-text-index="${index}"`,
        ` class="dataset-reference-context-editor-text">`,
        escapeHtml(part.content) || "&#8203;",
        `</span>`,
      ].join("");
    })
    .join("");
}

function ReferenceContextInlineEditor({
  value,
  placeholder,
  autoFocus,
  onChange,
  onBlur,
  onInsertIndexChange,
  onRemovePart,
  formatChunkLabel,
  formatDeleteChunkAria,
}: {
  value: string;
  placeholder: string;
  autoFocus?: boolean;
  onChange: (value: string) => void;
  onBlur: () => void;
  onInsertIndexChange: (index?: number) => void;
  onRemovePart: (index: number) => void;
  formatChunkLabel: (index: number) => string;
  formatDeleteChunkAria: (index: number) => string;
}) {
  const editorRef = useRef<HTMLDivElement | null>(null);
  const editorValue = referenceContextEditorValue(value);

  useLayoutEffect(() => {
    const editor = editorRef.current;
    if (!editor) {
      return;
    }

    const currentValue = getReferenceContextEditorValue(editor);
    if (currentValue !== value) {
      editor.innerHTML = buildReferenceContextEditorHtml(
        value,
        formatChunkLabel,
        formatDeleteChunkAria,
      );
    }
    editor.classList.toggle("is-empty", !value);

    if (autoFocus && document.activeElement !== editor) {
      editor.focus();
      placeCaretAtEnd(editor);
    }
  }, [autoFocus, formatChunkLabel, formatDeleteChunkAria, value]);

  const updateInsertIndex = (target: EventTarget | null) => {
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const textNode = target.closest<HTMLElement>("[data-reference-context-text-index]");
    if (!textNode) {
      return;
    }
    const index = Number(textNode.dataset.referenceContextTextIndex);
    if (Number.isFinite(index)) {
      onInsertIndexChange(index);
    }
  };

  const handleInput = () => {
    const editor = editorRef.current;
    if (!editor) {
      return;
    }
    const nextValue = getReferenceContextEditorValue(editor);
    editor.classList.toggle("is-empty", !nextValue);
    onChange(nextValue);
  };

  const handlePaste = (event: ReactClipboardEvent<HTMLDivElement>) => {
    event.preventDefault();
    document.execCommand("insertText", false, event.clipboardData.getData("text/plain"));
  };

  const partIndexFromElement = (element: HTMLElement | null) => {
    if (!element) {
      return undefined;
    }
    const partNode = element.closest<HTMLElement>("[data-reference-context-part-index]");
    const index = Number(partNode?.dataset.referenceContextPartIndex);
    return Number.isFinite(index) ? index : undefined;
  };

  const removePartIndexFromTarget = (target: EventTarget | null) => {
    if (!(target instanceof HTMLElement)) {
      return undefined;
    }
    const removeButton = target.closest<HTMLElement>(
      "[data-reference-context-remove-part]",
    );
    const index = Number(removeButton?.dataset.referenceContextRemovePart);
    return Number.isFinite(index) ? index : undefined;
  };

  const findAdjacentChunkIndex = (forward: boolean) => {
    const selection = window.getSelection();
    const editor = editorRef.current;
    if (!selection || !editor || selection.rangeCount === 0 || !selection.isCollapsed) {
      return undefined;
    }
    const range = selection.getRangeAt(0);
    let currentNode: Node | null = range.startContainer;
    if (!editor.contains(currentNode)) {
      return undefined;
    }
    if (currentNode.nodeType === Node.TEXT_NODE) {
      currentNode = currentNode.parentElement;
    }
    if (!(currentNode instanceof HTMLElement)) {
      return undefined;
    }
    const currentIndex = partIndexFromElement(currentNode);
    if (currentIndex === undefined) {
      return undefined;
    }
    const currentPart = editorValue.parts[currentIndex];
    if (currentPart?.type === "chunk") {
      return currentIndex;
    }
    if (currentPart?.type !== "text") {
      return undefined;
    }
    const offset = range.startOffset;
    const textLength = currentPart.content.length;
    if (!forward && offset > 0) {
      return undefined;
    }
    if (forward && offset < textLength) {
      return undefined;
    }
    const targetIndex = forward ? currentIndex + 1 : currentIndex - 1;
    return editorValue.parts[targetIndex]?.type === "chunk" ? targetIndex : undefined;
  };

  const handleKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Backspace" && event.key !== "Delete") {
      return;
    }
    const chunkIndexToRemove = findAdjacentChunkIndex(event.key === "Delete");
    if (chunkIndexToRemove === undefined) {
      return;
    }
    event.preventDefault();
    onRemovePart(chunkIndexToRemove);
  };

  const isEmpty = editorValue.parts.length === 0;
  return (
    <div
      ref={editorRef}
      className={`dataset-reference-context-editor${isEmpty ? " is-empty" : ""}`}
      contentEditable
      suppressContentEditableWarning
      role="textbox"
      aria-label={placeholder}
      data-placeholder={placeholder}
      onInput={handleInput}
      onBlur={onBlur}
      onFocus={(event) => {
        onInsertIndexChange(0);
        updateInsertIndex(event.target);
      }}
      onKeyUp={(event) => updateInsertIndex(event.target)}
      onKeyDown={handleKeyDown}
      onMouseDown={(event) => {
        if (removePartIndexFromTarget(event.target) !== undefined) {
          event.preventDefault();
          event.stopPropagation();
        }
      }}
      onClick={(event) => {
        const index = removePartIndexFromTarget(event.target);
        if (index === undefined) {
          return;
        }
        event.preventDefault();
        event.stopPropagation();
        onRemovePart(index);
      }}
      onMouseUp={(event) => updateInsertIndex(event.target)}
      onPaste={handlePaste}
    />
  );
}

function createItemDraft(item?: DatasetItem): DatasetItemFormValues {
  return {
    case_id: item?.case_id || "",
    question: item?.question || "",
    question_type: item?.question_type || "",
    ground_truth: item?.ground_truth || "",
    key_points: item?.key_points || "",
    reference_context: item?.reference_context || "",
    reference_doc: item?.reference_doc || "",
    reference_doc_ids: joinListField(item?.reference_doc_ids),
    reference_chunk_ids: joinListField(item?.reference_chunk_ids),
    generate_reason: item?.generate_reason || "",
    is_deleted: Boolean(item?.is_deleted),
  };
}

function mergeHiddenItemFields(
  item: DatasetItem,
  values: DatasetItemFormValues,
): DatasetItemFormValues {
  const currentReferenceDocIDs = joinListField(item.reference_doc_ids);
  const nextReferenceDocIDs = values.reference_doc_ids || "";
  const currentReferenceContext = `${item.reference_context || ""}`.trim();
  const nextReferenceContext = `${values.reference_context || ""}`.trim();
  const referenceContextChanged = nextReferenceContext !== currentReferenceContext;
  const referenceDocChanged =
    `${values.reference_doc || ""}`.trim() !== `${item.reference_doc || ""}`.trim() ||
    nextReferenceDocIDs.trim() !== currentReferenceDocIDs.trim();
  const currentReferenceChunkIDs = joinListField(item.reference_chunk_ids);
  const referenceChunkIdsChanged =
    `${values.reference_chunk_ids ?? currentReferenceChunkIDs}`.trim() !==
    `${currentReferenceChunkIDs}`.trim();

  return {
    ...values,
    case_id: item.case_id || values.case_id,
    reference_doc_ids: referenceDocChanged
      ? nextReferenceDocIDs
      : nextReferenceDocIDs || currentReferenceDocIDs,
    reference_chunk_ids:
      referenceDocChanged || referenceContextChanged || referenceChunkIdsChanged
        ? (values.reference_chunk_ids ?? "")
        : currentReferenceChunkIDs,
    is_deleted: Boolean(item.is_deleted),
  };
}

export default function DatasetDetailPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { datasetId = "" } = useParams();
  const [dataset, setDataset] = useState<DatasetListItem | null>(null);
  const [items, setItems] = useState<DatasetItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [keyword, setKeyword] = useState("");
  const [questionType, setQuestionType] = useState<string>();
  const [source, setSource] = useState<DatasetItemSource>();
  const [pagination, setPagination] = useState({ current: 1, pageSize: 10 });
  const [total, setTotal] = useState(0);
  const [questionTypeOptions, setQuestionTypeOptions] = useState<string[]>([]);
  const [selectedRowKeys, setSelectedRowKeys] = useState<Key[]>([]);
  const [drafts, setDrafts] = useState<Record<string, DatasetItemFormValues>>({});
  const [dirtyItemIds, setDirtyItemIds] = useState<string[]>([]);
  const [activeCell, setActiveCell] = useState<ActiveEditableCell>(null);
  const [importModalOpen, setImportModalOpen] = useState(false);
  const [newItemVisible, setNewItemVisible] = useState(false);
  const [columnWidths, setColumnWidths] =
    useState<Record<ResizableColumnKey, number>>(DEFAULT_COLUMN_WIDTHS);
  const [visibleColumnKeys, setVisibleColumnKeys] = useState<ConfigurableColumnKey[]>(
    DEFAULT_VISIBLE_COLUMN_KEYS,
  );
  const columnSettingOptions = useMemo(
    () =>
      CONFIGURABLE_COLUMN_OPTIONS.map((option) => ({
        label: t(option.labelKey),
        value: option.value,
        disabled: option.disabled,
      })),
    [t],
  );
  const effectiveVisibleColumnKeys = useMemo(
    () => normalizeVisibleColumnKeys(visibleColumnKeys),
    [visibleColumnKeys],
  );
  const [headerHeight, setHeaderHeight] = useState(DEFAULT_HEADER_HEIGHT);
  const [rowHeight, setRowHeight] = useState(DEFAULT_ROW_HEIGHT);
  const [documentSearchState, setDocumentSearchState] = useState<
    Record<string, DocumentSearchState>
  >({});
  const [referenceChunkSelector, setReferenceChunkSelector] =
    useState<ReferenceChunkSelectorState>(createReferenceChunkSelectorState);
  const documentSearchPaginationRequestRef = useRef<Record<string, string>>({});
  const documentSearchRequestRef = useRef<Record<string, number>>({});
  const referenceChunkSelectorRequestRef = useRef(0);
  const referenceDocumentCacheRef = useRef<Record<string, ReferenceDocumentPreview>>({});
  const [referenceDocumentCacheVersion, setReferenceDocumentCacheVersion] = useState(0);
  const referenceContextInsertIndexRef = useRef<Record<string, number | undefined>>({});
  const referenceContextEditingValueRef = useRef<Record<string, string | undefined>>({});
  const referenceContextEditingDirtyRef = useRef<Record<string, boolean | undefined>>({});
  const pendingNewItemCellActivationRef = useRef<ActiveEditableCell>(null);
  const requiredItemMessages = useMemo(
    () => ({
      question: t("datasetManagement.validation.questionRequired"),
      question_type: t("datasetManagement.validation.questionTypeRequired"),
      ground_truth: t("datasetManagement.validation.groundTruthRequired"),
    }),
    [t],
  );

  const resetReferenceChunkSelector = useCallback(() => {
    referenceChunkSelectorRequestRef.current += 1;
    setReferenceChunkSelector(createReferenceChunkSelectorState());
  }, []);

  const clearReferenceContextRuntimeState = useCallback((itemId: string) => {
    delete referenceContextInsertIndexRef.current[itemId];
    delete referenceContextEditingValueRef.current[itemId];
    delete referenceContextEditingDirtyRef.current[itemId];
  }, []);

  const clearReferenceDocumentRuntimeState = useCallback((itemId: string) => {
    delete documentSearchPaginationRequestRef.current[itemId];
    delete documentSearchRequestRef.current[itemId];
    setDocumentSearchState((current) => {
      if (!current[itemId]) {
        return current;
      }
      const next = { ...current };
      delete next[itemId];
      return next;
    });
  }, []);

  const clearItemRuntimeState = useCallback(
    (itemId: string) => {
      clearReferenceContextRuntimeState(itemId);
      if (itemId === NEW_ITEM_ID) {
        pendingNewItemCellActivationRef.current = null;
      }
      clearReferenceDocumentRuntimeState(itemId);
    },
    [clearReferenceContextRuntimeState, clearReferenceDocumentRuntimeState],
  );

  const clearAllItemRuntimeState = useCallback(() => {
    documentSearchPaginationRequestRef.current = {};
    documentSearchRequestRef.current = {};
    referenceContextInsertIndexRef.current = {};
    referenceContextEditingValueRef.current = {};
    referenceContextEditingDirtyRef.current = {};
    pendingNewItemCellActivationRef.current = null;
    referenceChunkSelectorRequestRef.current += 1;
    setDocumentSearchState({});
    setReferenceChunkSelector(createReferenceChunkSelectorState());
  }, []);

  const handleColumnResize = useCallback(
    (columnKey: ResizableColumnKey, startX: number, startWidth: number) => {
      const handleMouseMove = (event: MouseEvent) => {
        const nextWidth = Math.max(
          MIN_COLUMN_WIDTH,
          Math.round(startWidth + event.clientX - startX),
        );
        setColumnWidths((current) => ({
          ...current,
          [columnKey]: nextWidth,
        }));
      };
      const handleMouseUp = () => {
        document.removeEventListener("mousemove", handleMouseMove);
        document.removeEventListener("mouseup", handleMouseUp);
        document.body.classList.remove("dataset-table-column-is-resizing");
      };

      document.body.classList.add("dataset-table-column-is-resizing");
      document.addEventListener("mousemove", handleMouseMove);
      document.addEventListener("mouseup", handleMouseUp);
    },
    [],
  );

  const handleRowResize = useCallback((startY: number, startHeight: number) => {
    const handleMouseMove = (event: MouseEvent) => {
      const nextHeight = Math.min(
        MAX_ROW_HEIGHT,
        Math.max(MIN_ROW_HEIGHT, Math.round(startHeight + event.clientY - startY)),
      );
      setRowHeight(nextHeight);
    };
    const handleMouseUp = () => {
      document.removeEventListener("mousemove", handleMouseMove);
      document.removeEventListener("mouseup", handleMouseUp);
      document.body.classList.remove("dataset-table-row-is-resizing");
    };

    document.body.classList.add("dataset-table-row-is-resizing");
    document.addEventListener("mousemove", handleMouseMove);
    document.addEventListener("mouseup", handleMouseUp);
  }, []);

  const handleHeaderResize = useCallback((startY: number, startHeight: number) => {
    const handleMouseMove = (event: MouseEvent) => {
      const nextHeight = Math.min(
        MAX_HEADER_HEIGHT,
        Math.max(MIN_HEADER_HEIGHT, Math.round(startHeight + event.clientY - startY)),
      );
      setHeaderHeight(nextHeight);
    };
    const handleMouseUp = () => {
      document.removeEventListener("mousemove", handleMouseMove);
      document.removeEventListener("mouseup", handleMouseUp);
      document.body.classList.remove("dataset-table-row-is-resizing");
    };

    document.body.classList.add("dataset-table-row-is-resizing");
    document.addEventListener("mousemove", handleMouseMove);
    document.addEventListener("mouseup", handleMouseUp);
  }, []);

  const getHeaderCellProps = useCallback(
    (columnKey: ResizableColumnKey) => ({
      columnKey,
      columnWidth: columnWidths[columnKey],
      headerHeight,
      onResizeColumn: handleColumnResize,
      onResizeHeader: handleHeaderResize,
    }) as ResizableHeaderCellProps,
    [columnWidths, handleColumnResize, handleHeaderResize, headerHeight],
  );

  useEffect(() => {
    clearAllItemRuntimeState();
    referenceDocumentCacheRef.current = {};
    setReferenceDocumentCacheVersion((version) => version + 1);
    setSelectedRowKeys([]);
    setDirtyItemIds([]);
    setNewItemVisible(false);
    setActiveCell(null);
  }, [clearAllItemRuntimeState, datasetId]);

  const loadDetail = useCallback(async () => {
    if (!datasetId) {
      return;
    }
    setLoading(true);
    try {
      const [datasetDetail, itemList, remoteQuestionTypes] = await Promise.all([
        getDataset(datasetId),
        listDatasetItems(datasetId, {
          keyword,
          question_type: questionType,
          source,
          page: pagination.current,
          pageSize: pagination.pageSize,
        }),
        listDatasetQuestionTypes(datasetId).catch(() => []),
      ]);
      setDataset(datasetDetail);
      setItems(itemList.items);
      setTotal(itemList.total);
      setQuestionTypeOptions(remoteQuestionTypes);
    } catch (error: any) {
      message.error(error?.message || t("datasetManagement.detail.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [datasetId, keyword, pagination.current, pagination.pageSize, questionType, source, t]);

  useEffect(() => {
    void loadDetail();
  }, [loadDetail]);

  useEffect(() => {
    setDrafts((current) => {
      const nextDrafts: Record<string, DatasetItemFormValues> = {};
      items.forEach((item) => {
        nextDrafts[item.id] = dirtyItemIds.includes(item.id)
          ? current[item.id] || createItemDraft(item)
          : createItemDraft(item);
      });
      if (newItemVisible) {
        nextDrafts[NEW_ITEM_ID] = current[NEW_ITEM_ID] || createItemDraft();
      }
      return nextDrafts;
    });
  }, [dirtyItemIds, items, newItemVisible]);

  useEffect(() => {
    if (
      activeCell &&
      !effectiveVisibleColumnKeys.includes(editableFieldColumnMap[activeCell.field])
    ) {
      setActiveCell(null);
    }
  }, [activeCell, effectiveVisibleColumnKeys]);

  const confirmDiscardDirty = () =>
    new Promise<boolean>((resolve) => {
      if (dirtyItemIds.length === 0 && !newItemVisible) {
        resolve(true);
        return;
      }
      Modal.confirm({
        title: t("datasetManagement.detail.unsavedTitle"),
        content: t("datasetManagement.detail.unsavedContent"),
        okText: t("datasetManagement.detail.continue"),
        cancelText: t("common.cancel"),
        onOk: () => resolve(true),
        onCancel: () => resolve(false),
      });
    });

  const handleFilterSearch = async () => {
    const canContinue = await confirmDiscardDirty();
    if (!canContinue) {
      return;
    }
    clearAllItemRuntimeState();
    setDirtyItemIds([]);
    setNewItemVisible(false);
    setActiveCell(null);
    setPagination((current) => ({ ...current, current: 1 }));
    await loadDetail();
  };

  const handleAddItem = async () => {
    const canContinue = await confirmDiscardDirty();
    if (!canContinue) {
      return;
    }
    clearAllItemRuntimeState();
    setNewItemVisible(true);
    setDirtyItemIds([NEW_ITEM_ID]);
    setDrafts({ [NEW_ITEM_ID]: createItemDraft() });
    setActiveCell({ itemId: NEW_ITEM_ID, field: "question" });
    setPagination((current) => ({ ...current, current: 1 }));
  };

  const handleDraftChange = (
    item: DatasetItem,
    field: EditableDatasetItemField,
    value?: string,
  ) => {
    setDrafts((current) => ({
      ...current,
      [item.id]: {
        ...(current[item.id] || createItemDraft(item.id === NEW_ITEM_ID ? undefined : item)),
        [field]: value || "",
      },
    }));
    setDirtyItemIds((current) =>
      current.includes(item.id) ? current : [...current, item.id],
    );
  };

  const buildItemDraftForSave = useCallback(
    (item: DatasetItem): DatasetItemFormValues => {
      const editingReferenceContext = referenceContextEditingValueRef.current[item.id];
      const baseDraft = drafts[item.id] || createItemDraft(item.id === NEW_ITEM_ID ? undefined : item);
      if (editingReferenceContext === undefined) {
        return baseDraft;
      }
      return {
        ...baseDraft,
        reference_context: editingReferenceContext,
        reference_chunk_ids: referenceContextChunkIDs(editingReferenceContext).join(", "),
      };
    },
    [drafts],
  );

  const findDatasetItemForSave = useCallback(
    (itemId: string): DatasetItem | undefined => {
      if (itemId === NEW_ITEM_ID) {
        if (!newItemVisible) {
          return undefined;
        }
        return {
          id: NEW_ITEM_ID,
          dataset_id: datasetId,
          case_id: "",
          question: t("datasetManagement.detail.newSample"),
          question_type: "",
          ground_truth: "",
          source: "manual",
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
          created_by: t("datasetManagement.detail.currentUser"),
        };
      }
      return items.find((item) => item.id === itemId);
    },
    [datasetId, items, newItemVisible, t],
  );

  const handleSaveItem = useCallback(
    async (
      itemId: string,
      values: DatasetItemFormValues,
      successMessage?: string,
    ) => {
      const validationErrors = validateRequiredDatasetItem(values, requiredItemMessages);
      if (validationErrors.length > 0) {
        message.warning(validationErrors[0]);
        return false;
      }
      setSaving(true);
      try {
        if (itemId === NEW_ITEM_ID) {
          await createDatasetItem(datasetId, values);
          message.success(t("datasetManagement.detail.sampleAdded"));
          setNewItemVisible(false);
          setActiveCell(null);
        } else {
          const currentItem = items.find((item) => item.id === itemId);
          await updateDatasetItem(
            datasetId,
            itemId,
            currentItem ? mergeHiddenItemFields(currentItem, values) : values,
          );
          message.success(successMessage || t("datasetManagement.detail.sampleSaved"));
        }
        if (activeCell?.itemId === itemId) {
          setActiveCell(null);
        }
        clearItemRuntimeState(itemId);
        setDirtyItemIds((current) => current.filter((id) => id !== itemId));
        await loadDetail();
        return true;
      } catch (error: any) {
        message.error(error?.message || t("datasetManagement.detail.saveFailed"));
        return false;
      } finally {
        setSaving(false);
      }
    },
    [activeCell, clearItemRuntimeState, datasetId, items, loadDetail, requiredItemMessages, t],
  );

  const handleAutoSaveItem = useCallback(
    async (item: DatasetItem) => {
      const pendingNewItemCellActivation = pendingNewItemCellActivationRef.current;
      if (
        item.id === NEW_ITEM_ID &&
        pendingNewItemCellActivation?.itemId === NEW_ITEM_ID
      ) {
        pendingNewItemCellActivationRef.current = null;
        return;
      }

      const draft = buildItemDraftForSave(item);
      const referenceContextEditingDirty = Boolean(referenceContextEditingDirtyRef.current[item.id]);
      if (
        item.id !== NEW_ITEM_ID &&
        !dirtyItemIds.includes(item.id) &&
        !referenceContextEditingDirty
      ) {
        setActiveCell(null);
        return;
      }
      if (item.id === NEW_ITEM_ID && validateRequiredDatasetItem(draft, requiredItemMessages).length > 0) {
        setActiveCell(null);
        return;
      }
      await handleSaveItem(item.id, draft);
    },
    [buildItemDraftForSave, dirtyItemIds, handleSaveItem, requiredItemMessages],
  );

  const activateEditableCell = useCallback(
    async (record: DatasetItem, field: EditableDatasetItemField) => {
      if (activeCell?.itemId === record.id && activeCell.field === field) {
        return;
      }
      if (activeCell) {
        const previousItem = findDatasetItemForSave(activeCell.itemId);
        if (previousItem) {
          await handleAutoSaveItem(previousItem);
        }
      }
      setActiveCell({ itemId: record.id, field });
      if (record.id === NEW_ITEM_ID) {
        window.setTimeout(() => {
          pendingNewItemCellActivationRef.current = null;
        }, 0);
      }
    },
    [activeCell, findDatasetItemForSave, handleAutoSaveItem],
  );

  const updateDocumentSearchState = (
    itemId: string,
    patch: Partial<DocumentSearchState>,
  ) => {
    setDocumentSearchState((current) => ({
      ...current,
      [itemId]: {
        ...(current[itemId] || {}),
        loading: current[itemId]?.loading || false,
        keyword: current[itemId]?.keyword || "",
        options: current[itemId]?.options || [],
        nextPageToken: current[itemId]?.nextPageToken,
        totalSize: current[itemId]?.totalSize,
        ...patch,
      },
    }));
  };

  const handleReferenceDocumentSearch = async (record: DatasetItem, value: string) => {
    const searchRequestId = (documentSearchRequestRef.current[record.id] || 0) + 1;
    documentSearchRequestRef.current[record.id] = searchRequestId;
    setDrafts((current) => {
      const currentDraft =
        current[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record);
      const referenceContext =
        referenceContextEditingValueRef.current[record.id] ??
        currentDraft.reference_context ??
        "";
      return {
        ...current,
        [record.id]: {
          ...currentDraft,
          reference_doc: value || "",
          reference_doc_ids: "",
          reference_chunk_ids: referenceContextChunkIDs(referenceContext).join(", "),
          reference_context: referenceContext,
        },
      };
    });
    setDirtyItemIds((current) =>
      current.includes(record.id) ? current : [...current, record.id],
    );
    delete documentSearchPaginationRequestRef.current[record.id];

    const match = value.match(/@([^@]*)$/);
    const rawKeyword = `${match?.[1] || ""}`;
    const keyword = rawKeyword.trim();
    const knowledgeBaseIds = (dataset?.knowledge_bases || [])
      .map((item) => `${item.id || ""}`.trim())
      .filter(Boolean);

    if (!match || knowledgeBaseIds.length === 0) {
      updateDocumentSearchState(record.id, {
        keyword,
        loading: false,
        options: [],
      });
      return;
    }

    updateDocumentSearchState(record.id, {
      keyword,
      loading: true,
      options: [],
    });

    try {
      const result = await searchKnowledgeBaseDocuments(knowledgeBaseIds, keyword);
      setDocumentSearchState((current) => {
        if (documentSearchRequestRef.current[record.id] !== searchRequestId) {
          return current;
        }
        const latestKeyword = current[record.id]?.keyword || "";
        if (latestKeyword !== keyword) {
          return current;
        }
        return {
          ...current,
          [record.id]: {
            loading: false,
            keyword,
            options: result.options,
            nextPageToken: result.nextPageToken,
            totalSize: result.totalSize,
          },
        };
      });
    } catch {
      if (documentSearchRequestRef.current[record.id] !== searchRequestId) {
        return;
      }
      updateDocumentSearchState(record.id, {
        keyword,
        loading: false,
        options: [],
      });
    }
  };

  const handleReferenceDocumentScroll = async (record: DatasetItem, event: React.UIEvent<HTMLDivElement>) => {
    const searchState = documentSearchState[record.id];
    const nextPageToken = `${searchState?.nextPageToken || ""}`.trim();
    const keyword = `${searchState?.keyword || ""}`.trim();
    const knowledgeBaseIds = (dataset?.knowledge_bases || [])
      .map((item) => `${item.id || ""}`.trim())
      .filter(Boolean);

    if (!nextPageToken || searchState?.loading || knowledgeBaseIds.length === 0) {
      return;
    }

    const target = event.currentTarget;
    const distanceToBottom = target.scrollHeight - target.scrollTop - target.clientHeight;
    if (distanceToBottom > 24) {
      return;
    }

    const requestKey = `${keyword}::${nextPageToken}`;
    if (documentSearchPaginationRequestRef.current[record.id] === requestKey) {
      return;
    }
    documentSearchPaginationRequestRef.current[record.id] = requestKey;

    updateDocumentSearchState(record.id, {
      loading: true,
    });

    try {
      const result = await searchKnowledgeBaseDocuments(
        knowledgeBaseIds,
        keyword,
        nextPageToken,
      );
      setDocumentSearchState((current) => {
        const latestState = current[record.id];
        if (
          `${latestState?.keyword || ""}`.trim() !== keyword ||
          `${latestState?.nextPageToken || ""}`.trim() !== nextPageToken
        ) {
          delete documentSearchPaginationRequestRef.current[record.id];
          return {
            ...current,
            [record.id]: {
              ...(latestState || {}),
              loading: false,
            },
          };
        }

        const currentOptions = latestState?.options || [];
        return {
          ...current,
          [record.id]: {
            ...(latestState || {}),
            loading: false,
            keyword,
            options: mergeKnowledgeDocumentOptions(currentOptions, result.options),
            nextPageToken: result.nextPageToken,
            totalSize: result.totalSize,
          },
        };
      });
      delete documentSearchPaginationRequestRef.current[record.id];
    } catch {
      delete documentSearchPaginationRequestRef.current[record.id];
      updateDocumentSearchState(record.id, {
        loading: false,
      });
    }
  };

  const handleReferenceDocumentSelect = (
    record: DatasetItem,
    option: KnowledgeDocumentOption,
  ) => {
    documentSearchRequestRef.current[record.id] =
      (documentSearchRequestRef.current[record.id] || 0) + 1;
    if (option.datasetId) {
      referenceDocumentCacheRef.current[option.documentId] = {
        datasetId: option.datasetId,
        documentId: option.documentId,
        name: option.name,
      };
      setReferenceDocumentCacheVersion((version) => version + 1);
    }
    const currentDraft =
      drafts[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record);
    const referenceContext =
      referenceContextEditingValueRef.current[record.id] ??
      currentDraft.reference_context ??
      "";
    const nextDraft: DatasetItemFormValues = {
      ...currentDraft,
      reference_doc: option.name,
      reference_doc_ids: option.documentId,
      reference_chunk_ids: referenceContextChunkIDs(referenceContext).join(", "),
      reference_context: referenceContext,
    };
    setDrafts((current) => ({
      ...current,
      [record.id]: nextDraft,
    }));
    setDirtyItemIds((current) =>
      current.includes(record.id) ? current : [...current, record.id],
    );
    updateDocumentSearchState(record.id, {
      keyword: "",
      loading: false,
      options: [],
    });
    delete documentSearchPaginationRequestRef.current[record.id];
    if (record.id !== NEW_ITEM_ID) {
      void handleSaveItem(record.id, nextDraft);
    }
  };

  const resolveReferenceDocument = useCallback(
    async (documentId: string, fallbackName: string) => {
      const normalizedDocumentId = `${documentId || ""}`.trim();
      if (!normalizedDocumentId) {
        throw new Error(t("datasetManagement.detail.reference.chooseDocumentFirst"));
      }

      const cachedDocument = referenceDocumentCacheRef.current[normalizedDocumentId];
      if (cachedDocument?.datasetId) {
        const response = await DocumentServiceApi().documentServiceGetDocument({
          dataset: cachedDocument.datasetId,
          document: normalizedDocumentId,
        });
        const detail = response.data;
        return {
          datasetId: cachedDocument.datasetId,
          documentId: normalizedDocumentId,
          name:
            `${detail.display_name || cachedDocument.name || fallbackName || normalizedDocumentId}`.trim(),
          fileUrl: normalizeProxyableUrl(
            detail.file_url ? `${window.location.origin}/api/core${detail.file_url}` : "",
          ),
        } satisfies ReferenceDocumentPreview;
      }

      const knowledgeBaseIds = (dataset?.knowledge_bases || [])
        .map((item) => `${item.id || ""}`.trim())
        .filter(Boolean);
      if (knowledgeBaseIds.length === 0) {
        throw new Error(t("datasetManagement.detail.reference.documentKbMissing"));
      }

      const matchedDocument = await findKnowledgeBaseDocumentById(
        knowledgeBaseIds,
        normalizedDocumentId,
      );
      if (!matchedDocument?.datasetId) {
        throw new Error(t("datasetManagement.detail.reference.documentKbMissing"));
      }
      const resolvedDocument = {
        datasetId: matchedDocument.datasetId,
        documentId: normalizedDocumentId,
        name: `${matchedDocument.name || fallbackName || normalizedDocumentId}`.trim(),
      } satisfies ReferenceDocumentPreview;
      referenceDocumentCacheRef.current[normalizedDocumentId] = resolvedDocument;
      setReferenceDocumentCacheVersion((version) => version + 1);
      const detailResponse = await DocumentServiceApi().documentServiceGetDocument({
        dataset: matchedDocument.datasetId,
        document: normalizedDocumentId,
      });
      const detail = detailResponse.data;
      return {
        ...resolvedDocument,
        name:
          `${detail.display_name || resolvedDocument.name || normalizedDocumentId}`.trim(),
        fileUrl: normalizeProxyableUrl(
          detail.file_url ? `${window.location.origin}/api/core${detail.file_url}` : "",
        ),
      } satisfies ReferenceDocumentPreview;
    },
    [dataset?.knowledge_bases, t],
  );

  const handleOpenReferenceChunkSelector = useCallback(
    async (record: DatasetItem) => {
      const draft = drafts[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record);
      const referenceContext =
        referenceContextEditingValueRef.current[record.id] ??
        draft.reference_context ??
        "";
      const documentId = `${draft.reference_doc_ids || ""}`
        .split(",")
        .map((item) => item.trim())
        .find(Boolean) || "";
      if (!documentId) {
        message.warning(t("datasetManagement.detail.reference.chooseKbDocumentFirst"));
        return;
      }

      const selectorRequestId = referenceChunkSelectorRequestRef.current + 1;
      referenceChunkSelectorRequestRef.current = selectorRequestId;
      setReferenceChunkSelector({
        open: true,
        loading: true,
        confirming: false,
        error: "",
        itemId: record.id,
        documentName: `${draft.reference_doc || record.reference_doc || ""}`.trim(),
        documentPreviewUrl: "",
        segmentGroup: "",
        chunks: [],
        selectedChunkIds: referenceContextChunkIDs(referenceContext).length > 0
          ? referenceContextChunkIDs(referenceContext)
          : `${draft.reference_chunk_ids || ""}`
            .split(",")
            .map((item) => item.trim())
            .filter(Boolean),
      });

      try {
        const resolvedDocument = await resolveReferenceDocument(
          documentId,
          `${draft.reference_doc || record.reference_doc || ""}`.trim(),
        );
        const datasetDetailResponse = await KnowledgeBaseServiceApi().datasetServiceGetDataset({
          dataset: resolvedDocument.datasetId,
        });
        const parsers = datasetDetailResponse.data.parsers || [];
        const splitParsers = parsers.filter(
          (parser) => parser.type === ParserConfigTypeEnum.ParseTypeSplit,
        );
        const segmentGroup = `${splitParsers[0]?.name || "block"}`.trim() || "block";

        const segmentResponse = await SegmentServiceApi().segmentServiceSearchSegments({
          dataset: resolvedDocument.datasetId,
          document: resolvedDocument.documentId,
          searchSegmentsRequest: {
            parent: "",
            group: segmentGroup,
            page_size: 100,
            page_token: "",
          },
        });
        const chunks = segmentResponse.data.segments || [];
        if (referenceChunkSelectorRequestRef.current !== selectorRequestId) {
          return;
        }
        const nextSelectedChunkIds =
          `${draft.reference_chunk_ids || ""}`
            .split(",")
            .map((item) => item.trim())
            .filter((item) => chunks.some((chunk) => chunk.segment_id === item)) || [];
        const previewSegment =
          chunks.find((chunk) => nextSelectedChunkIds.includes(`${chunk.segment_id || ""}`)) ||
          chunks[0];

        setReferenceChunkSelector((current) => ({
          ...current,
          loading: false,
          documentName: resolvedDocument.name,
          documentPreviewUrl: resolvedDocument.fileUrl || "",
          segmentGroup,
          chunks,
          selectedChunkIds: nextSelectedChunkIds,
          previewSegment,
          error: chunks.length === 0 ? t("datasetManagement.detail.reference.noChunks") : "",
        }));
      } catch (error: any) {
        if (referenceChunkSelectorRequestRef.current !== selectorRequestId) {
          return;
        }
        setReferenceChunkSelector((current) => ({
          ...current,
          loading: false,
          error: error?.message || t("datasetManagement.detail.reference.docLoadFailed"),
        }));
      }
    },
    [drafts, resolveReferenceDocument, t],
  );

  const canSelectReferenceChunks = useCallback(
    (record: DatasetItem) => {
      const draft = drafts[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record);
      const documentId = `${draft.reference_doc_ids || ""}`
        .split(",")
        .map((item) => item.trim())
        .find(Boolean) || "";
      if (!documentId) {
        return false;
      }

      if (referenceDocumentCacheRef.current[documentId]?.datasetId) {
        return true;
      }

      return Boolean(record.reference_doc_from_knowledge_base);
    },
    [drafts, referenceDocumentCacheVersion],
  );

  const hasSelectedReferenceChunks = useCallback(
    (record: DatasetItem) => {
      const draft = drafts[record.id];
      if (!draft) {
        return Boolean(record.reference_chunk_selected);
      }
      const draftChunkIds = `${draft.reference_chunk_ids || ""}`
        .split(",")
        .map((item) => item.trim())
        .filter(Boolean);
      return draftChunkIds.length > 0;
    },
    [drafts],
  );

  const handleToggleReferenceChunk = useCallback((segment: Segment, checked: boolean) => {
    const segmentId = `${segment.segment_id || ""}`.trim();
    if (!segmentId) {
      return;
    }
    setReferenceChunkSelector((current) => {
      const selectedChunkIds = checked
        ? Array.from(new Set([...current.selectedChunkIds, segmentId]))
        : current.selectedChunkIds.filter((item) => item !== segmentId);
      return {
        ...current,
        selectedChunkIds,
        previewSegment: segment,
      };
    });
  }, []);

  const handleConfirmReferenceChunks = useCallback(async () => {
    const itemId = referenceChunkSelector.itemId;
    if (!itemId) {
      return;
    }

    const selectedChunks = referenceChunkSelector.chunks.filter((chunk) =>
      referenceChunkSelector.selectedChunkIds.includes(`${chunk.segment_id || ""}`.trim()),
    );
    if (selectedChunks.length === 0) {
      message.warning(t("datasetManagement.detail.reference.selectAtLeastOneChunk"));
      return;
    }

    const selectedChunkParts = selectedChunks
      .map((chunk): ReferenceContextChunkPart | null => {
        const content = normalizeReferenceContextText(`${chunk.display_content || chunk.content || ""}`);
        if (!content) {
          return null;
        }
        return {
          type: "chunk",
          id: `${chunk.segment_id || ""}`.trim(),
          content,
        };
      })
      .filter((chunk): chunk is ReferenceContextChunkPart => Boolean(chunk));
    setReferenceChunkSelector((current) => ({
      ...current,
      confirming: true,
    }));

    const currentItem = items.find((item) => item.id === itemId);
    const currentDraft =
      drafts[itemId] ||
      createItemDraft(itemId === NEW_ITEM_ID ? undefined : currentItem);
    const insertIndex = referenceContextInsertIndexRef.current[itemId];
    const nextReferenceContext = buildReferenceContextWithChunksAtTextPart(
      referenceContextEditingValueRef.current[itemId] ?? currentDraft.reference_context,
      selectedChunkParts,
      insertIndex,
    );
    referenceContextEditingValueRef.current[itemId] = nextReferenceContext;
    referenceContextEditingDirtyRef.current[itemId] = true;
    const nextDraft: DatasetItemFormValues = {
      ...currentDraft,
      reference_context: nextReferenceContext,
      reference_chunk_ids: referenceContextChunkIDs(nextReferenceContext).join(", "),
    };
    setDrafts((current) => ({
      ...current,
      [itemId]: nextDraft,
    }));
    setDirtyItemIds((current) => (current.includes(itemId) ? current : [...current, itemId]));

    resetReferenceChunkSelector();
    if (itemId !== NEW_ITEM_ID) {
      await handleSaveItem(itemId, nextDraft);
    }
  }, [drafts, items, referenceChunkSelector, resetReferenceChunkSelector, t]);

  const handleCancelItem = (item: DatasetItem) => {
    clearItemRuntimeState(item.id);
    if (item.id === NEW_ITEM_ID) {
      setNewItemVisible(false);
      setActiveCell(null);
      setDirtyItemIds((current) => current.filter((id) => id !== NEW_ITEM_ID));
      setDrafts((current) => {
        const { [NEW_ITEM_ID]: _newItemDraft, ...rest } = current;
        return rest;
      });
      return;
    }
    if (activeCell?.itemId === item.id) {
      setActiveCell(null);
    }
    setDrafts((current) => ({
      ...current,
      [item.id]: createItemDraft(item),
    }));
    setDirtyItemIds((current) => current.filter((id) => id !== item.id));
  };

  const handleClearInvalidReferenceDoc = async (item: DatasetItem) => {
    const currentDraft = drafts[item.id] || createItemDraft(item);
    const referenceContext =
      referenceContextEditingValueRef.current[item.id] ??
      currentDraft.reference_context ??
      "";
    const nextValues: DatasetItemFormValues = {
      ...currentDraft,
      reference_doc: "",
      reference_doc_ids: "",
      reference_context: referenceContext,
      reference_chunk_ids: referenceContextChunkIDs(referenceContext).join(", "),
    };

    clearReferenceDocumentRuntimeState(item.id);
    await handleSaveItem(
      item.id,
      nextValues,
      t("datasetManagement.detail.reference.invalidDocCleared"),
    );
  };

  const handleClearInvalidReferenceContext = async (item: DatasetItem) => {
    const currentDraft = drafts[item.id] || createItemDraft(item);
    const referenceContext =
      referenceContextEditingValueRef.current[item.id] ??
      currentDraft.reference_context ??
      "";
    const nextReferenceContext = removeReferenceContextChunks(referenceContext);
    const nextValues: DatasetItemFormValues = {
      ...currentDraft,
      reference_context: nextReferenceContext,
      reference_chunk_ids: "",
    };

    referenceContextEditingValueRef.current[item.id] = nextReferenceContext;
    referenceContextEditingDirtyRef.current[item.id] = true;
    setDrafts((current) => ({
      ...current,
      [item.id]: nextValues,
    }));
    await handleSaveItem(
      item.id,
      nextValues,
      t("datasetManagement.detail.reference.invalidContextCleared"),
    );
  };

  const handleDeleteItem = (item: DatasetItem) => {
    if (item.id === NEW_ITEM_ID) {
      handleCancelItem(item);
      return;
    }
    Modal.confirm({
      title: t("datasetManagement.detail.deleteSampleTitle"),
      content: item.question,
      okText: t("common.delete"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        await deleteDatasetItem(datasetId, item.id);
        clearItemRuntimeState(item.id);
        message.success(t("datasetManagement.detail.sampleDeleted"));
        await loadDetail();
      },
    });
  };

  const handleBatchDelete = () => {
    if (selectedRowKeys.length === 0) {
      message.warning(t("datasetManagement.detail.selectSampleFirst"));
      return;
    }
    Modal.confirm({
      title: t("datasetManagement.detail.batchDeleteTitle", { count: selectedRowKeys.length }),
      content: t("datasetManagement.detail.batchDeleteContent"),
      okText: t("common.delete"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        await batchDeleteDatasetItems(datasetId, selectedRowKeys.map(String));
        selectedRowKeys.map(String).forEach(clearItemRuntimeState);
        setSelectedRowKeys([]);
        message.success(t("datasetManagement.detail.batchDeleted"));
        await loadDetail();
      },
    });
  };

  const handleImported = async (
    importedItems: Array<Partial<DatasetItem>>,
    result: DatasetImportResultState,
    file: File | null,
  ) => {
    await importDatasetItems(datasetId, file, importedItems, result.failedCount);
    clearAllItemRuntimeState();
    setVisibleColumnKeys(DEFAULT_VISIBLE_COLUMN_KEYS);
    message.success(t("datasetManagement.detail.importCompleted"));
    await loadDetail();
  };

  const dataSource = useMemo(() => {
    if (!newItemVisible) {
      return items;
    }
    const newItem: DatasetItem = {
      id: NEW_ITEM_ID,
      dataset_id: datasetId,
      case_id: "",
      question: t("datasetManagement.detail.newSample"),
      question_type: "",
      ground_truth: "",
      source: "manual",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      created_by: t("datasetManagement.detail.currentUser"),
    };
    return [newItem, ...items];
  }, [datasetId, items, newItemVisible, t]);

  const renderCellDisplay = (
    record: DatasetItem,
    field: EditableDatasetItemField,
    placeholder: string,
  ) => {
    const draft = drafts[record.id];
    const value =
      field === "reference_context"
        ? referenceContextEditingValueRef.current[record.id] ??
          draft?.reference_context ??
          ""
        : draft?.[field] || "";
    const shouldShowReferenceChunkSelector =
      field === "reference_context" && canSelectReferenceChunks(record);
    const shouldShowInvalidReferenceDocClear =
      field === "reference_doc" &&
      record.id !== NEW_ITEM_ID &&
      Boolean(record.reference_doc_invalid);
    const shouldShowInvalidReferenceContextClear =
      field === "reference_context" &&
      record.id !== NEW_ITEM_ID &&
      Boolean(record.reference_chunk_invalid);
    return (
      <div
        className={`dataset-inline-display-wrapper${
          shouldShowInvalidReferenceDocClear || shouldShowInvalidReferenceContextClear
            ? " is-reference-invalid"
            : ""
        }`}
      >
        <button
          type="button"
          className={`dataset-inline-display${field === "reference_context" ? " dataset-reference-context-display" : ""}`}
          onMouseDown={() => {
            if (record.id === NEW_ITEM_ID) {
              pendingNewItemCellActivationRef.current = {
                itemId: record.id,
                field,
              };
            }
          }}
          onClick={() => {
            void activateEditableCell(record, field);
          }}
        >
          {field === "reference_context"
            ? renderReferenceContextParts(
              value,
              placeholder,
              (index) => t("datasetManagement.detail.reference.chunkLabel", { index }),
            )
            : field === "reference_doc"
              ? renderReferenceDocumentTag(value, placeholder)
            : value || <span className="dataset-inline-placeholder">{placeholder}</span>}
        </button>
        {shouldShowReferenceChunkSelector ? (
          <Button
            size="small"
            type="link"
            className="dataset-reference-chunk-trigger"
            onClick={() => void handleOpenReferenceChunkSelector(record)}
          >
            {hasSelectedReferenceChunks(record)
              ? t("datasetManagement.detail.reference.selectedChunks")
              : t("datasetManagement.detail.reference.selectChunks")}
          </Button>
        ) : null}
        {shouldShowInvalidReferenceDocClear ? (
          <Button
            size="small"
            type="link"
            danger
            disabled={saving}
            className="dataset-reference-invalid-clear"
            title={t("datasetManagement.detail.reference.invalidDocHint")}
            onMouseDown={(event) => {
              event.preventDefault();
              event.stopPropagation();
            }}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              void handleClearInvalidReferenceDoc(record);
            }}
          >
            {t("datasetManagement.detail.reference.clearInvalidDoc")}
          </Button>
        ) : null}
        {shouldShowInvalidReferenceContextClear ? (
          <Button
            size="small"
            type="link"
            danger
            disabled={saving}
            className="dataset-reference-invalid-clear"
            title={t("datasetManagement.detail.reference.invalidContextHint")}
            onMouseDown={(event) => {
              event.preventDefault();
              event.stopPropagation();
            }}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              void handleClearInvalidReferenceContext(record);
            }}
          >
            {t("datasetManagement.detail.reference.clearInvalidContext")}
          </Button>
        ) : null}
      </div>
    );
  };

  const renderInlineInput = (
    record: DatasetItem,
    field: EditableDatasetItemField,
    placeholder: string,
  ) => {
    if (activeCell?.itemId !== record.id || activeCell.field !== field) {
      return renderCellDisplay(record, field, placeholder);
    }
    return (
      <Input
        autoFocus
        className="dataset-inline-input"
        value={drafts[record.id]?.[field] || ""}
        placeholder={placeholder}
        onChange={(event) => handleDraftChange(record, field, event.target.value)}
        onBlur={() => void handleAutoSaveItem(record)}
      />
    );
  };

  const renderReferenceDocumentInput = (record: DatasetItem) => {
    if (activeCell?.itemId !== record.id || activeCell.field !== "reference_doc") {
      return renderCellDisplay(
        record,
        "reference_doc",
        t("datasetManagement.detail.placeholders.referenceDoc"),
      );
    }

    const currentDraft = drafts[record.id];
    const currentReferenceDoc = `${currentDraft?.reference_doc || ""}`.trim();
    const selectedReferenceDocIDs = `${currentDraft?.reference_doc_ids || ""}`
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean);
    const shouldRenderReferenceDocTag =
      Boolean(currentReferenceDoc) && selectedReferenceDocIDs.length > 0;

    if (shouldRenderReferenceDocTag) {
      return (
        <div className="dataset-inline-textarea-wrapper">
          <span className="dataset-reference-doc-tag dataset-reference-doc-tag-editing">
            <span className="dataset-reference-doc-tag-text">{currentReferenceDoc}</span>
            <button
              type="button"
              className="dataset-reference-doc-tag-remove"
              aria-label={t("datasetManagement.detail.reference.deleteReferenceDocAria")}
              onClick={() => {
                const currentDraft = drafts[record.id] || createItemDraft(record);
                const referenceContext =
                  referenceContextEditingValueRef.current[record.id] ??
                  currentDraft.reference_context ??
                  "";
                const nextDraft: DatasetItemFormValues = {
                  ...currentDraft,
                  reference_doc: "",
                  reference_doc_ids: "",
                  reference_chunk_ids: referenceContextChunkIDs(referenceContext).join(", "),
                  reference_context: referenceContext,
                };
                setDrafts((current) => ({
                  ...current,
                  [record.id]: nextDraft,
                }));
                clearReferenceDocumentRuntimeState(record.id);
                setDirtyItemIds((current) =>
                  current.includes(record.id) ? current : [...current, record.id],
                );
                if (record.id !== NEW_ITEM_ID) {
                  void handleSaveItem(record.id, nextDraft);
                }
              }}
            >
              <CloseOutlined />
            </button>
          </span>
        </div>
      );
    }

    const searchState = documentSearchState[record.id];
    const displayDocumentOptions = mergeKnowledgeDocumentOptions(
      [],
      searchState?.options || [],
    );
    const autoCompleteOptions = displayDocumentOptions.map((option) => ({
      key: option.documentId,
      label: option.name,
      value: option.documentId,
      option,
    }));
    const shouldOpenDocumentOptions =
      activeCell?.itemId === record.id &&
      activeCell.field === "reference_doc" &&
      (drafts[record.id]?.reference_doc || "").includes("@") &&
      (Boolean(searchState?.loading) || autoCompleteOptions.length > 0);

    return (
      <AutoComplete
        autoFocus
        open={shouldOpenDocumentOptions}
        className="dataset-inline-input"
        filterOption={false}
        listHeight={280}
        value={drafts[record.id]?.reference_doc || ""}
        placeholder={t("datasetManagement.detail.placeholders.referenceDocSearch")}
        notFoundContent={
          searchState?.loading
            ? t("datasetManagement.detail.reference.searching")
            : t("datasetManagement.detail.reference.noMatchedDocs")
        }
        options={autoCompleteOptions}
        onPopupScroll={(event) => {
          void handleReferenceDocumentScroll(record, event);
        }}
        onChange={(value) => {
          void handleReferenceDocumentSearch(record, value);
        }}
        onSelect={(_, selectedOption) => {
          if ((selectedOption as { option?: KnowledgeDocumentOption })?.option) {
            handleReferenceDocumentSelect(
              record,
              (selectedOption as { option: KnowledgeDocumentOption }).option,
            );
          }
        }}
        onBlur={() => void handleAutoSaveItem(record)}
      >
        <Input />
      </AutoComplete>
    );
  };

  const renderInlineTextArea = (
    record: DatasetItem,
    field: EditableDatasetItemField,
    placeholder: string,
  ) => {
    if (activeCell?.itemId !== record.id || activeCell.field !== field) {
      return renderCellDisplay(record, field, placeholder);
    }
    if (field === "reference_context") {
      const currentValue = drafts[record.id]?.reference_context || "";
      return (
        <div className="dataset-inline-textarea-wrapper">
          <ReferenceContextInlineEditor
            autoFocus
            value={referenceContextEditingValueRef.current[record.id] ?? currentValue}
            placeholder={placeholder}
            onChange={(nextValue) => {
              referenceContextEditingValueRef.current[record.id] = nextValue;
              referenceContextEditingDirtyRef.current[record.id] = true;
              setDrafts((current) => {
                const currentDraft = current[record.id] || createItemDraft(record);
                return {
                  ...current,
                  [record.id]: {
                    ...currentDraft,
                    reference_context: nextValue,
                    reference_chunk_ids: referenceContextChunkIDs(nextValue).join(", "),
                  },
                };
              });
              setDirtyItemIds((current) =>
                current.includes(record.id) ? current : [...current, record.id],
              );
            }}
            onBlur={() => void handleAutoSaveItem(record)}
            onInsertIndexChange={(index) => {
              referenceContextInsertIndexRef.current[record.id] = index;
            }}
            formatChunkLabel={(index) =>
              t("datasetManagement.detail.reference.chunkLabel", { index })
            }
            formatDeleteChunkAria={(index) =>
              t("datasetManagement.detail.reference.deleteChunkAria", { index })
            }
            onRemovePart={(index) => {
              const baseReferenceContext =
                referenceContextEditingValueRef.current[record.id] ??
                drafts[record.id]?.reference_context ??
                "";
              const nextReferenceContext = removeReferenceContextPart(
                baseReferenceContext,
                index,
              );
              referenceContextEditingValueRef.current[record.id] = nextReferenceContext;
              referenceContextEditingDirtyRef.current[record.id] = true;
              setDrafts((current) => {
                const currentDraft = current[record.id] || createItemDraft(record);
                return {
                  ...current,
                  [record.id]: {
                    ...currentDraft,
                    reference_context: nextReferenceContext,
                    reference_chunk_ids: referenceContextChunkIDs(nextReferenceContext).join(", "),
                  },
                };
              });
              setDirtyItemIds((current) =>
                current.includes(record.id) ? current : [...current, record.id],
              );
            }}
          />
          {canSelectReferenceChunks(record) ? (
            <Button
              size="small"
              type="link"
              className="dataset-reference-chunk-trigger"
              onMouseDown={(event) => {
                event.preventDefault();
                event.stopPropagation();
              }}
              onClick={() => void handleOpenReferenceChunkSelector(record)}
            >
              {hasSelectedReferenceChunks(record)
                ? t("datasetManagement.detail.reference.selectedChunks")
                : t("datasetManagement.detail.reference.selectChunks")}
            </Button>
          ) : null}
        </div>
      );
    }
    return (
      <div className="dataset-inline-textarea-wrapper">
        <TextArea
          autoFocus
          className="dataset-inline-textarea"
          value={drafts[record.id]?.[field] || ""}
          placeholder={placeholder}
          autoSize={{ minRows: 1, maxRows: 4 }}
          onChange={(event) => handleDraftChange(record, field, event.target.value)}
          onBlur={() => void handleAutoSaveItem(record)}
        />
      </div>
    );
  };

  const renderQuestionTypeCell = (record: DatasetItem) => {
    if (activeCell?.itemId !== record.id || activeCell.field !== "question_type") {
      return renderCellDisplay(
        record,
        "question_type",
        t("datasetManagement.detail.placeholders.questionType"),
      );
    }
    return (
      <QuestionTypeSelect
        value={drafts[record.id]?.question_type || undefined}
        placeholder={t("datasetManagement.detail.questionTypePlaceholder")}
        onChange={(value) => handleDraftChange(record, "question_type", value)}
        onBlur={() => void handleAutoSaveItem(record)}
        options={questionTypeOptions}
      />
    );
  };

  const columns = useMemo<ColumnsType<DatasetItem>>(() => {
    const allColumns: ColumnsType<DatasetItem> = [
      {
        title: renderRequiredColumnTitle(t(datasetItemFieldI18nKeys.question)),
        dataIndex: "question",
        key: "question",
        width: columnWidths.question,
        onHeaderCell: () => getHeaderCellProps("question"),
        render: (_, record) =>
          renderInlineInput(record, "question", t("datasetManagement.detail.placeholders.question")),
      },
      {
        title: renderRequiredColumnTitle(t(datasetItemFieldI18nKeys.question_type)),
        dataIndex: "question_type",
        key: "question_type",
        width: columnWidths.question_type,
        onHeaderCell: () => getHeaderCellProps("question_type"),
        render: (_, record) => renderQuestionTypeCell(record),
      },
      {
        title: renderRequiredColumnTitle(t(datasetItemFieldI18nKeys.ground_truth)),
        dataIndex: "ground_truth",
        key: "ground_truth",
        width: columnWidths.ground_truth,
        onHeaderCell: () => getHeaderCellProps("ground_truth"),
        render: (_, record) =>
          renderInlineTextArea(
            record,
            "ground_truth",
            t("datasetManagement.detail.placeholders.groundTruth"),
          ),
      },
      {
        title: t(datasetItemFieldI18nKeys.key_points),
        dataIndex: "key_points",
        key: "key_points",
        width: columnWidths.key_points,
        onHeaderCell: () => getHeaderCellProps("key_points"),
        render: (_, record) =>
          renderInlineTextArea(
            record,
            "key_points",
            t("datasetManagement.detail.placeholders.keyPoints"),
          ),
      },
      {
        title: t(datasetItemFieldI18nKeys.reference_doc),
        dataIndex: "reference_doc",
        key: "reference_doc",
        width: columnWidths.reference_doc,
        onHeaderCell: () => getHeaderCellProps("reference_doc"),
        render: (_, record) => renderReferenceDocumentInput(record),
      },
      {
        title: t(datasetItemFieldI18nKeys.reference_context),
        dataIndex: "reference_context",
        key: "reference_context",
        width: columnWidths.reference_context,
        onHeaderCell: () => getHeaderCellProps("reference_context"),
        render: (_, record) =>
          renderInlineTextArea(
            record,
            "reference_context",
            t("datasetManagement.detail.placeholders.referenceContext"),
          ),
      },
      {
        title: t("datasetManagement.fields.source"),
        dataIndex: "source",
        key: "source",
        width: columnWidths.source,
        onHeaderCell: () => getHeaderCellProps("source"),
        render: (value: DatasetItemSource) => <SourceTypeTag source={value} />,
      },
      {
        title: t("datasetManagement.fields.updatedAt"),
        dataIndex: "updated_at",
        key: "updated_at",
        width: columnWidths.updated_at,
        onHeaderCell: () => getHeaderCellProps("updated_at"),
        render: (value) => formatDateTime(value),
      },
      {
        title: t("common.actions"),
        key: "actions",
        width: columnWidths.actions,
        fixed: "right",
        onHeaderCell: () => getHeaderCellProps("actions"),
        render: (_, record) => (
          <Button
            danger
            size="small"
            icon={<DeleteOutlined />}
            onMouseDown={() => {
              if (record.id === NEW_ITEM_ID) {
                pendingNewItemCellActivationRef.current = {
                  itemId: NEW_ITEM_ID,
                  field: activeCell?.field || "question",
                };
              }
            }}
            onClick={() => handleDeleteItem(record)}
          >
            {t("common.delete")}
          </Button>
        ),
      },
    ];

    return allColumns.filter((column) => {
      if (column.key === "actions") {
        return true;
      }
      return effectiveVisibleColumnKeys.includes(column.key as ConfigurableColumnKey);
    });
  }, [
    columnWidths,
    dirtyItemIds,
    drafts,
    activeCell,
    documentSearchState,
    getHeaderCellProps,
    saving,
    effectiveVisibleColumnKeys,
    t,
  ]);

  const tableScrollX = useMemo(
    () =>
      effectiveVisibleColumnKeys.reduce(
        (total, columnKey) => total + columnWidths[columnKey],
        columnWidths.actions + 96,
      ),
    [columnWidths, effectiveVisibleColumnKeys],
  );
  const tableStyle = {
    "--dataset-table-header-height": `${headerHeight}px`,
    "--dataset-table-row-height": `${rowHeight}px`,
  } as CSSProperties;
  const columnSettingsContent = (
    <div className="dataset-column-settings">
      <div className="dataset-column-settings-header">
        <span>{t("datasetManagement.columnSettings.selectColumns")}</span>
        <Button
          type="link"
          size="small"
          onClick={() => setVisibleColumnKeys(DEFAULT_VISIBLE_COLUMN_KEYS)}
        >
          {t("datasetManagement.columnSettings.restoreDefault")}
        </Button>
      </div>
      <Checkbox.Group
        className="dataset-column-settings-options"
        value={effectiveVisibleColumnKeys}
        options={columnSettingOptions}
        onChange={(values) =>
          setVisibleColumnKeys(normalizeVisibleColumnKeys(values as ConfigurableColumnKey[]))
        }
      />
    </div>
  );

  return (
    <div className="dataset-page dataset-detail-page">
      <div className="dataset-detail-breadcrumb">
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          onClick={async () => {
            const canContinue = await confirmDiscardDirty();
            if (canContinue) {
              navigate("/dataset-management");
            }
          }}
        >
          {t("datasetManagement.detail.breadcrumb", {
            name: dataset?.name || t("datasetManagement.detail.titleFallback"),
          })}
        </Button>
      </div>

      <Card className="dataset-detail-card">
        <div className="dataset-detail-actions">
          <Space wrap>
            <Button type="primary" icon={<PlusOutlined />} onClick={handleAddItem}>
              {t("datasetManagement.detail.addSample")}
            </Button>
            <Button icon={<ImportOutlined />} onClick={() => setImportModalOpen(true)}>
              {t("datasetManagement.detail.importData")}
            </Button>
            <Button danger icon={<DeleteOutlined />} onClick={handleBatchDelete}>
              {t("datasetManagement.detail.batchDelete")}
            </Button>
            <Popover
              trigger="click"
              placement="bottomRight"
              content={columnSettingsContent}
            >
              <Button icon={<SettingOutlined />}>{t("datasetManagement.detail.columnSettings")}</Button>
            </Popover>
          </Space>
        </div>

        <div className="dataset-detail-filters">
          <Input
            allowClear
            className="dataset-detail-search"
            prefix={<SearchOutlined />}
            placeholder={t("datasetManagement.detail.searchPlaceholder")}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={handleFilterSearch}
          />
          <div className="dataset-filter-controls">
            <QuestionTypeSelect
              allowClear
              value={questionType}
              onChange={setQuestionType}
              placeholder={t("datasetManagement.detail.questionTypePlaceholder")}
              options={questionTypeOptions}
            />
            <Select
              allowClear
              className="dataset-source-filter"
              value={source}
              placeholder={t("datasetManagement.detail.sourcePlaceholder")}
              onChange={setSource}
              options={(["upload", "manual", "flowback"] as const).map((value) => ({
                label: t(sourceLabelI18nKeys[value]),
                value,
              }))}
            />
            <Button type="primary" onClick={handleFilterSearch}>
              {t("datasetManagement.detail.search")}
            </Button>
          </div>
        </div>

        <Table
          rowKey="id"
          className="dataset-item-table"
          style={tableStyle}
          loading={loading}
          components={tableComponents}
          columns={columns}
          dataSource={dataSource}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t("datasetManagement.detail.emptySamples")}
              />
            ),
          }}
          rowSelection={{
            selectedRowKeys,
            onChange: setSelectedRowKeys,
            getCheckboxProps: (record) => ({
              disabled: record.id === NEW_ITEM_ID,
            }),
          }}
          onRow={(record) => ({
            className: dirtyItemIds.includes(record.id) ? "is-editing-row" : "",
            rowHeight,
            onResizeRow: handleRowResize,
          })}
          scroll={{ x: tableScrollX }}
          pagination={{
            current: pagination.current,
            pageSize: pagination.pageSize,
            total,
            showTotal: (currentTotal) =>
              t("datasetManagement.detail.paginationTotal", { total: currentTotal }),
            onChange: async (current, pageSize) => {
              const canContinue = await confirmDiscardDirty();
              if (!canContinue) {
                return;
              }
              clearAllItemRuntimeState();
              setDirtyItemIds([]);
              setNewItemVisible(false);
              setActiveCell(null);
              setPagination({ current, pageSize });
            },
          }}
        />
      </Card>

      <DatasetImportModal
        open={importModalOpen}
        onCancel={() => setImportModalOpen(false)}
        onImported={handleImported}
      />

      <Modal
        open={referenceChunkSelector.open}
        title={
          referenceChunkSelector.documentName
            ? t("datasetManagement.detail.reference.selectorTitleWithName", {
              name: referenceChunkSelector.documentName,
            })
            : t("datasetManagement.detail.reference.selectorTitle")
        }
        width={1200}
        okText={t("common.confirm")}
        cancelText={t("common.cancel")}
        confirmLoading={referenceChunkSelector.confirming}
        onOk={() => void handleConfirmReferenceChunks()}
        onCancel={resetReferenceChunkSelector}
        destroyOnClose
      >
        <div className="dataset-reference-modal">
          <div className="dataset-reference-modal-panel">
            <div className="dataset-reference-modal-panel-header">
              {t("datasetManagement.detail.reference.originalDocument")}
            </div>
            <div className="dataset-reference-modal-panel-body dataset-reference-modal-preview">
              {referenceChunkSelector.loading ? (
                <div className="dataset-reference-modal-empty">
                  {t("datasetManagement.detail.reference.loading")}
                </div>
              ) : referenceChunkSelector.documentPreviewUrl ? (
                <FileViewer
                  file={referenceChunkSelector.documentPreviewUrl}
                  fileName={referenceChunkSelector.documentName}
                  segment={referenceChunkSelector.previewSegment}
                />
              ) : (
                <div className="dataset-reference-modal-empty">
                  {t("datasetManagement.detail.reference.previewUnsupported")}
                </div>
              )}
            </div>
          </div>
          <div className="dataset-reference-modal-panel">
            <div className="dataset-reference-modal-panel-header">
              {t("datasetManagement.detail.reference.parsedChunksTitle", {
                count: referenceChunkSelector.selectedChunkIds.length,
              })}
            </div>
            <div className="dataset-reference-modal-panel-body">
              {referenceChunkSelector.error ? (
                <div className="dataset-reference-modal-empty">{referenceChunkSelector.error}</div>
              ) : referenceChunkSelector.loading ? (
                <div className="dataset-reference-modal-empty">
                  {t("datasetManagement.detail.reference.loading")}
                </div>
              ) : referenceChunkSelector.chunks.length === 0 ? (
                <div className="dataset-reference-modal-empty">
                  {t("datasetManagement.detail.reference.noChunks")}
                </div>
              ) : (
                <div className="dataset-reference-chunk-list">
                  {referenceChunkSelector.chunks.map((chunk, index) => {
                    const segmentId = `${chunk.segment_id || ""}`.trim();
                    const content = `${chunk.display_content || chunk.content || ""}`.trim();
                    const checked = referenceChunkSelector.selectedChunkIds.includes(segmentId);
                    return (
                      <label
                        key={segmentId || `${index}`}
                        className={`dataset-reference-chunk-item${checked ? " is-selected" : ""}`}
                        onMouseEnter={() =>
                          setReferenceChunkSelector((current) => ({
                            ...current,
                            previewSegment: chunk,
                          }))
                        }
                      >
                        <Checkbox
                          checked={checked}
                          onChange={(event) =>
                            handleToggleReferenceChunk(chunk, event.target.checked)
                          }
                        />
                        <div className="dataset-reference-chunk-content">
                          <div className="dataset-reference-chunk-title">
                            {t("datasetManagement.detail.reference.chunkTitle", {
                              index: index + 1,
                            })}
                            {segmentId ? ` · ${segmentId}` : ""}
                          </div>
                          <div className="dataset-reference-chunk-text">
                            {content || t("datasetManagement.detail.reference.emptyChunkText")}
                          </div>
                        </div>
                      </label>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        </div>
      </Modal>
    </div>
  );
}
