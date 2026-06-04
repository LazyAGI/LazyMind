import { useCallback, useEffect, useRef, useState } from "react";
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
  FileTextOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  CloudOauthApi,
  Configuration as AuthConfiguration,
  type CloudConnectionResponse,
} from "@/api/generated/auth-client";
import { AgentAppsAuth } from "@/components/auth";
import { BASE_URL, axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  consumeFeishuDataSourceOAuthResult,
  finishFeishuDataSourceOAuth,
  getFeishuDataSourceCallbackUrl,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  type FeishuConnectionStatus,
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
  FEISHU_DEFAULT_SCOPES,
  type OAuthState,
  type PendingOAuthAttempt,
  formatDateTime,
} from "./shared";
import { getScanTenantId } from "./scanV2Api";
import "./index.scss";

const { Link, Paragraph, Text } = Typography;
const FEISHU_LOGO_URL = "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96";
const FEISHU_OPEN_PLATFORM_URL = "https://open.feishu.cn/app";

function createCloudOauthApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CloudOauthApi(
    new AuthConfiguration({
      basePath: baseUrl,
      accessToken: () => AgentAppsAuth.getAccessToken(),
      baseOptions: {
        headers: AgentAppsAuth.getAuthHeaders(),
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

function getFeishuOpenPlatformAppUrl(appId: string) {
  return `${FEISHU_OPEN_PLATFORM_URL}/${encodeURIComponent(appId)}/baseinfo`;
}

function isFeishuAppId(value?: string | null) {
  return /^cli_[a-z0-9]+$/i.test(`${value || ""}`.trim());
}

function getFeishuConnectionAppId(connection: CloudConnectionResponse) {
  const providerMeta = connection.provider_account_meta || {};
  const connectionMeta = connection as CloudConnectionResponse & {
    appid?: unknown;
    appId?: unknown;
    app_id?: unknown;
  };
  return [
    providerMeta.client_id,
    providerMeta.app_id,
    providerMeta.appid,
    providerMeta.appId,
    connectionMeta.appid,
    connectionMeta.appId,
    connectionMeta.app_id,
    connection.provider_account_id,
  ].find((value) => isFeishuAppId(`${value || ""}`));
}

function normalizeFeishuAccountStatus(status?: string): FeishuConnectionStatus {
  const normalized = `${status || ""}`.trim().toLowerCase();
  if (["active", "connected", "success", "succeeded", "enabled"].includes(normalized)) {
    return "connected";
  }
  if (["expired", "inactive"].includes(normalized)) {
    return "expired";
  }
  if (["error", "failed", "failure", "invalid"].includes(normalized)) {
    return "error";
  }
  return "pending";
}

function splitScopes(value?: string | null) {
  return `${value || ""}`
    .split(/[,\s]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function getCloudConnectionItems(payload: unknown): CloudConnectionResponse[] {
  const responsePayload = payload as {
    items?: CloudConnectionResponse[];
    data?: { items?: CloudConnectionResponse[] };
  };

  if (Array.isArray(responsePayload.items)) {
    return responsePayload.items;
  }
  if (Array.isArray(responsePayload.data?.items)) {
    return responsePayload.data.items;
  }
  return [];
}

function mapCloudConnectionToFeishuAccount(
  connection: CloudConnectionResponse,
  cachedAccounts: FeishuAuthAccount[],
): FeishuAuthAccount {
  const providerMeta = connection.provider_account_meta || {};
  const cachedAccount =
    cachedAccounts.find((item) => item.connection?.connectionId === connection.connection_id) ||
    cachedAccounts.find(
      (item) =>
        item.appId &&
        (item.appId === providerMeta.client_id ||
          item.appId === providerMeta.app_id ||
          item.appId === connection.provider_account_id),
    );
  const appId = `${getFeishuConnectionAppId(connection) || cachedAccount?.appId || connection.connection_id}`;
  const displayName =
    connection.display_name ||
    providerMeta.name ||
    providerMeta.display_name ||
    providerMeta.tenant_name ||
    cachedAccount?.name ||
    appId;
  const status = normalizeFeishuAccountStatus(connection.status);

  return {
    id: connection.connection_id,
    name: displayName,
    appId,
    appSecret: cachedAccount?.appSecret || "",
    chatEnabled: cachedAccount?.chatEnabled ?? false,
    status,
    connection: {
      provider: "feishu",
      connectionId: connection.connection_id,
      status,
      accountName: displayName,
      grantedScopes: splitScopes(connection.scope),
      connectedAt: connection.last_used_at || connection.updated_at || connection.created_at,
      tenantKey: connection.provider_tenant_key,
      openId: connection.provider_account_id,
    },
    createdAt: connection.created_at,
    updatedAt: connection.updated_at || undefined,
    lastAuthorizedAt: connection.last_used_at || connection.updated_at || undefined,
  };
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
  const callbackUrl = getFeishuDataSourceCallbackUrl();
  const [accounts, setAccounts] = useState<FeishuAuthAccount[]>(() =>
    loadFeishuAuthAccounts(),
  );
  const [accountsLoading, setAccountsLoading] = useState(false);
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

  const refreshAccounts = useCallback(async () => {
    setAccountsLoading(true);
    try {
      const response =
        await createCloudOauthApiClient().listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "feishu",
          status: null,
        });
      const cachedAccounts = loadFeishuAuthAccounts();
      const nextAccounts = getCloudConnectionItems(response.data).map((item) =>
        mapCloudConnectionToFeishuAccount(item, cachedAccounts),
      );
      persistAccounts(nextAccounts);
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    } finally {
      setAccountsLoading(false);
    }
  }, [t]);

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
      window.setTimeout(() => {
        void refreshAccounts();
      }, 0);
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
    void refreshAccounts();

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
  }, [refreshAccounts]);

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
        tenantId: getScanTenantId(),
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
    if (!account.appId || !account.appSecret) {
      openAccountModal(account);
      message.warning(t("admin.dataSourceFeishuCredentialFirst"));
      return;
    }
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
      title: t("admin.dataSourceFeishuAccountColumnOpenPlatformUrl"),
      dataIndex: "appId",
      key: "openPlatformUrl",
      width: 330,
      render: (appId: string) => {
        if (!isFeishuAppId(appId)) {
          return (
            <Text type="secondary">{t("common.noData")}</Text>
          );
        }
        const openPlatformUrl = getFeishuOpenPlatformAppUrl(appId);
        return (
          <Link
            className="data-source-ellipsis"
            href={openPlatformUrl}
            target="_blank"
            rel="noreferrer"
            title={openPlatformUrl}
          >
            {openPlatformUrl}
          </Link>
        );
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
        <Space>
          <Button
            icon={<FileTextOutlined />}
            onClick={() => navigate("/data-sources/docs/feishu-setup")}
          >
            接入文档
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => openAccountModal()}
          >
            {t("admin.dataSourceFeishuAccountCreate")}
          </Button>
        </Space>
      </div>

      <section className="data-source-feishu-account-shell">
        <Alert
          showIcon
          type="warning"
          className="data-source-feishu-account-alert"
          message={
            <div className="data-source-feishu-account-alert-message">
              <div>{t("admin.dataSourceFeishuAccountSecurityHint")}</div>
              <div>
                {t("admin.dataSourceFeishuAccountCallbackPrefix")}
                <Link
                  href={FEISHU_OPEN_PLATFORM_URL}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t("admin.dataSourceFeishuAccountOpenPlatform")}
                </Link>
                {t("admin.dataSourceFeishuAccountCallbackMiddle")}
                <Text code copyable={{ text: callbackUrl }}>
                  {callbackUrl}
                </Text>
                {t("admin.dataSourceFeishuAccountCallbackSuffix")}
                <Text className="data-source-feishu-account-alert-highlight">
                  {t("admin.dataSourceFeishuAccountCallbackTarget")}
                </Text>
                {t("admin.dataSourceFeishuAccountCallbackSuffixEnd")}
              </div>
            </div>
          }
        />
        {accounts.length > 1 ? (
          <Alert
            showIcon
            type="info"
            className="data-source-feishu-account-reauth-alert"
            message={t("admin.dataSourceFeishuAccountReauthorizeHint")}
          />
        ) : null}
        <div className="data-source-asset-table-wrap data-source-feishu-account-table-wrap">
          <Table<FeishuAuthAccount>
            className="admin-page-table data-source-asset-table data-source-feishu-account-table"
            rowKey="id"
            columns={accountColumns}
            dataSource={accounts}
            loading={accountsLoading}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            tableLayout="fixed"
            scroll={{ x: 1410, y: "calc(100vh - 310px)" }}
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
