import { Button, Table, Tag, Tooltip, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ApiOutlined,
  DeleteOutlined,
  EditOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import type { FeishuAuthAccount } from "@/modules/dataSource/common/feishuAccounts";
import { getFeishuOpenPlatformAppUrl } from "@/modules/dataSource/common/FeishuCredentialHintAlert";
import type { OAuthState } from "@/modules/dataSource/constants/types";
import { formatDateTime } from "@/modules/dataSource/utils/format";
import {
  isFeishuAccountAuthValid,
  isFeishuAppId,
} from "@/modules/dataSource/utils/feishuAccount";

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
      width: 280,
      render: (_value, record) => (
        <div className="model-provider-cloud-doc-table-account">
          <span className="model-provider-service-logo model-provider-service-logo-blue">
            <img
              alt=""
              aria-hidden="true"
              loading="lazy"
              src={FEISHU_LOGO_URL}
              className="is-loaded"
              onError={(event) => {
                event.currentTarget.style.display = "none";
              }}
            />
          </span>
          <div className="model-provider-cloud-doc-table-account-copy">
            <Text strong>{record.name}</Text>
            <Text type="secondary" ellipsis={{ tooltip: record.appId }}>
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
      width: 120,
      render: (status: OAuthState) => {
        if (status === "connected") {
          return (
            <Tag className="model-provider-service-status" color="success">
              {t("modelProvider.cloudDocuments.authValid")}
            </Tag>
          );
        }
        if (status === "waiting") {
          return (
            <Tag className="model-provider-service-status" color="processing">
              {t("modelProvider.cloudDocuments.authPending")}
            </Tag>
          );
        }
        if (status === "error") {
          return <Tag color="error">{t("admin.dataSourceConnectionError")}</Tag>;
        }
        if (status === "expired") {
          return <Tag color="warning">{t("admin.dataSourceConnectionExpired")}</Tag>;
        }
        return (
          <Tag className="model-provider-service-status">
            {t("modelProvider.cloudDocuments.credentialMissing")}
          </Tag>
        );
      },
    },
    {
      title: t("admin.dataSourceFeishuAccountColumnOpenPlatformUrl"),
      dataIndex: "appId",
      key: "openPlatformUrl",
      ellipsis: true,
      render: (appId: string) => {
        if (!isFeishuAppId(appId)) {
          return <Text type="secondary">{t("common.noData")}</Text>;
        }
        const openPlatformUrl = getFeishuOpenPlatformAppUrl(appId);
        return (
          <Link href={openPlatformUrl} target="_blank" rel="noreferrer" ellipsis>
            {openPlatformUrl}
          </Link>
        );
      },
    },
    {
      title: t("admin.dataSourceFeishuAccountColumnChat"),
      dataIndex: "chatEnabled",
      key: "chatEnabled",
      width: 132,
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
              className={`model-provider-cloud-doc-switch${enabled ? " is-on" : ""}${
                canToggleChat ? "" : " is-disabled"
              }`}
              onClick={() => onToggleChat(record, !enabled)}
            >
              <span className="model-provider-cloud-doc-switch-thumb" aria-hidden="true" />
              <span className="model-provider-cloud-doc-switch-label">
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
      width: 168,
      render: (createdAt: string) => (
        <Text type="secondary">{formatDateTime(createdAt)}</Text>
      ),
    },
    {
      title: t("admin.dataSourceTableActions"),
      key: "actions",
      width: 228,
      align: "center",
      fixed: "right",
      className: "model-provider-cloud-doc-table-action-column",
      onHeaderCell: () => ({
        className: "model-provider-cloud-doc-table-action-column",
      }),
      onCell: () => ({
        className: "model-provider-cloud-doc-table-action-column",
      }),
      render: (_value, record) => (
        <div className="model-provider-cloud-doc-table-actions">
          <Button
            type="link"
            size="small"
            icon={<SafetyCertificateOutlined />}
            onClick={() => onAuthorize(record)}
          >
            {record.status === "connected"
              ? t("admin.dataSourceFeishuReconnectAction")
              : t("admin.dataSourceFeishuAuthorizeAction")}
          </Button>
          <Button type="link" size="small" icon={<EditOutlined />} onClick={() => onEdit(record)}>
            {t("common.edit")}
          </Button>
          <Button
            type="link"
            size="small"
            danger
            icon={<DeleteOutlined />}
            onClick={() => onDelete(record)}
          >
            {t("common.delete")}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="model-provider-cloud-doc-table-panel">
      <Table<FeishuAuthAccount>
        className="model-provider-cloud-doc-table"
        rowKey="id"
        columns={accountColumns}
        dataSource={accounts}
        loading={accountsLoading}
        pagination={{ pageSize: 8, showSizeChanger: false, size: "small" }}
        tableLayout="fixed"
        scroll={{ x: 1100 }}
        locale={{
          emptyText: (
            <div className="model-provider-cloud-doc-table-empty">
              <ApiOutlined />
              <Text strong>{t("admin.dataSourceFeishuAccountEmptyTitle")}</Text>
              <Text type="secondary">{t("admin.dataSourceFeishuAccountEmptyDesc")}</Text>
            </div>
          ),
        }}
      />
    </div>
  );
}
