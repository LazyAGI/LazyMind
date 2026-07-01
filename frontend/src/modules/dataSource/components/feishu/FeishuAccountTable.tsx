import { Button, Space, Table, Tag, Tooltip, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ApiOutlined,
  DeleteOutlined,
  EditOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import type { FeishuAuthAccount } from "../../common/feishuAccounts";
import { getFeishuOpenPlatformAppUrl } from "../../common/FeishuCredentialHintAlert";
import type { OAuthState } from "../../constants/types";
import { formatDateTime } from "../../utils/format";
import {
  isFeishuAccountAuthValid,
  isFeishuAppId,
} from "../../utils/feishuAccount";

const { Link, Text } = Typography;
const FEISHU_LOGO_URL = "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96";

export interface FeishuAccountTableProps {
  t: TFunction;
  accounts: FeishuAuthAccount[];
  accountsLoading: boolean;
  onAuthorize: (account: FeishuAuthAccount) => void;
  onEdit: (account: FeishuAuthAccount) => void;
  onDelete: (account: FeishuAuthAccount) => void;
  onToggleChat: (account: FeishuAuthAccount, checked: boolean) => void;
}

export default function FeishuAccountTable({
  t,
  accounts,
  accountsLoading,
  onAuthorize,
  onEdit,
  onDelete,
  onToggleChat,
}: FeishuAccountTableProps) {
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
          return <Text type="secondary">{t("common.noData")}</Text>;
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
        const canToggleChat = isFeishuAccountAuthValid(record);
        const enabled = canToggleChat && Boolean(record.chatEnabled);
        return (
          <Tooltip
            title={
              canToggleChat
                ? t("admin.dataSourceFeishuAccountChatSwitchHint")
                : t("admin.dataSourceFeishuAccountChatAuthRequired")
            }
          >
            <button
              type="button"
              role="switch"
              aria-checked={enabled}
              aria-disabled={!canToggleChat}
              aria-label={t("admin.dataSourceFeishuAccountChatSwitchAria", {
                name: record.name,
              })}
              disabled={!canToggleChat}
              className={`data-source-chat-switch${enabled ? " is-on" : ""}${
                canToggleChat ? "" : " is-disabled"
              }`}
              onClick={() => onToggleChat(record, !enabled)}
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
            onClick={() => onAuthorize(record)}
          >
            {record.status === "connected"
              ? t("admin.dataSourceFeishuReconnectAction")
              : t("admin.dataSourceFeishuAuthorizeAction")}
          </Button>
          <Button type="link" icon={<EditOutlined />} onClick={() => onEdit(record)}>
            {t("common.edit")}
          </Button>
          <Button
            type="link"
            danger
            icon={<DeleteOutlined />}
            onClick={() => onDelete(record)}
          >
            {t("common.delete")}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="data-source-asset-table-wrap data-source-feishu-account-table-wrap">
      <Table<FeishuAuthAccount>
        className="admin-page-table data-source-asset-table data-source-feishu-account-table"
        rowKey="id"
        columns={accountColumns}
        dataSource={accounts}
        loading={accountsLoading}
        pagination={{ pageSize: 8, showSizeChanger: false }}
        tableLayout="fixed"
        scroll={{ x: 1410, y: "calc(100vh - 380px)" }}
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
  );
}
