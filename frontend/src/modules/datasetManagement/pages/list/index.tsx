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
import { useNavigate } from "react-router-dom";
import {
  createDataset,
  deleteDataset,
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
  const [datasets, setDatasets] = useState<DatasetListItem[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBaseOption[]>([]);
  const [keyword, setKeyword] = useState("");
  const [loading, setLoading] = useState(false);
  const [formModalOpen, setFormModalOpen] = useState(false);
  const [editingDataset, setEditingDataset] = useState<DatasetListItem | null>(null);
  const [submitting, setSubmitting] = useState(false);

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
      message.error(error?.message || "数据集加载失败");
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
    setFormModalOpen(true);
  };

  const handleOpenEdit = (dataset: DatasetListItem) => {
    setEditingDataset(dataset);
    setFormModalOpen(true);
  };

  const handleDelete = (dataset: DatasetListItem) => {
    Modal.confirm({
      title: `确认删除 ${dataset.name}？`,
      content: "删除后会影响该数据集下的全部样本，请谨慎操作。",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await deleteDataset(dataset.id);
        message.success("数据集已删除");
        await loadDatasets();
      },
    });
  };

  const handleSubmitDataset = async (values: DatasetFormValues) => {
    setSubmitting(true);
    try {
      if (editingDataset) {
        await updateDataset(editingDataset.id, {
          name: values.name,
          description: values.description,
          knowledge_base_ids: values.knowledge_base_ids,
        });
        message.success("数据集已更新");
        setFormModalOpen(false);
        await loadDatasets();
        return;
      }

      const created = await createDataset({
        name: values.name,
        description: values.description,
        knowledge_base_ids: values.knowledge_base_ids,
      });

      message.success("数据集已创建");
      setFormModalOpen(false);
      navigate(`/dataset-management/${created.id}`);
    } catch (error: any) {
      message.error(error?.message || "保存失败");
    } finally {
      setSubmitting(false);
    }
  };

  const columns = useMemo<ColumnsType<DatasetListItem>>(
    () => [
      {
        title: "数据集名称",
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
        title: "关联知识库",
        dataIndex: "knowledge_bases",
        width: 220,
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
        title: "样本数",
        dataIndex: "sample_count",
        width: 100,
        render: (value) => value ?? 0,
      },
      {
        title: "创建人",
        dataIndex: "owner_id",
        width: 120,
        render: (_, record) => record.owner_name || record.owner_id || "-",
      },
      {
        title: "更新时间",
        dataIndex: "updated_at",
        width: 150,
        render: (value) => formatDateTime(value),
      },
      {
        title: "操作",
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
              进入
            </Button>
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              onClick={() => handleOpenEdit(record)}
            >
              编辑
            </Button>
            <Button
              type="link"
              danger
              size="small"
              icon={<DeleteOutlined />}
              onClick={() => handleDelete(record)}
            >
              删除
            </Button>
          </Space>
        ),
      },
    ],
    [navigate],
  );

  return (
    <div className="dataset-page">
      <div className="dataset-page-header">
        <div>
          <h2 className="admin-page-title">数据集管理</h2>
          <p className="dataset-page-subtitle">
            统一维护问答评测样本，支持手动编辑、文件导入和算法回流数据展示。
          </p>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={handleOpenCreate}>
          新建数据集
        </Button>
      </div>

      <Card className="dataset-list-card">
        <div className="dataset-list-toolbar">
          <Input
            allowClear
            className="dataset-search-input"
            prefix={<SearchOutlined />}
            placeholder="搜索数据集名称/描述"
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={handleSearch}
          />
          <Button onClick={handleSearch}>搜索</Button>
        </div>
        <Table
          rowKey="id"
          className="dataset-table"
          loading={loading}
          columns={columns}
          dataSource={datasets}
          scroll={{ x: 1050 }}
          pagination={{
            pageSize: 10,
            showTotal: (total) => `共 ${total} 条`,
          }}
        />
      </Card>

      <DatasetFormModal
        open={formModalOpen}
        mode={editingDataset ? "edit" : "create"}
        dataset={editingDataset}
        knowledgeBases={knowledgeBases}
        submitting={submitting}
        onCancel={() => setFormModalOpen(false)}
        onSubmit={handleSubmitDataset}
      />
    </div>
  );
}
