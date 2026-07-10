import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Modal,
  Space,
  Steps,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  FileTextOutlined,
  PlusOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  checkDatabaseConnection,
  createDatabaseConnection,
  deleteDatabaseConnection,
  listDatabaseConnections,
  updateDatabaseConnection,
  type DatabaseConnectionItem,
  type DatabaseConnectionPayload,
} from "../api/databaseConnections";
import DatabaseConnectionModal from "./DatabaseConnectionModal";
import "../index.scss";

const { Paragraph, Text } = Typography;

export default function DatabaseConnectionsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [items, setItems] = useState<DatabaseConnectionItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [checkingId, setCheckingId] = useState<string>("");
  const [modalOpen, setModalOpen] = useState(false);
  const [guideOpen, setGuideOpen] = useState(false);
  const [editing, setEditing] = useState<DatabaseConnectionItem | null>(null);

  const refresh = async () => {
    setLoading(true);
    try {
      const result = await listDatabaseConnections();
      setItems(result.connections || []);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("admin.dataSourceDatabaseLoadFailed")));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const openCreate = () => {
    setEditing(null);
    setModalOpen(true);
  };

  const openEdit = (record: DatabaseConnectionItem) => {
    setEditing(record);
    setModalOpen(true);
  };

  const handleSubmit = async (payload: DatabaseConnectionPayload) => {
    setSaving(true);
    try {
      if (editing) {
        await updateDatabaseConnection(editing.id, payload);
      } else {
        await createDatabaseConnection(payload);
      }
      message.success(editing ? t("admin.dataSourceDatabaseUpdated") : t("admin.dataSourceDatabaseCreated"));
      setModalOpen(false);
      await refresh();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("admin.dataSourceDatabaseSaveFailed")));
    } finally {
      setSaving(false);
    }
  };

  const handleCheck = async (record: DatabaseConnectionItem) => {
    setCheckingId(record.id);
    try {
      const result = await checkDatabaseConnection(record.id);
      if (result.success) {
        message.success(t("admin.dataSourceDatabaseCheckSuccess", { count: result.table_count }));
      } else {
        message.error(result.message || t("admin.dataSourceDatabaseCheckFailed"));
      }
      await refresh();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("admin.dataSourceDatabaseCheckFailed")));
    } finally {
      setCheckingId("");
    }
  };

  const handleDelete = (record: DatabaseConnectionItem) => {
    Modal.confirm({
      title: t("admin.dataSourceDatabaseDeleteTitle"),
      content: t("admin.dataSourceDatabaseDeleteContent", { name: record.display_name }),
      okText: t("common.delete"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        try {
          await deleteDatabaseConnection(record.id);
          message.success(t("admin.dataSourceDatabaseDeleted"));
          await refresh();
        } catch (error) {
          message.error(getLocalizedErrorMessage(error, t("admin.dataSourceDatabaseDeleteFailed")));
          throw error;
        }
      },
    });
  };

  const columns: ColumnsType<DatabaseConnectionItem> = [
    {
      title: t("admin.dataSourceDatabaseName"),
      dataIndex: "display_name",
      key: "display_name",
      width: 360,
      render: (value, record) => (
        <div className="data-source-table-name">
          <span className="data-source-provider-logo data-source-icon-database">
            <DatabaseOutlined />
          </span>
          <div className="data-source-table-copy">
            <Text strong>{value}</Text>
            <Text type="secondary" className="data-source-ellipsis">
              {record.description || record.database_name}
            </Text>
          </div>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceDatabaseType"),
      dataIndex: "db_type",
      width: 130,
      render: (value) => <Tag color={value === "mysql" ? "blue" : "geekblue"}>{value}</Tag>,
    },
    {
      title: t("admin.dataSourceDatabaseAddress"),
      width: 360,
      render: (_, record) => {
        const address = `${record.host}:${record.port}/${record.database_name}`;
        return (
          <Tooltip title={address} placement="topLeft">
            <Text className="data-source-ellipsis">{address}</Text>
          </Tooltip>
        );
      },
    },
    {
      title: t("admin.dataSourceDatabaseUsername"),
      dataIndex: "username",
      width: 180,
    },
    {
      title: t("admin.dataSourceDatabaseStatus"),
      dataIndex: "is_verified",
      width: 150,
      render: (verified, record) => verified ? (
        <Tag color="success" icon={<CheckCircleOutlined />}>{t("admin.dataSourceDatabaseVerified")}</Tag>
      ) : (
        <Tag color={record.last_check_error ? "error" : "default"}>{t("admin.dataSourceDatabaseUnverified")}</Tag>
      ),
    },
    {
      title: t("admin.dataSourceTableActions"),
      width: 260,
      fixed: "right",
      className: "data-source-action-column",
      render: (_, record) => (
        <Space size={14} className="data-source-table-actions">
          <Button
            type="link"
            icon={<SyncOutlined />}
            loading={checkingId === record.id}
            onClick={() => void handleCheck(record)}
          >
            {t("admin.dataSourceDatabaseTestAction")}
          </Button>
          <Button type="link" icon={<EditOutlined />} onClick={() => openEdit(record)}>{t("common.edit")}</Button>
          <Button type="link" danger icon={<DeleteOutlined />} onClick={() => handleDelete(record)}>{t("common.delete")}</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="admin-page data-source-page data-source-database-page">
      <div className="admin-page-toolbar data-source-page-toolbar">
        <div className="admin-page-toolbar-left data-source-page-toolbar-left">
          <div>
            <Button
              type="link"
              icon={<ArrowLeftOutlined />}
              className="data-source-provider-back-button"
              onClick={() => navigate("/model-providers/cloud-documents")}
            >
              {t("modelProvider.cloudDocuments.backToProviders")}
            </Button>
            <h2 className="admin-page-title">{t("admin.dataSourceDatabaseTitle")}</h2>
            <Paragraph className="data-source-page-subtitle">
              {t("admin.dataSourceDatabaseSubtitle")}
            </Paragraph>
          </div>
        </div>
        <Space>
          <Button icon={<FileTextOutlined />} onClick={() => setGuideOpen(true)}>{t("admin.dataSourceDatabaseGuideAction")}</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t("admin.dataSourceDatabaseCreateAction")}</Button>
        </Space>
      </div>

      <section className="data-source-feishu-account-shell data-source-database-shell">
        <Alert
          showIcon
          type="info"
          className="data-source-feishu-account-reauth-alert data-source-database-alert"
          message={t("admin.dataSourceDatabaseListHint")}
        />
        <div className="data-source-asset-table-wrap data-source-feishu-account-table-wrap">
          <Table<DatabaseConnectionItem>
            className="admin-page-table data-source-asset-table data-source-database-table"
            rowKey="id"
            columns={columns}
            dataSource={items}
            loading={loading}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            tableLayout="fixed"
            scroll={{ x: 1260, y: "calc(100vh - 360px)" }}
            locale={{
              emptyText: (
                <div className="data-source-asset-empty">
                  <DatabaseOutlined />
                  <Text strong>{t("admin.dataSourceDatabaseEmptyTitle")}</Text>
                  <Text type="secondary">{t("admin.dataSourceDatabaseEmptyDesc")}</Text>
                </div>
              ),
            }}
          />
        </div>
      </section>

      <DatabaseConnectionModal
        open={modalOpen}
        editing={editing}
        saving={saving}
        onCancel={() => setModalOpen(false)}
        onSubmit={handleSubmit}
      />

      <Modal
        title={t("admin.dataSourceDatabaseGuideTitle")}
        open={guideOpen}
        width={680}
        footer={<Button type="primary" onClick={() => setGuideOpen(false)}>{t("admin.dataSourceDatabaseGuideClose")}</Button>}
        onCancel={() => setGuideOpen(false)}
      >
        <Paragraph className="data-source-create-provider-intro">
          {t("admin.dataSourceDatabaseGuideIntro")}
        </Paragraph>
        <Steps
          direction="vertical"
          current={-1}
          items={[
            {
              title: t("admin.dataSourceDatabaseGuideReadOnlyTitle"),
              description: t("admin.dataSourceDatabaseGuideReadOnlyDesc"),
            },
            {
              title: t("admin.dataSourceDatabaseGuideNetworkTitle"),
              description: t("admin.dataSourceDatabaseGuideNetworkDesc"),
            },
            {
              title: t("admin.dataSourceDatabaseGuideConfigTitle"),
              description: t("admin.dataSourceDatabaseGuideConfigDesc"),
            },
            {
              title: t("admin.dataSourceDatabaseGuideTestTitle"),
              description: t("admin.dataSourceDatabaseGuideTestDesc"),
            },
            {
              title: t("admin.dataSourceDatabaseGuideUseTitle"),
              description: t("admin.dataSourceDatabaseGuideUseDesc"),
            },
          ]}
        />
      </Modal>
    </div>
  );
}
