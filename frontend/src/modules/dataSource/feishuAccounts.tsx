import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Form,
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
  ApiOutlined,
  ArrowLeftOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Configuration as ScanConfiguration,
  DefaultApi as ScanDefaultApi,
  type Agent as ScanAgent,
} from "@/api/generated/scan-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  consumeFeishuDataSourceOAuthResult,
  finishFeishuDataSourceOAuth,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  type FeishuDataSourceOAuthMessage,
} from "@/modules/dataSource/common/feishuOAuth";
import {
  createFeishuAccountId,
  getOAuthStateFromConnection,
  loadFeishuAuthAccounts,
  persistFeishuAppSetup,
  persistFeishuAuthAccounts,
  type FeishuAccountFormValues,
  type FeishuAuthAccount,
} from "./common/feishuAccounts";
import {
  DEFAULT_SCAN_TENANT_ID,
  FEISHU_DEFAULT_SCOPES,
  type OAuthState,
  type PendingOAuthAttempt,
  formatDateTime,
} from "./shared";
import "./index.scss";

const { Paragraph, Text } = Typography;
const FEISHU_LOGO_URL = "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96";

function createScanApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new ScanDefaultApi(
    new ScanConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

function listScanAgents(client: ScanDefaultApi) {
  return client.apiScanAgentsGet({
    params: {
      tenant_id: DEFAULT_SCAN_TENANT_ID,
    },
  });
}

function pickScanAgent(agents: ScanAgent[]) {
  const onlineAgent = agents.find((item) => {
    const status = (item.status || "").toLowerCase();
    return (
      status.includes("online") ||
      status.includes("active") ||
      status.includes("running")
    );
  });

  return onlineAgent || agents[0];
}

function parseFeishuOAuthCallbackInput(value: string) {
  const normalized = value.trim();
  if (!normalized) {
    return null;
  }

  try {
    const url = new URL(normalized);
    const code = url.searchParams.get("code");
    const state = url.searchParams.get("state");
    if (code && state) {
      return { code, state };
    }
  } catch {
  }

  const search = normalized.startsWith("?") ? normalized.slice(1) : normalized;
  const params = new URLSearchParams(search);
  const code = params.get("code");
  const state = params.get("state");
  if (code && state) {
    return { code, state };
  }

  const matchCode = normalized.match(/[?&]code=([^&]+)/);
  const matchState = normalized.match(/[?&]state=([^&]+)/);
  if (matchCode?.[1] && matchState?.[1]) {
    return {
      code: decodeURIComponent(matchCode[1]),
      state: decodeURIComponent(matchState[1]),
    };
  }

  return null;
}

export default function FeishuAccountPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [form] = Form.useForm<FeishuAccountFormValues>();
  const [accounts, setAccounts] = useState<FeishuAuthAccount[]>(() =>
    loadFeishuAuthAccounts(),
  );
  const [modalOpen, setModalOpen] = useState(false);
  const [editingAccountId, setEditingAccountId] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [manualOauthModalOpen, setManualOauthModalOpen] = useState(false);
  const [manualOauthCallbackValue, setManualOauthCallbackValue] = useState("");
  const [manualOauthSubmitting, setManualOauthSubmitting] = useState(false);
  const oauthAttemptRef = useRef<PendingOAuthAttempt | null>(null);

  const persistAccounts = (nextAccounts: FeishuAuthAccount[]) => {
    setAccounts(nextAccounts);
    persistFeishuAuthAccounts(nextAccounts);
  };

  const clearOauthAttempt = () => {
    if (oauthAttemptRef.current?.timerId) {
      window.clearInterval(oauthAttemptRef.current.timerId);
    }
    oauthAttemptRef.current = null;
  };

  const restorePreviousOauthState = (
    messageText?: string,
    level: "warning" | "error" = "warning",
  ) => {
    const attempt = oauthAttemptRef.current;
    if (!attempt) {
      if (messageText) {
        message[level](messageText);
      }
      return;
    }

    if (attempt.timerId) {
      window.clearInterval(attempt.timerId);
    }
    setAccounts((current) => {
      const nextAccounts = current.map((item) =>
        item.id === attempt.accountId
          ? {
              ...item,
              status: attempt.previousState,
              connection: attempt.previousConnection,
              updatedAt: new Date().toISOString(),
            }
          : item,
      );
      persistFeishuAuthAccounts(nextAccounts);
      return nextAccounts;
    });
    oauthAttemptRef.current = null;

    if (messageText) {
      message[level](messageText);
    }
  };

  const applyOauthResult = (payload: FeishuDataSourceOAuthMessage) => {
    const attempt = oauthAttemptRef.current;

    if (payload.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
      return;
    }

    if (attempt?.timerId) {
      window.clearInterval(attempt.timerId);
    }
    if (attempt) {
      attempt.resolved = true;
    }

    if (payload.status === "success") {
      oauthAttemptRef.current = null;
      const nextOauthState = getOAuthStateFromConnection(payload.connection);
      setAccounts((current) => {
        const matchedAccount =
          current.find(
            (item) =>
              (attempt?.accountId && item.id === attempt.accountId) ||
              item.appId === attempt?.appId,
          ) ||
          current.find((item) => item.status === "waiting") ||
          current[0];

        if (!matchedAccount) {
          return current;
        }

        const nextAccounts = current.map((item) =>
          item.id === matchedAccount.id
            ? {
                ...item,
                status: nextOauthState,
                connection: payload.connection,
                updatedAt: new Date().toISOString(),
                lastAuthorizedAt: new Date().toISOString(),
              }
            : item,
        );
        persistFeishuAuthAccounts(nextAccounts);
        return nextAccounts;
      });
      message.success(t("admin.dataSourceOauthSuccess"));
      return;
    }

    if (attempt?.previousConnection) {
      restorePreviousOauthState(
        t("admin.dataSourceOauthReconnectFailed", {
          message: payload.message ? ` ${payload.message}` : "",
        }),
        "error",
      );
      return;
    }

    oauthAttemptRef.current = null;
    setAccounts((current) => {
      const matchedAccount =
        current.find((item) => item.id === attempt?.accountId) ||
        current.find((item) => item.status === "waiting");
      if (!matchedAccount) {
        return current;
      }

      const nextAccounts = current.map((item) =>
        item.id === matchedAccount.id
          ? {
              ...item,
              status: "error" as OAuthState,
              connection: null,
              updatedAt: new Date().toISOString(),
            }
          : item,
      );
      persistFeishuAuthAccounts(nextAccounts);
      return nextAccounts;
    });
    message.error(payload.message || t("admin.dataSourceOauthFailedRetry"));
  };

  useEffect(() => {
    const storedResult = consumeFeishuDataSourceOAuthResult();
    if (storedResult) {
      window.setTimeout(() => {
        applyOauthResult(storedResult);
      }, 0);
    }

    const handleMessage = (event: MessageEvent<FeishuDataSourceOAuthMessage>) => {
      if (event.origin !== window.location.origin) {
        return;
      }
      if (!event.data || event.data.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
        return;
      }
      applyOauthResult(event.data);
    };

    window.addEventListener("message", handleMessage);

    return () => {
      window.removeEventListener("message", handleMessage);
      clearOauthAttempt();
    };
  }, []);

  const openAccountModal = (account?: FeishuAuthAccount) => {
    setEditingAccountId(account?.id || null);
    form.setFieldsValue({
      name: account?.name || "",
      appId: account?.appId || "",
      appSecret: account?.appSecret || "",
    });
    setModalOpen(true);
  };

  const startFeishuOAuth = async (account: FeishuAuthAccount) => {
    const previousState = account.status;
    const previousConnection = account.connection;

    try {
      const agentsResponse = await listScanAgents(createScanApiClient());
      const selectedAgent = pickScanAgent(agentsResponse.data.items || []);
      if (!selectedAgent?.agent_id || !selectedAgent.tenant_id) {
        message.error("未发现可用扫描 Agent，请先启动并注册扫描 Agent。");
        return false;
      }

      setAccounts((current) => {
        const nextAccounts = current.map((item) =>
          item.id === account.id
            ? {
                ...item,
                status: "waiting" as OAuthState,
                updatedAt: new Date().toISOString(),
              }
            : item,
        );
        persistFeishuAuthAccounts(nextAccounts);
        return nextAccounts;
      });

      const authorizeUrl = await requestFeishuDataSourceAuthorizeUrl({
        tenantId: selectedAgent.tenant_id,
        appId: account.appId,
        appSecret: account.appSecret,
        scopes: FEISHU_DEFAULT_SCOPES,
        returnUrl: window.location.href,
      });

      const popup = openCenteredPopup(
        authorizeUrl,
        t("admin.dataSourceFeishuAuthWindowTitle"),
      );

      oauthAttemptRef.current = {
        timerId: null,
        previousState,
        previousVerified: previousState === "connected",
        previousConnection,
        resolved: false,
        accountId: account.id,
        appId: account.appId,
      };

      if (popup) {
        const timerId = window.setInterval(() => {
          if (!popup.closed) {
            return;
          }

          if (oauthAttemptRef.current?.resolved) {
            clearOauthAttempt();
            return;
          }

          restorePreviousOauthState(t("admin.dataSourceOauthWindowClosed"));
        }, 400);

        oauthAttemptRef.current.timerId = timerId;
        popup.focus();
        return true;
      }

      window.location.assign(authorizeUrl);
      return true;
    } catch (error: any) {
      restorePreviousOauthState(
        error?.message || t("admin.dataSourceAuthorizeUrlFailed"),
        "error",
      );
      return false;
    }
  };

  const upsertAccount = (values: FeishuAccountFormValues) => {
    const now = new Date().toISOString();
    const appId = values.appId.trim();
    const appSecret = values.appSecret.trim();
    const existingAccount = editingAccountId
      ? accounts.find((item) => item.id === editingAccountId)
      : accounts.find((item) => item.appId === appId);
    const nextAccount: FeishuAuthAccount = {
      id: existingAccount?.id || createFeishuAccountId(),
      name: `${values.name || ""}`.trim() || existingAccount?.name || appId,
      appId,
      appSecret,
      chatEnabled: existingAccount?.chatEnabled ?? false,
      status: "pending",
      connection: null,
      createdAt: existingAccount?.createdAt || now,
      updatedAt: now,
      lastAuthorizedAt: existingAccount?.lastAuthorizedAt,
    };
    const nextAccounts = existingAccount
      ? accounts.map((item) => (item.id === existingAccount.id ? nextAccount : item))
      : [nextAccount, ...accounts];

    persistAccounts(nextAccounts);
    persistFeishuAppSetup({ appId, appSecret });
    return nextAccount;
  };

  const handleSaveAccount = async () => {
    if (submitting) {
      return;
    }

    setSubmitting(true);
    try {
      const values = await form.validateFields();
      const account = upsertAccount(values);
      setModalOpen(false);
      setEditingAccountId(null);
      message.success(t("admin.dataSourceFeishuCredentialSaved"));
      await startFeishuOAuth(account);
    } finally {
      setSubmitting(false);
    }
  };

  const handleAuthorizeAccount = (account: FeishuAuthAccount) => {
    persistFeishuAppSetup({
      appId: account.appId,
      appSecret: account.appSecret,
    });
    void startFeishuOAuth(account);
  };

  const handleDeleteAccount = (account: FeishuAuthAccount) => {
    Modal.confirm({
      title: t("admin.dataSourceFeishuAccountDeleteTitle"),
      content: t("admin.dataSourceFeishuAccountDeleteContent", {
        name: account.name,
      }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => {
        persistAccounts(accounts.filter((item) => item.id !== account.id));
      },
    });
  };

  const handleToggleChat = (account: FeishuAuthAccount, checked: boolean) => {
    setAccounts((current) => {
      const nextAccounts = current.map((item) =>
        item.id === account.id
          ? { ...item, chatEnabled: checked, updatedAt: new Date().toISOString() }
          : item,
      );
      persistFeishuAuthAccounts(nextAccounts);
      return nextAccounts;
    });
  };

  const handleSubmitManualOauthCallback = async () => {
    const parsed = parseFeishuOAuthCallbackInput(manualOauthCallbackValue);
    if (!parsed) {
      message.warning(t("admin.dataSourceOauthManualCallbackInvalid"));
      return;
    }

    try {
      setManualOauthSubmitting(true);
      const connection = await finishFeishuDataSourceOAuth(parsed.code, parsed.state);
      applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "success",
        connection,
      });
      setManualOauthModalOpen(false);
      setManualOauthCallbackValue("");
    } catch (error: any) {
      applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "error",
        message: error?.message || t("admin.dataSourceOauthFailedRetry"),
      });
    } finally {
      setManualOauthSubmitting(false);
    }
  };

  const accountColumns: ColumnsType<FeishuAuthAccount> = [
    {
      title: t("admin.dataSourceFeishuAccountColumnAccount"),
      dataIndex: "name",
      key: "name",
      width: 360,
      render: (_value, record) => (
        <div className="data-source-table-name">
          <span className="data-source-provider-logo data-source-icon-feishu">
            <img
              alt=""
              aria-hidden="true"
              loading="lazy"
              src={FEISHU_LOGO_URL}
              onError={(event) => {
                event.currentTarget.style.display = "none";
              }}
            />
          </span>
          <div className="data-source-table-copy">
            <Text strong>{record.name}</Text>
            <Text type="secondary" className="data-source-ellipsis">
              {record.appId}
            </Text>
          </div>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceFeishuAccountColumnStatus"),
      dataIndex: "status",
      key: "status",
      width: 150,
      render: (status: OAuthState) => {
        if (status === "connected") {
          return <Tag color="success">{t("admin.dataSourceProviderAuthValid")}</Tag>;
        }
        if (status === "waiting") {
          return <Tag color="processing">{t("admin.dataSourceProviderAuthPending")}</Tag>;
        }
        if (status === "error") {
          return <Tag color="error">{t("admin.dataSourceConnectionError")}</Tag>;
        }
        if (status === "expired") {
          return <Tag color="warning">{t("admin.dataSourceConnectionExpired")}</Tag>;
        }
        return <Tag>{t("admin.dataSourceProviderCredentialReady")}</Tag>;
      },
    },
    {
      title: t("admin.dataSourceFeishuAccountColumnChat"),
      dataIndex: "chatEnabled",
      key: "chatEnabled",
      width: 150,
      render: (_value, record) => {
        const enabled = Boolean(record.chatEnabled);
        return (
          <Tooltip title={t("admin.dataSourceFeishuAccountChatSwitchHint")}>
            <button
              type="button"
              role="switch"
              aria-checked={enabled}
              aria-label={t("admin.dataSourceFeishuAccountChatSwitchAria", {
                name: record.name,
              })}
              className={`data-source-chat-switch${enabled ? " is-on" : ""}`}
              onClick={() => handleToggleChat(record, !enabled)}
            >
              <span className="data-source-chat-switch-thumb" aria-hidden="true" />
              <span className="data-source-chat-switch-label">
                {enabled
                  ? t("admin.dataSourceFeishuAccountChatOn")
                  : t("admin.dataSourceFeishuAccountChatOff")}
              </span>
            </button>
          </Tooltip>
        );
      },
    },
    {
      title: t("admin.dataSourceFeishuAccountColumnCreatedAt"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 190,
      render: (createdAt: string) => formatDateTime(createdAt),
    },
    {
      title: t("admin.dataSourceTableActions"),
      key: "actions",
      width: 230,
      fixed: "right",
      className: "data-source-action-column",
      render: (_value, record) => (
        <Space size={14} className="data-source-table-actions">
          <Button
            type="link"
            icon={<SafetyCertificateOutlined />}
            onClick={() => handleAuthorizeAccount(record)}
          >
            {record.status === "connected"
              ? t("admin.dataSourceFeishuReconnectAction")
              : t("admin.dataSourceFeishuAuthorizeAction")}
          </Button>
          <Button
            type="link"
            icon={<EditOutlined />}
            onClick={() => openAccountModal(record)}
          >
            {t("common.edit")}
          </Button>
          <Button
            type="link"
            danger
            icon={<DeleteOutlined />}
            onClick={() => handleDeleteAccount(record)}
          >
            {t("common.delete")}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="admin-page data-source-page data-source-feishu-account-page">
      <div className="admin-page-toolbar data-source-page-toolbar">
        <div className="admin-page-toolbar-left data-source-page-toolbar-left">
          <div>
            <Button
              type="link"
              icon={<ArrowLeftOutlined />}
              className="data-source-provider-back-button"
              onClick={() => navigate("/data-sources?view=connectors")}
            >
              {t("admin.dataSourceProviderBack")}
            </Button>
            <h2 className="admin-page-title">
              {t("admin.dataSourceFeishuAccountManagementTitle")}
            </h2>
            <Paragraph className="data-source-page-subtitle">
              {t("admin.dataSourceFeishuAccountManagementSubtitle")}
            </Paragraph>
          </div>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => openAccountModal()}
        >
          {t("admin.dataSourceFeishuAccountCreate")}
        </Button>
      </div>

      <section className="data-source-feishu-account-shell">
        <Alert
          showIcon
          type="warning"
          className="data-source-feishu-account-alert"
          message={t("admin.dataSourceFeishuAccountSecurityHint")}
        />
        <div className="data-source-asset-table-wrap data-source-feishu-account-table-wrap">
          <Table<FeishuAuthAccount>
            className="admin-page-table data-source-asset-table data-source-feishu-account-table"
            rowKey="id"
            columns={accountColumns}
            dataSource={accounts}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            tableLayout="fixed"
            scroll={{ x: 1080, y: "calc(100vh - 310px)" }}
            locale={{
              emptyText: (
                <div className="data-source-asset-empty">
                  <ApiOutlined />
                  <Text strong>{t("admin.dataSourceFeishuAccountEmptyTitle")}</Text>
                  <Text type="secondary">
                    {t("admin.dataSourceFeishuAccountEmptyDesc")}
                  </Text>
                </div>
              ),
            }}
          />
        </div>
      </section>

      <Modal
        title={
          editingAccountId
            ? t("admin.dataSourceFeishuAccountEdit")
            : t("admin.dataSourceFeishuAccountCreate")
        }
        open={modalOpen}
        destroyOnHidden
        onCancel={() => {
          if (submitting) {
            return;
          }
          setModalOpen(false);
          setEditingAccountId(null);
        }}
        onOk={handleSaveAccount}
        okText={t("admin.dataSourceFeishuAccountSaveAndAuthorize")}
        okButtonProps={{ loading: submitting }}
        cancelButtonProps={{ disabled: submitting }}
        cancelText={t("common.cancel")}
      >
        <Form form={form} layout="vertical">
          <Form.Item label={t("admin.dataSourceFeishuAccountName")} name="name">
            <Input placeholder={t("admin.dataSourceFeishuAccountNamePlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.dataSourceAppId")}
            name="appId"
            rules={[{ required: true, message: t("admin.dataSourceAppIdRequired") }]}
          >
            <Input placeholder={t("admin.dataSourceAppIdPlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.dataSourceAppSecret")}
            name="appSecret"
            rules={[{ required: true, message: t("admin.dataSourceAppSecretRequired") }]}
          >
            <Input.Password placeholder={t("admin.dataSourceAppSecretPlaceholder")} />
          </Form.Item>
          <Alert
            showIcon
            type="info"
            message={t("admin.dataSourceFeishuCredentialHint")}
          />
        </Form>
      </Modal>

      <Modal
        title={t("admin.dataSourceOauthManualCallbackTitle")}
        open={manualOauthModalOpen}
        onCancel={() => {
          if (!manualOauthSubmitting) {
            setManualOauthModalOpen(false);
          }
        }}
        onOk={handleSubmitManualOauthCallback}
        okText={t("admin.dataSourceOauthManualCallbackConfirm")}
        okButtonProps={{ loading: manualOauthSubmitting }}
        cancelText={t("common.cancel")}
        destroyOnHidden
      >
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Alert
            showIcon
            type="info"
            message={t("admin.dataSourceOauthManualCallbackDesc")}
          />
          <Input.TextArea
            value={manualOauthCallbackValue}
            onChange={(event) => setManualOauthCallbackValue(event.target.value)}
            placeholder={t("admin.dataSourceOauthManualCallbackPlaceholder")}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </Space>
      </Modal>
    </div>
  );
}
