import { useEffect, useMemo, useState } from "react";
import {
  Button,
  Card,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ArrowLeftOutlined,
  DeleteOutlined,
  DownOutlined,
  ImportOutlined,
  PlusOutlined,
  RightOutlined,
  SearchOutlined,
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
import DatasetExpandedRowEditor from "../../components/DatasetExpandedRowEditor";
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
import "../../index.scss";

const { Paragraph } = Typography;
const NEW_ITEM_ID = "__new_dataset_item__";

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
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
  const [expandedItemId, setExpandedItemId] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [importModalOpen, setImportModalOpen] = useState(false);
  const [newItemVisible, setNewItemVisible] = useState(false);

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

  const confirmDiscardDirty = () =>
    new Promise<boolean>((resolve) => {
      if (!dirty) {
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
    setExpandedItemId(null);
    setDirty(false);
    setPagination((current) => ({ ...current, current: 1 }));
    await loadDetail();
  };

  const handleExpandItem = async (itemId: string) => {
    if (expandedItemId === itemId) {
      const canContinue = await confirmDiscardDirty();
      if (!canContinue) {
        return;
      }
      setExpandedItemId(null);
      setDirty(false);
      if (itemId === NEW_ITEM_ID) {
        setNewItemVisible(false);
      }
      return;
    }
    const canContinue = await confirmDiscardDirty();
    if (!canContinue) {
      return;
    }
    setExpandedItemId(itemId);
    setDirty(false);
  };

  const handleAddItem = async () => {
    const canContinue = await confirmDiscardDirty();
    if (!canContinue) {
      return;
    }
    setNewItemVisible(true);
    setExpandedItemId(NEW_ITEM_ID);
    setDirty(false);
    setPagination((current) => ({ ...current, current: 1 }));
  };

  const handleSaveItem = async (itemId: string, values: DatasetItemFormValues) => {
    setSaving(true);
    try {
      if (itemId === NEW_ITEM_ID) {
        await createDatasetItem(datasetId, values);
        message.success("样本已新增");
        setNewItemVisible(false);
      } else {
        await updateDatasetItem(datasetId, itemId, values);
        message.success("样本已保存");
      }
      setExpandedItemId(null);
      setDirty(false);
      await loadDetail();
    } catch (error: any) {
      message.error(error?.message || "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const handleDeleteItem = (item: DatasetItem) => {
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

  const columns = useMemo<ColumnsType<DatasetItem>>(
    () => [
      {
        title: "Case ID",
        dataIndex: "case_id",
        width: 130,
        render: (value) => value || "-",
      },
      {
        title: "问题",
        dataIndex: "question",
        ellipsis: true,
        render: (value) => <Paragraph ellipsis={{ rows: 1 }}>{value || "-"}</Paragraph>,
      },
      {
        title: "问题类型",
        dataIndex: "question_type",
        width: 130,
        render: (value) => value || "-",
      },
      {
        title: "标准答案",
        dataIndex: "ground_truth",
        ellipsis: true,
        render: (value) => <Paragraph ellipsis={{ rows: 1 }}>{value || "-"}</Paragraph>,
      },
      {
        title: "参考文档",
        dataIndex: "reference_doc",
        width: 160,
        ellipsis: true,
        render: (value) => value || "-",
      },
      {
        title: "来源",
        dataIndex: "source",
        width: 100,
        render: (value: DatasetItemSource) => <SourceTypeTag source={value} />,
      },
      {
        title: "更新时间",
        dataIndex: "updated_at",
        width: 150,
        render: (value) => formatDateTime(value),
      },
      {
        title: "操作",
        width: 120,
        fixed: "right",
        render: (_, record) =>
          record.id === NEW_ITEM_ID ? null : (
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              onClick={(event) => {
                event.stopPropagation();
                handleDeleteItem(record);
              }}
            >
              删除
            </Button>
          ),
      },
    ],
    [datasetId],
  );

  const expandedRowRender = (record: DatasetItem) => (
    <DatasetExpandedRowEditor
      item={record.id === NEW_ITEM_ID ? undefined : record}
      isNew={record.id === NEW_ITEM_ID}
      saving={saving}
      onDirtyChange={setDirty}
      onCancel={() => {
        setExpandedItemId(null);
        setDirty(false);
        if (record.id === NEW_ITEM_ID) {
          setNewItemVisible(false);
        }
      }}
      onSave={(values) => handleSaveItem(record.id, values)}
    />
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
          loading={loading}
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
          expandable={{
            expandedRowRender,
            expandedRowKeys: expandedItemId ? [expandedItemId] : [],
            expandIcon: ({ expanded, record }) => (
              <button
                type="button"
                className="dataset-row-expand-button"
                aria-label={expanded ? "收起编辑区" : "展开编辑区"}
                onClick={(event) => {
                  event.stopPropagation();
                  void handleExpandItem(record.id);
                }}
              >
                {expanded ? <DownOutlined /> : <RightOutlined />}
              </button>
            ),
          }}
          onRow={(record) => ({
            className: record.id === expandedItemId ? "is-editing-row" : "",
            onClick: () => {
              void handleExpandItem(record.id);
            },
          })}
          scroll={{ x: 1200 }}
          pagination={{
            current: pagination.current,
            pageSize: pagination.pageSize,
            showTotal: (total) => `共 ${total} 条`,
            onChange: async (current, pageSize) => {
              const canContinue = await confirmDiscardDirty();
              if (!canContinue) {
                return;
              }
              setExpandedItemId(null);
              setDirty(false);
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
