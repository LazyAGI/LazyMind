import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Form,
  Input,
  Modal,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ApiOutlined,
  ArrowLeftOutlined,
  DeleteOutlined,
  EditOutlined,
  FileTextOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  WechatOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import type {
  CloudConnectionResponse,
  CloudConnectionUpdateBody,
} from "@/api/generated/auth-client";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { getCloudConnectionItems } from "@/modules/dataSource/mappers/cloudConnection";
import { formatDateTime } from "@/modules/dataSource/utils/format";
import {
  CLOUD_DOCUMENTS_PATH,
  CLOUD_DOCUMENTS_WECHAT_SETUP_PATH,
  WECHAT_OFFICIAL_ACCOUNT_PLATFORM_URL,
} from "../utils/cloudDocumentUrls";

const { Link, Paragraph, Text } = Typography;
const WECHAT_PROVIDER = "wechat";

interface AccountFormValues {
  name?: string;
  appId: string;
  appSecret: string;
}

function normalizeWeChatAccountForm(values: AccountFormValues) {
  const appId = values.appId.trim();
  return {
    appId,
    appSecret: values.appSecret.trim(),
    displayName: values.name?.trim() || appId,
  };
}

function getErrorDetail(error: unknown) {
  const payload = (error as any)?.response?.data;
  return String(
    payload?.ex_mesage || payload?.ex_message || payload?.detail || payload?.message || "",
  );
}

function getWechatErrorCode(detail: string) {
  return detail.match(/\[(-?\d+)\]/)?.[1] || "";
}

function getDetectedIp(detail: string) {
  return (
    detail.match(/\b(?:\d{1,3}\.){3}\d{1,3}\b/)?.[0] ||
    detail.match(/\b(?:[a-f\d]{1,4}:){2,}[a-f\d:]+\b/i)?.[0] ||
    ""
  );
}

function isActive(connection: CloudConnectionResponse) {
  return connection.status.trim().toUpperCase() === "ACTIVE";
}

