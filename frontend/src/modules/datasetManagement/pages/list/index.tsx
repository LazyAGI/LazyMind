import { useEffect, useMemo, useState } from "react";
import {
  Button,
  Card,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  createDataset,
  deleteDataset,
  getDataset,
  listDatasets,
  listKnowledgeBases,
  updateDataset,
} from "../../api";
import DatasetFormModal from "../../components/DatasetFormModal";
import type {
  DatasetFormValues,
  DatasetListItem,
  KnowledgeBaseOption,
} from "../../shared";
import { formatDateTime } from "../../shared";
import "../../index.scss";

const { Text, Paragraph } = Typography;

export default function DatasetListPage() {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [datasets, setDatasets] = useState<DatasetListItem[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBaseOption[]>([]);
  const [keyword, setKeyword] = useState("");
  const [loading, setLoading] = useState(false);
  const [formModalOpen, setFormModalOpen] = useState(false);
  const [editingDataset, setEditingDataset] = useState<DatasetListItem | null>(null);
  const [editingDatasetId, setEditingDatasetId] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [editingLoadingId, setEditingLoadingId] = useState("");

  const loadDatasets = async (nextKeyword = keyword) => {
    setLoading(true);
    try {
      const [datasetList, kbList] = await Promise.all([
        listDatasets(nextKeyword),
        listKnowledgeBases(),
      ]);
      setDatasets(datasetList);
      setKnowledgeBases(kbList);
    } catch (error: any) {
      message.error(error?.message || t("datasetManagement.list.message.loadFailed"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadDatasets("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSearch = () => {
    void loadDatasets(keyword);
  };

  const handleOpenCreate = () => {
    setEditingDataset(null);
    setEditingDatasetId("");
    setFormModalOpen(true);
  };

  const handleOpenEdit = async (dataset: DatasetListItem) => {
    setEditingLoadingId(dataset.id);
    try {
      const detail = await getDataset(dataset.id);
      setEditingDataset(detail);
      setEditingDatasetId(detail.id);
      setFormModalOpen(true);
    } catch (error: any) {
      message.error(error?.message || t("datasetManagement.list.message.detailLoadFailed"));
    } finally {
      setEditingLoadingId("");
    }
  };

  const handleCloseFormModal = () => {
    setFormModalOpen(false);
    setEditingDataset(null);
    setEditingDatasetId("");
  };

  const handleDelete = (dataset: DatasetListItem) => {
    Modal.confirm({
      title: t("datasetManagement.list.confirmDeleteTitle", { name: dataset.name }),
      content: t("datasetManagement.list.confirmDeleteContent"),
      okText: t("common.delete"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        await deleteDataset(dataset.id);
        message.success(t("datasetManagement.list.message.deleted"));
        await loadDatasets();
      },
    });
  };

  const handleSubmitDataset = async (values: DatasetFormValues) => {
    setSubmitting(true);
    try {
      if (editingDatasetId) {
        await updateDataset(editingDatasetId, {
          name: values.name,
          description: values.description,
          knowledge_base_ids: values.knowledge_base_ids,
        });
        message.success(t("datasetManagement.list.message.updated"));
        handleCloseFormModal();
        await loadDatasets();
        return;
      }

      const created = await createDataset({
        name: values.name,
        description: values.description,
        knowledge_base_ids: values.knowledge_base_ids,
      });

      message.success(t("datasetManagement.list.message.created"));
      handleCloseFormModal();
      navigate(`/dataset-management/${created.id}`);
    } catch {
      // The global axios interceptor already shows the backend error message.
    } finally {
      setSubmitting(false);
    }
  };

  const columns = useMemo<ColumnsType<DatasetListItem>>(
    () => [
      {
        title: t("datasetManagement.fields.datasetName"),
        dataIndex: "name",
        width: 220,
        render: (_, record) => (
          <div className="dataset-name-cell">
            <Button
              type="link"
              className="dataset-link-button"
              onClick={() => navigate(`/dataset-management/${record.id}`)}
            >
              <Tooltip title={record.name}>
                <span className="dataset-name-text">{record.name}</span>
              </Tooltip>
            </Button>
            <Tooltip title={record.description || ""}>
              <Paragraph className="dataset-description" ellipsis={{ rows: 1 }}>
                {record.description || "-"}
              </Paragraph>
            </Tooltip>
          </div>
        ),
      },
      {
        title: t("datasetManagement.fields.knowledgeBase"),
        dataIndex: "knowledge_bases",
        width: 220,
        className: "dataset-kb-column",
        render: (_, record) =>
          record.knowledge_bases?.length ? (
            <div className="dataset-kb-scroll-list">
              {record.knowledge_bases.map((item) => (
                <Tag key={item.id} className="dataset-kb-scroll-tag">
                  {item.name}
                </Tag>
              ))}
            </div>
          ) : (
            <Text type="secondary">-</Text>
          ),
      },
      {
        title: t("datasetManagement.fields.sampleCount"),
        dataIndex: "sample_count",
        width: 100,
        render: (value) => value ?? 0,
      },
      {
        title: t("datasetManagement.fields.owner"),
        dataIndex: "owner_id",
        width: 120,
        render: (_, record) => record.owner_name || record.owner_id || "-",
      },
      {
        title: t("datasetManagement.fields.updatedAt"),
        dataIndex: "updated_at",
        width: 150,
        render: (value) => formatDateTime(value),
      },
      {
        title: t("common.actions"),
        width: 240,
        fixed: "right",
        render: (_, record) => (
          <Space>
            <Button
              type="link"
              size="small"
              icon={<EyeOutlined />}
              onClick={() => navigate(`/dataset-management/${record.id}`)}
            >
              {t("datasetManagement.list.enter")}
            </Button>
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              loading={editingLoadingId === record.id}
              onClick={() => handleOpenEdit(record)}
            >
              {t("common.edit")}
            </Button>
            <Button
              type="link"
              danger
              size="small"
              icon={<DeleteOutlined />}
              onClick={() => handleDelete(record)}
            >
              {t("common.delete")}
            </Button>
          </Space>
        ),
      },
    ],
    [editingLoadingId, navigate, t],
  );

  return (
    <div className="dataset-page">
      <div className="dataset-page-header">
        <div>
          <h2 className="admin-page-title">{t("datasetManagement.list.title")}</h2>
          <p className="dataset-page-subtitle">
            {t("datasetManagement.list.subtitle")}
          </p>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={handleOpenCreate}>
          {t("datasetManagement.list.createDataset")}
        </Button>
      </div>

      <Card className="dataset-list-card">
        <div className="dataset-list-toolbar">
          <Input
            allowClear
            className="dataset-search-input"
            prefix={<SearchOutlined />}
            placeholder={t("datasetManagement.list.searchPlaceholder")}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={handleSearch}
          />
          <Button onClick={handleSearch}>{t("common.search")}</Button>
        </div>
        <Table
          rowKey="id"
          className="dataset-table"
          loading={loading}
          columns={columns}
          dataSource={datasets}
          scroll={{ x: 1050, y: "calc(100vh - 340px)" }}
          tableLayout="fixed"
          pagination={{
            pageSize: 10,
            showTotal: (total) => t("common.totalItems", { total }),
          }}
        />
      </Card>

      <DatasetFormModal
        key={editingDatasetId || "create"}
        open={formModalOpen}
        mode={editingDatasetId ? "edit" : "create"}
        dataset={editingDataset}
        knowledgeBases={knowledgeBases}
        submitting={submitting}
        onCancel={handleCloseFormModal}
        onSubmit={handleSubmitDataset}
      />
    </div>
  );
}
