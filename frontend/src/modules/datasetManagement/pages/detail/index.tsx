import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  CSSProperties,
  Key,
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
  DeleteOutlined,
  ImportOutlined,
  PlusOutlined,
  SearchOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { useNavigate, useParams } from "react-router-dom";
import {
  batchDeleteDatasetItems,
  createDatasetItem,
  deleteDatasetItem,
  findKnowledgeBaseDocumentById,
  getDataset,
  importDatasetItems,
  listDatasetItems,
  type KnowledgeDocumentOption,
  listQuestionTypes,
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
import { formatDateTime, sourceLabelMap } from "../../shared";
import {
  joinListField,
  validateRequiredDatasetItem,
} from "../../utils/datasetValidation";
import "../../index.scss";

const { TextArea } = Input;
const NEW_ITEM_ID = "__new_dataset_item__";
const MIN_COLUMN_WIDTH = 88;
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
  generate_reason: 220,
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
  | "reference_doc"
  | "generate_reason";
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
type ConfigurableColumnKey = Exclude<ResizableColumnKey, "actions">;
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

const CONFIGURABLE_COLUMN_OPTIONS: Array<{
  label: string;
  value: ConfigurableColumnKey;
}> = [
  { label: "问题", value: "question" },
  { label: "问题类型", value: "question_type" },
  { label: "标准答案", value: "ground_truth" },
  { label: "答案要点", value: "key_points" },
  { label: "参考文档", value: "reference_doc" },
  { label: "参考上下文", value: "reference_context" },
  { label: "生成依据", value: "generate_reason" },
  { label: "来源", value: "source" },
  { label: "更新时间", value: "updated_at" },
];

const DEFAULT_VISIBLE_COLUMN_KEYS = CONFIGURABLE_COLUMN_OPTIONS.map(
  (option) => option.value,
);

const editableFieldColumnMap: Record<EditableDatasetItemField, ConfigurableColumnKey> = {
  question: "question",
  question_type: "question_type",
  ground_truth: "ground_truth",
  key_points: "key_points",
  reference_context: "reference_context",
  reference_doc: "reference_doc",
  generate_reason: "generate_reason",
};

type ResizableHeaderCellProps = ThHTMLAttributes<HTMLTableCellElement> & {
  columnKey?: ResizableColumnKey;
  columnWidth?: number;
  onResizeColumn?: (
    columnKey: ResizableColumnKey,
    startX: number,
    startWidth: number,
  ) => void;
};

function ResizableHeaderCell({
  columnKey,
  columnWidth,
  onResizeColumn,
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

  return (
    <th {...rest} style={style}>
      <div className="dataset-resizable-header-content">{children}</div>
      {columnKey ? (
        <span
          aria-hidden="true"
          className="dataset-column-resize-handle"
          onMouseDown={handleColumnResizeStart}
        />
      ) : null}
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
  return {
    ...values,
    case_id: item.case_id || values.case_id,
    reference_doc_ids: values.reference_doc_ids || joinListField(item.reference_doc_ids),
    reference_chunk_ids:
      values.reference_chunk_ids || joinListField(item.reference_chunk_ids),
    is_deleted: Boolean(item.is_deleted),
  };
}

export default function DatasetDetailPage() {
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
  const [rowHeight, setRowHeight] = useState(DEFAULT_ROW_HEIGHT);
  const [documentSearchState, setDocumentSearchState] = useState<
    Record<string, DocumentSearchState>
  >({});
  const [referenceChunkSelector, setReferenceChunkSelector] =
    useState<ReferenceChunkSelectorState>({
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
    });
  const documentSearchPaginationRequestRef = useRef<Record<string, string>>({});
  const referenceDocumentCacheRef = useRef<Record<string, ReferenceDocumentPreview>>({});
  const [referenceDocumentCacheVersion, setReferenceDocumentCacheVersion] = useState(0);

  const resetReferenceChunkSelector = useCallback(() => {
    setReferenceChunkSelector({
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
    });
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

  const getHeaderCellProps = useCallback(
    (columnKey: ResizableColumnKey) => ({
      columnKey,
      columnWidth: columnWidths[columnKey],
      onResizeColumn: handleColumnResize,
    }) as ResizableHeaderCellProps,
    [columnWidths, handleColumnResize],
  );

  const loadDetail = async () => {
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
        listQuestionTypes().catch(() => []),
      ]);
      setDataset(datasetDetail);
      setItems(itemList.items);
      setTotal(itemList.total);
      if (remoteQuestionTypes.length > 0) {
        setQuestionTypeOptions(remoteQuestionTypes);
      }
    } catch (error: any) {
      message.error(error?.message || "数据集加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadDetail();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [datasetId, pagination.current, pagination.pageSize]);

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
      !visibleColumnKeys.includes(editableFieldColumnMap[activeCell.field])
    ) {
      setActiveCell(null);
    }
  }, [activeCell, visibleColumnKeys]);

  const confirmDiscardDirty = () =>
    new Promise<boolean>((resolve) => {
      if (dirtyItemIds.length === 0 && !newItemVisible) {
        resolve(true);
        return;
      }
      Modal.confirm({
        title: "存在未保存的编辑",
        content: "切换后当前编辑内容将丢失，是否继续？",
        okText: "继续",
        cancelText: "取消",
        onOk: () => resolve(true),
        onCancel: () => resolve(false),
      });
    });

  const handleFilterSearch = async () => {
    const canContinue = await confirmDiscardDirty();
    if (!canContinue) {
      return;
    }
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
    setDrafts((current) => ({
      ...current,
      [record.id]: {
        ...(current[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record)),
        reference_doc: value || "",
        reference_doc_ids: "",
        reference_chunk_ids: "",
      },
    }));
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
    if (option.datasetId) {
      referenceDocumentCacheRef.current[option.documentId] = {
        datasetId: option.datasetId,
        documentId: option.documentId,
        name: option.name,
      };
      setReferenceDocumentCacheVersion((version) => version + 1);
    }
    setDrafts((current) => ({
      ...current,
      [record.id]: {
        ...(current[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record)),
        reference_doc: option.name,
        reference_doc_ids: option.documentId,
        reference_chunk_ids: "",
      },
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
  };

  const resolveReferenceDocument = useCallback(
    async (documentId: string, fallbackName: string) => {
      const normalizedDocumentId = `${documentId || ""}`.trim();
      if (!normalizedDocumentId) {
        throw new Error("请先选择知识库文档");
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
        throw new Error("未找到文档所属知识库，请重新从下拉中选择参考文档");
      }

      const matchedDocument = await findKnowledgeBaseDocumentById(
        knowledgeBaseIds,
        normalizedDocumentId,
      );
      if (!matchedDocument?.datasetId) {
        throw new Error("未找到文档所属知识库，请重新从下拉中选择参考文档");
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
    [dataset?.knowledge_bases],
  );

  const handleOpenReferenceChunkSelector = useCallback(
    async (record: DatasetItem) => {
      const draft = drafts[record.id] || createItemDraft(record.id === NEW_ITEM_ID ? undefined : record);
      const documentId = `${draft.reference_doc_ids || ""}`
        .split(",")
        .map((item) => item.trim())
        .find(Boolean) || "";
      if (!documentId) {
        message.warning("请先在参考文档中选择平台知识库文档");
        return;
      }

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
        selectedChunkIds: `${draft.reference_chunk_ids || ""}`
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
          error: chunks.length === 0 ? "该文档暂无可选 chunk" : "",
        }));
      } catch (error: any) {
        setReferenceChunkSelector((current) => ({
          ...current,
          loading: false,
          error: error?.message || "知识库文档加载失败",
        }));
      }
    },
    [drafts, resolveReferenceDocument],
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

      return false;
    },
    [drafts, referenceDocumentCacheVersion],
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
      message.warning("请至少勾选一个 chunk");
      return;
    }

    const selectedChunkContent = selectedChunks
      .map((chunk) => `${chunk.display_content || chunk.content || ""}`.trim())
      .filter(Boolean)
      .join("\n\n");
    const selectedChunkIds = selectedChunks
      .map((chunk) => `${chunk.segment_id || ""}`.trim())
      .filter(Boolean)
      .join(", ");

    setReferenceChunkSelector((current) => ({
      ...current,
      confirming: true,
    }));

    setDrafts((current) => ({
      ...current,
      [itemId]: {
        ...(current[itemId] || createItemDraft(items.find((item) => item.id === itemId))),
        reference_context: selectedChunkContent,
        reference_chunk_ids: selectedChunkIds,
      },
    }));
    setDirtyItemIds((current) => (current.includes(itemId) ? current : [...current, itemId]));

    resetReferenceChunkSelector();
  }, [referenceChunkSelector, resetReferenceChunkSelector]);

  const handleCancelItem = (item: DatasetItem) => {
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

  const handleSaveItem = async (itemId: string, values: DatasetItemFormValues) => {
    const validationErrors = validateRequiredDatasetItem(values);
    if (validationErrors.length > 0) {
      message.warning(validationErrors[0]);
      return;
    }
    setSaving(true);
    try {
      if (itemId === NEW_ITEM_ID) {
        await createDatasetItem(datasetId, values);
        message.success("样本已新增");
        setNewItemVisible(false);
        setActiveCell(null);
      } else {
        const currentItem = items.find((item) => item.id === itemId);
        await updateDatasetItem(
          datasetId,
          itemId,
          currentItem ? mergeHiddenItemFields(currentItem, values) : values,
        );
        message.success("样本已保存");
      }
      if (activeCell?.itemId === itemId) {
        setActiveCell(null);
      }
      setDirtyItemIds((current) => current.filter((id) => id !== itemId));
      await loadDetail();
    } catch (error: any) {
      message.error(error?.message || "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const handleAutoSaveItem = async (item: DatasetItem) => {
    const draft = drafts[item.id] || createItemDraft(item);
    if (item.id !== NEW_ITEM_ID && !dirtyItemIds.includes(item.id)) {
      setActiveCell(null);
      return;
    }
    if (item.id === NEW_ITEM_ID && validateRequiredDatasetItem(draft).length > 0) {
      setActiveCell(null);
      return;
    }
    await handleSaveItem(item.id, draft);
  };

  const handleDeleteItem = (item: DatasetItem) => {
    if (item.id === NEW_ITEM_ID) {
      handleCancelItem(item);
      return;
    }
    Modal.confirm({
      title: "确认删除该样本？",
      content: item.question,
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await deleteDatasetItem(datasetId, item.id);
        message.success("样本已删除");
        await loadDetail();
      },
    });
  };

  const handleBatchDelete = () => {
    if (selectedRowKeys.length === 0) {
      message.warning("请先选择样本");
      return;
    }
    Modal.confirm({
      title: `确认删除 ${selectedRowKeys.length} 条样本？`,
      content: "删除后将从当前表格中移除这些样本。",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await batchDeleteDatasetItems(datasetId, selectedRowKeys.map(String));
        setSelectedRowKeys([]);
        message.success("样本已批量删除");
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
    message.success("导入完成");
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
      question: "新建样本",
      question_type: "",
      ground_truth: "",
      source: "manual",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      created_by: "当前用户",
    };
    return [newItem, ...items];
  }, [datasetId, items, newItemVisible]);

  const renderCellDisplay = (
    record: DatasetItem,
    field: EditableDatasetItemField,
    placeholder: string,
  ) => {
    const value = drafts[record.id]?.[field] || "";
    const shouldShowReferenceChunkSelector =
      field === "reference_context" && canSelectReferenceChunks(record);
    return (
      <div className="dataset-inline-display-wrapper">
        <button
          type="button"
          className="dataset-inline-display"
          onClick={() => setActiveCell({ itemId: record.id, field })}
        >
          {value || <span className="dataset-inline-placeholder">{placeholder}</span>}
        </button>
        {shouldShowReferenceChunkSelector ? (
          <Button
            size="small"
            type="link"
            className="dataset-reference-chunk-trigger"
            onClick={() => void handleOpenReferenceChunkSelector(record)}
          >
            选择 chunk
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
      return renderCellDisplay(record, "reference_doc", "请输入参考文档");
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
        placeholder="请输入参考文档，输入 @ 可搜索知识库文档"
        notFoundContent={searchState?.loading ? "搜索中..." : "暂无匹配文档"}
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
        {field === "reference_context" && canSelectReferenceChunks(record) ? (
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
            选择 chunk
          </Button>
        ) : null}
      </div>
    );
  };

  const renderQuestionTypeCell = (record: DatasetItem) => {
    if (activeCell?.itemId !== record.id || activeCell.field !== "question_type") {
      return renderCellDisplay(record, "question_type", "请选择问题类型");
    }
    return (
      <QuestionTypeSelect
        value={drafts[record.id]?.question_type || undefined}
        placeholder="问题类型"
        onChange={(value) => handleDraftChange(record, "question_type", value)}
        onBlur={() => void handleAutoSaveItem(record)}
      />
    );
  };

  const columns = useMemo<ColumnsType<DatasetItem>>(() => {
    const allColumns: ColumnsType<DatasetItem> = [
      {
        title: "问题",
        dataIndex: "question",
        key: "question",
        width: columnWidths.question,
        onHeaderCell: () => getHeaderCellProps("question"),
        render: (_, record) => renderInlineInput(record, "question", "请输入问题"),
      },
      {
        title: "问题类型",
        dataIndex: "question_type",
        key: "question_type",
        width: columnWidths.question_type,
        onHeaderCell: () => getHeaderCellProps("question_type"),
        render: (_, record) => renderQuestionTypeCell(record),
      },
      {
        title: "标准答案",
        dataIndex: "ground_truth",
        key: "ground_truth",
        width: columnWidths.ground_truth,
        onHeaderCell: () => getHeaderCellProps("ground_truth"),
        render: (_, record) =>
          renderInlineTextArea(record, "ground_truth", "请输入标准答案"),
      },
      {
        title: "答案要点",
        dataIndex: "key_points",
        key: "key_points",
        width: columnWidths.key_points,
        onHeaderCell: () => getHeaderCellProps("key_points"),
        render: (_, record) =>
          renderInlineTextArea(record, "key_points", "请输入答案要点"),
      },
      {
        title: "参考文档",
        dataIndex: "reference_doc",
        key: "reference_doc",
        width: columnWidths.reference_doc,
        onHeaderCell: () => getHeaderCellProps("reference_doc"),
        render: (_, record) => renderReferenceDocumentInput(record),
      },
      {
        title: "参考上下文",
        dataIndex: "reference_context",
        key: "reference_context",
        width: columnWidths.reference_context,
        onHeaderCell: () => getHeaderCellProps("reference_context"),
        render: (_, record) =>
          renderInlineTextArea(record, "reference_context", "请输入参考上下文"),
      },
      {
        title: "生成依据",
        dataIndex: "generate_reason",
        key: "generate_reason",
        width: columnWidths.generate_reason,
        onHeaderCell: () => getHeaderCellProps("generate_reason"),
        render: (_, record) =>
          renderInlineTextArea(record, "generate_reason", "请输入生成依据"),
      },
      {
        title: "来源",
        dataIndex: "source",
        key: "source",
        width: columnWidths.source,
        onHeaderCell: () => getHeaderCellProps("source"),
        render: (value: DatasetItemSource) => <SourceTypeTag source={value} />,
      },
      {
        title: "更新时间",
        dataIndex: "updated_at",
        key: "updated_at",
        width: columnWidths.updated_at,
        onHeaderCell: () => getHeaderCellProps("updated_at"),
        render: (value) => formatDateTime(value),
      },
      {
        title: "操作",
        key: "actions",
        width: columnWidths.actions,
        fixed: "right",
        onHeaderCell: () => getHeaderCellProps("actions"),
        render: (_, record) => (
          <Button
            danger
            size="small"
            icon={<DeleteOutlined />}
            onClick={() => handleDeleteItem(record)}
          >
            删除
          </Button>
        ),
      },
    ];

    return allColumns.filter((column) => {
      if (column.key === "actions") {
        return true;
      }
      return visibleColumnKeys.includes(column.key as ConfigurableColumnKey);
    });
  }, [
    columnWidths,
    dirtyItemIds,
    drafts,
    activeCell,
    documentSearchState,
    getHeaderCellProps,
    saving,
    visibleColumnKeys,
  ]);

  const tableScrollX = useMemo(
    () =>
      visibleColumnKeys.reduce(
        (total, columnKey) => total + columnWidths[columnKey],
        columnWidths.actions + 96,
      ),
    [columnWidths, visibleColumnKeys],
  );
  const tableStyle = {
    "--dataset-table-row-height": `${rowHeight}px`,
  } as CSSProperties;
  const columnSettingsContent = (
    <div className="dataset-column-settings">
      <div className="dataset-column-settings-header">
        <span>选择展示列</span>
        <Button
          type="link"
          size="small"
          onClick={() => setVisibleColumnKeys(DEFAULT_VISIBLE_COLUMN_KEYS)}
        >
          恢复默认
        </Button>
      </div>
      <Checkbox.Group
        className="dataset-column-settings-options"
        value={visibleColumnKeys}
        options={CONFIGURABLE_COLUMN_OPTIONS}
        onChange={(values) => setVisibleColumnKeys(values as ConfigurableColumnKey[])}
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
          数据集管理 / {dataset?.name || "数据集详情"}
        </Button>
      </div>

      <Card className="dataset-detail-card">
        <div className="dataset-detail-actions">
          <Space wrap>
            <Button type="primary" icon={<PlusOutlined />} onClick={handleAddItem}>
              新增样本
            </Button>
            <Button icon={<ImportOutlined />} onClick={() => setImportModalOpen(true)}>
              导入数据
            </Button>
            <Button danger icon={<DeleteOutlined />} onClick={handleBatchDelete}>
              批量删除
            </Button>
            <Popover
              trigger="click"
              placement="bottomRight"
              content={columnSettingsContent}
            >
              <Button icon={<SettingOutlined />}>列设置</Button>
            </Popover>
          </Space>
        </div>

        <div className="dataset-detail-filters">
          <Input
            allowClear
            className="dataset-detail-search"
            prefix={<SearchOutlined />}
            placeholder="搜索问题/答案"
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={handleFilterSearch}
          />
          <div className="dataset-filter-controls">
            <QuestionTypeSelect
              allowClear
              value={questionType}
              onChange={setQuestionType}
              placeholder="问题类型"
              options={questionTypeOptions}
            />
            <Select
              allowClear
              className="dataset-source-filter"
              value={source}
              placeholder="来源"
              onChange={setSource}
              options={(["upload", "manual", "flowback"] as const).map((value) => ({
                label: sourceLabelMap[value],
                value,
              }))}
            />
            <Button type="primary" onClick={handleFilterSearch}>
              查询
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
                description="暂无样本数据"
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
            showTotal: (currentTotal) => `共 ${currentTotal} 条`,
            onChange: async (current, pageSize) => {
              const canContinue = await confirmDiscardDirty();
              if (!canContinue) {
                return;
              }
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
        title={`选择参考上下文${referenceChunkSelector.documentName ? ` - ${referenceChunkSelector.documentName}` : ""}`}
        width={1200}
        okText="确定"
        cancelText="取消"
        confirmLoading={referenceChunkSelector.confirming}
        onOk={() => void handleConfirmReferenceChunks()}
        onCancel={resetReferenceChunkSelector}
        destroyOnClose
      >
        <div className="dataset-reference-modal">
          <div className="dataset-reference-modal-panel">
            <div className="dataset-reference-modal-panel-header">文档原文</div>
            <div className="dataset-reference-modal-panel-body dataset-reference-modal-preview">
              {referenceChunkSelector.loading ? (
                <div className="dataset-reference-modal-empty">加载中...</div>
              ) : referenceChunkSelector.documentPreviewUrl ? (
                <FileViewer
                  file={referenceChunkSelector.documentPreviewUrl}
                  fileName={referenceChunkSelector.documentName}
                  segment={referenceChunkSelector.previewSegment}
                />
              ) : (
                <div className="dataset-reference-modal-empty">当前文档暂不支持原文预览</div>
              )}
            </div>
          </div>
          <div className="dataset-reference-modal-panel">
            <div className="dataset-reference-modal-panel-header">
              已解析 chunk（已选 {referenceChunkSelector.selectedChunkIds.length}）
            </div>
            <div className="dataset-reference-modal-panel-body">
              {referenceChunkSelector.error ? (
                <div className="dataset-reference-modal-empty">{referenceChunkSelector.error}</div>
              ) : referenceChunkSelector.loading ? (
                <div className="dataset-reference-modal-empty">加载中...</div>
              ) : referenceChunkSelector.chunks.length === 0 ? (
                <div className="dataset-reference-modal-empty">暂无可选 chunk</div>
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
                            Chunk {index + 1}
                            {segmentId ? ` · ${segmentId}` : ""}
                          </div>
                          <div className="dataset-reference-chunk-text">
                            {content || "该 chunk 暂无文本内容"}
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