export default function WeChatOfficialAccountPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [form] = Form.useForm<AccountFormValues>();
  const [connections, setConnections] = useState<CloudConnectionResponse[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingConnection, setEditingConnection] =
    useState<CloudConnectionResponse | null>(null);
  const [saving, setSaving] = useState(false);
  const [verifyingId, setVerifyingId] = useState("");

  const refreshConnections = useCallback(async () => {
    setLoading(true);
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: WECHAT_PROVIDER,
          status: null,
        });
      setConnections(getCloudConnectionItems(response.data));
    } catch {
      setConnections([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshConnections();
  }, [refreshConnections]);

  const detectedIp = useMemo(() => {
    for (const connection of connections) {
      if (getWechatErrorCode(connection.last_error || "") === "40164") {
        const ip = getDetectedIp(connection.last_error || "");
        if (ip) return ip;
      }
    }
    return "";
  }, [connections]);

  const openAccountModal = (connection?: CloudConnectionResponse) => {
    setEditingConnection(connection || null);
    form.setFieldsValue({
      name: connection?.display_name || "",
      appId: connection?.app_id || "",
      appSecret: "",
    });
    setModalOpen(true);
  };

  const updateConnection = async (
    connectionId: string,
    body: CloudConnectionUpdateBody,
  ) => {
    await dataSourceCloudOauthApi.updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut({
      connectionId,
      cloudConnectionUpdateBody: body,
    });
  };

  const saveAccount = async () => {
    if (saving) return;
    setSaving(true);
    try {
      const values = await form.validateFields();
      const { appId, appSecret, displayName } = normalizeWeChatAccountForm(values);

      if (editingConnection) {
        await updateConnection(editingConnection.connection_id, {
          display_name: displayName,
          client_id: appId,
          ...(appSecret ? { client_secret: appSecret } : {}),
        });
      } else {
        await dataSourceCloudOauthApi.createConnectionApiAuthserviceV1CloudProviderConnectionsPost({
            provider: WECHAT_PROVIDER,
            cloudConnectionCreateBody: {
              auth_mode: "service_account",
              client_id: appId,
              client_secret: appSecret,
              display_name: displayName,
            },
          });
      }

      setModalOpen(false);
      setEditingConnection(null);
      form.resetFields();
      message.success(t("modelProvider.wechatOfficialAccount.accountSaved"));
      await refreshConnections();
    } catch {
      // Request errors are localized by the shared API client.
    } finally {
      setSaving(false);
    }
  };

  const refreshStableToken = async (connection: CloudConnectionResponse) => {
    if (verifyingId) return;
    setVerifyingId(connection.connection_id);
    try {
      await dataSourceCloudOauthApi.refreshConnectionTokenApiAuthserviceV1CloudConnectionsConnectionIdTokenRefreshPost(
        { connectionId: connection.connection_id },
        { silentError: true } as never,
      );
      message.success(t("modelProvider.wechatOfficialAccount.verifySuccess"));
    } catch (error) {
      const detail = getErrorDetail(error);
      const code = getWechatErrorCode(detail);
      if (code === "40164") {
        const ip = getDetectedIp(detail);
        Modal.warning({
          title: t("modelProvider.wechatOfficialAccount.ipWhitelistTitle"),
          content: (
            <Space direction="vertical" size={10}>
              <Paragraph>
                {t("modelProvider.wechatOfficialAccount.ipWhitelistDescription")}
              </Paragraph>
              {ip ? (
                <Text code copyable={{ text: ip }}>
                  {ip}
                </Text>
              ) : null}
              <Link href={WECHAT_OFFICIAL_ACCOUNT_PLATFORM_URL} target="_blank" rel="noreferrer">
                {t("modelProvider.wechatOfficialAccount.openPlatform")}
              </Link>
            </Space>
          ),
        });
      } else if (code === "40013") {
        message.error(t("modelProvider.wechatOfficialAccount.invalidAppId"));
      } else if (code === "40125") {
        message.error(t("modelProvider.wechatOfficialAccount.invalidAppSecret"));
      } else {
        message.error(t("modelProvider.wechatOfficialAccount.verifyFailed"));
      }
    } finally {
      try {
        await refreshConnections();
      } finally {
        setVerifyingId("");
      }
    }
  };

  const deleteAccount = (connection: CloudConnectionResponse) => {
    Modal.confirm({
      title: t("modelProvider.wechatOfficialAccount.deleteTitle"),
      content: t("modelProvider.wechatOfficialAccount.deleteDescription", {
        name: connection.display_name || connection.app_id,
      }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        await dataSourceCloudOauthApi.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete({
          connectionId: connection.connection_id,
        });
        await refreshConnections();
      },
    });
  };

  const toggleChat = async (connection: CloudConnectionResponse, checked: boolean) => {
    try {
      await updateConnection(connection.connection_id, {
        chat_enabled: checked,
      });
      await refreshConnections();
    } catch {
      // Request errors are localized by the shared API client.
    }
  };

  const columns: ColumnsType<CloudConnectionResponse> = [
    {
      title: t("modelProvider.wechatOfficialAccount.accountColumn"),
      key: "account",
      width: 270,
      render: (_value, record) => (
        <div className="model-provider-cloud-doc-table-account">
          <span className="model-provider-service-logo model-provider-service-logo-green">
            <WechatOutlined />
          </span>
          <div className="model-provider-cloud-doc-table-account-copy">
            <Text strong>{record.display_name || record.app_id}</Text>
            <Text type="secondary" ellipsis={{ tooltip: record.app_id }}>
              {record.app_id}
            </Text>
          </div>
        </div>
      ),
    },
    {
      title: t("modelProvider.wechatOfficialAccount.statusColumn"),
      dataIndex: "status",
      key: "status",
      width: 150,
      render: (status: string, record) => {
        const normalized = status.trim().toUpperCase();
        if (normalized === "ACTIVE") {
          return <Tag color="success">{t("modelProvider.wechatOfficialAccount.active")}</Tag>;
        }
        if (normalized === "ERROR") {
          const whitelistError = getWechatErrorCode(record.last_error || "") === "40164";
          return (
            <Tooltip title={record.last_error || undefined}>
              <Tag color="error">
                {whitelistError
                  ? t("modelProvider.wechatOfficialAccount.ipWhitelistMissing")
                  : t("modelProvider.wechatOfficialAccount.error")}
              </Tag>
            </Tooltip>
          );
        }
        return <Tag color="processing">{t("modelProvider.wechatOfficialAccount.pending")}</Tag>;
      },
    },
    {
      title: t("modelProvider.wechatOfficialAccount.chatColumn"),
      key: "chat",
      width: 150,
      render: (_value, record) => {
        const active = isActive(record);
        const checked = active && Boolean(record.provider_options?.chat_enabled);
        return (
          <Tooltip
            title={
              active
                ? t("modelProvider.wechatOfficialAccount.chatHint")
                : t("modelProvider.wechatOfficialAccount.verifyBeforeEnable")
            }
          >
            <Switch
              checked={checked}
              disabled={!active}
              onChange={(next) => void toggleChat(record, next)}
            />
          </Tooltip>
        );
      },
    },
    {
      title: t("modelProvider.wechatOfficialAccount.createdAtColumn"),
      dataIndex: "created_at",
      key: "createdAt",
      width: 170,
      render: (createdAt: string) => <Text type="secondary">{formatDateTime(createdAt)}</Text>,
    },
    {
      title: t("admin.dataSourceTableActions"),
      key: "actions",
      width: 310,
      fixed: "right",
      render: (_value, record) => (
        <Space size={2}>
          <Button
            type="link"
            size="small"
            icon={<ReloadOutlined />}
            loading={verifyingId === record.connection_id}
            onClick={() => void refreshStableToken(record)}
          >
            {isActive(record)
              ? t("modelProvider.wechatOfficialAccount.refreshToken")
              : t("modelProvider.wechatOfficialAccount.verifyConnection")}
          </Button>
          <Button type="link" size="small" icon={<EditOutlined />} onClick={() => openAccountModal(record)}>
            {t("common.edit")}
          </Button>
          <Button type="link" size="small" danger icon={<DeleteOutlined />} onClick={() => deleteAccount(record)}>
            {t("common.delete")}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-feishu-page">
      <button
        type="button"
        className="model-provider-cloud-doc-breadcrumb"
        onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
      >
        <ArrowLeftOutlined />
        <span>{t("modelProvider.cloudDocuments.backToProviders")}</span>
      </button>

      <section className="model-provider-service-category model-provider-cloud-doc-feishu-section">
        <div className="model-provider-service-category-top">
          <div className="model-provider-service-category-head">
            <span
              className="model-provider-cloud-doc-feishu-logo"
              style={{ color: "#07c160", fontSize: 22 }}
            >
              <WechatOutlined />
            </span>
            <div>
              <h3>{t("modelProvider.wechatOfficialAccount.title")}</h3>
              <p>{t("modelProvider.wechatOfficialAccount.subtitle")}</p>
            </div>
          </div>
          <Space size={10} wrap>
            <Button
              icon={<FileTextOutlined />}
              onClick={() => navigate(CLOUD_DOCUMENTS_WECHAT_SETUP_PATH)}
            >
              {t("modelProvider.wechatOfficialAccount.setupGuideAction")}
            </Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => openAccountModal()}>
              {t("modelProvider.wechatOfficialAccount.createAccount")}
            </Button>
          </Space>
        </div>

        <div className="model-provider-cloud-doc-setup-card">
          <div className="model-provider-cloud-doc-setup-card-main">
            <span className="model-provider-cloud-doc-setup-card-icon" aria-hidden="true">
              <SafetyCertificateOutlined />
            </span>
            <div className="model-provider-cloud-doc-setup-card-copy">
              <h4>{t("modelProvider.wechatOfficialAccount.setupTitle")}</h4>
              <p>{t("modelProvider.wechatOfficialAccount.setupDescription")}</p>
            </div>
          </div>
          <Link
            className="model-provider-cloud-doc-open-platform"
            href={WECHAT_OFFICIAL_ACCOUNT_PLATFORM_URL}
            target="_blank"
            rel="noreferrer"
          >
            {t("modelProvider.wechatOfficialAccount.openPlatform")}
          </Link>
        </div>

        {detectedIp ? (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message={t("modelProvider.wechatOfficialAccount.detectedIpTitle")}
            description={
              <Space>
                <Text>{t("modelProvider.wechatOfficialAccount.detectedIpDescription")}</Text>
                <Text code copyable={{ text: detectedIp }}>{detectedIp}</Text>
              </Space>
            }
          />
        ) : null}

        <div className="model-provider-cloud-doc-table-panel">
          <Table<CloudConnectionResponse>
            className="model-provider-cloud-doc-table"
            rowKey="connection_id"
            columns={columns}
            dataSource={connections}
            loading={loading}
            pagination={{ pageSize: 8, showSizeChanger: false, size: "small" }}
            tableLayout="fixed"
            scroll={{ x: 1050 }}
            locale={{
              emptyText: (
                <div className="model-provider-cloud-doc-table-empty">
                  <ApiOutlined />
                  <Text strong>{t("modelProvider.wechatOfficialAccount.emptyTitle")}</Text>
                  <Text type="secondary">{t("modelProvider.wechatOfficialAccount.emptyDescription")}</Text>
                </div>
              ),
            }}
          />
        </div>
      </section>

      <Modal
        title={
          editingConnection
            ? t("modelProvider.wechatOfficialAccount.editAccount")
            : t("modelProvider.wechatOfficialAccount.createAccount")
        }
        open={modalOpen}
        destroyOnHidden
        onCancel={() => {
          if (!saving) {
            setModalOpen(false);
            setEditingConnection(null);
          }
        }}
        onOk={() => void saveAccount()}
        okText={t("common.save")}
        okButtonProps={{ loading: saving }}
        cancelText={t("common.cancel")}
      >
        <Form form={form} layout="vertical">
          <Form.Item label={t("modelProvider.wechatOfficialAccount.accountName")} name="name">
            <Input placeholder={t("modelProvider.wechatOfficialAccount.accountNamePlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.dataSourceAppId")}
            name="appId"
            rules={[{ required: true, message: t("admin.dataSourceAppIdRequired") }]}
          >
            <Input placeholder={t("modelProvider.wechatOfficialAccount.appIdPlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.dataSourceAppSecret")}
            name="appSecret"
            extra={
              editingConnection
                ? t("modelProvider.wechatOfficialAccount.secretUnchangedHint")
                : undefined
            }
            rules={editingConnection ? [] : [{ required: true, message: t("admin.dataSourceAppSecretRequired") }]}
          >
            <Input.Password placeholder={t("modelProvider.wechatOfficialAccount.appSecretPlaceholder")} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
