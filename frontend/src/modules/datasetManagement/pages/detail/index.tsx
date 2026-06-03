import { useCallback, useEffect, useMemo, useState } from "react";
import type {
  CSSProperties,
  Key,
  MouseEvent as ReactMouseEvent,
  ThHTMLAttributes,
  HTMLAttributes,
} from "react";
import {
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
  getDataset,
  importDatasetItems,
  listDatasetItems,
  updateDatasetItem,
} from "../../api";
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
type ConfigurableColumnKey = Exclude<ResizableColumnKey, "actions">;

const CONFIGURABLE_COLUMN_OPTIONS: Array<{
  label: string;
  value: ConfigurableColumnKey;
}> = [
  { label: "问题", value: "question" },
  { label: "问题类型", value: "question_type" },
  { label: "标准答案", value: "ground_truth" },
  { label: "答案要点", value: "key_points" },
  { label: "参考上下文", value: "reference_context" },
  { label: "参考文档", value: "reference_doc" },
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
    reference_doc_ids: joinListField(item.reference_doc_ids) || values.reference_doc_ids,
    reference_chunk_ids:
      joinListField(item.reference_chunk_ids) || values.reference_chunk_ids,
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
      const [datasetDetail, itemList] = await Promise.all([
        getDataset(datasetId),
        listDatasetItems(datasetId, {
          keyword,
          question_type: questionType,
          source,
        }),
      ]);
      setDataset(datasetDetail);
      setItems(itemList);
    } catch (error: any) {
      message.error(error?.message || "数据集加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadDetail();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [datasetId]);

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
    return (
      <button
        type="button"
        className="dataset-inline-display"
        onClick={() => setActiveCell({ itemId: record.id, field })}
      >
        {value || <span className="dataset-inline-placeholder">{placeholder}</span>}
      </button>
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

  const renderInlineTextArea = (
    record: DatasetItem,
    field: EditableDatasetItemField,
    placeholder: string,
  ) => {
    if (activeCell?.itemId !== record.id || activeCell.field !== field) {
      return renderCellDisplay(record, field, placeholder);
    }
    return (
      <TextArea
        autoFocus
        className="dataset-inline-textarea"
        value={drafts[record.id]?.[field] || ""}
        placeholder={placeholder}
        autoSize={{ minRows: 1, maxRows: 4 }}
        onChange={(event) => handleDraftChange(record, field, event.target.value)}
        onBlur={() => void handleAutoSaveItem(record)}
      />
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
        title: "参考上下文",
        dataIndex: "reference_context",
        key: "reference_context",
        width: columnWidths.reference_context,
        onHeaderCell: () => getHeaderCellProps("reference_context"),
        render: (_, record) =>
          renderInlineTextArea(record, "reference_context", "请输入参考上下文"),
      },
      {
        title: "参考文档",
        dataIndex: "reference_doc",
        key: "reference_doc",
        width: columnWidths.reference_doc,
        onHeaderCell: () => getHeaderCellProps("reference_doc"),
        render: (_, record) =>
          renderInlineInput(record, "reference_doc", "请输入参考文档"),
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
            showTotal: (total) => `共 ${total} 条`,
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
    </div>
  );
}
