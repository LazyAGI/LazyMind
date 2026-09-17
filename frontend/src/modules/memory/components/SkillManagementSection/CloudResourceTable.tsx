import { Alert, Button, Empty, Space, Table, Tag } from "antd";
import { CloudDownloadOutlined, ReloadOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import type { CloudPresenceStatus, CloudResourceItem } from "../../cloudResourceApi";
import { useCloudResourceTable, type CloudResourceTableProps } from "../../hooks/useCloudResourceTable";

export default function CloudResourceTable(props: CloudResourceTableProps) {
  const { t } = props;
  const { signedIn, sessionLoading, items, loading, loadFailed, downloading, load, handleDownload, statusMeta, formatBytes, canDownload, openRegistration } = useCloudResourceTable(props);

  const columns: ColumnsType<CloudResourceItem> = [
    { title: t("admin.memoryCloudResourceName"), dataIndex: "resource_name", ellipsis: true },
    { title: t("admin.memoryCloudResourceSize"), dataIndex: "content_size", width: 120, render: formatBytes },
    {
      title: t("admin.memoryCloudResourceUpdatedAt"), dataIndex: "updated_at", width: 190,
      render: (value: string) => new Date(value).toLocaleString(),
    },
    {
      title: t("admin.memoryCloudResourceStatus"), dataIndex: "presence_status", width: 150,
      render: (status: CloudPresenceStatus) => <Tag color={statusMeta[status].color}>{statusMeta[status].label}</Tag>,
    },
    {
      title: t("common.actions"), key: "actions", width: 150,
      render: (_value, item) => {
        return (
          <Button
            type="link"
            icon={<CloudDownloadOutlined />}
            disabled={!canDownload(item)}
            loading={downloading.has(item.resource_id)}
            onClick={() => void handleDownload(item)}
          >
            {item.presence_status === "local_missing" ? t("admin.memoryCloudRedownload") : t("admin.memoryCloudDownload")}
          </Button>
        );
      },
    },
  ];

  if (!sessionLoading && !signedIn && !loadFailed) {
    return (
      <Empty description={t("admin.memoryCloudLoginRequired")}>
        <Button onClick={() => void openRegistration()}>{t("admin.memoryCloudRegister")}</Button>
      </Empty>
    );
  }

  return (
    <div className="memory-cloud-resource-view">
      {loadFailed ? (
        <Alert
          showIcon
          type="error"
          message={t("admin.memoryCloudLoadFailed")}
          action={<Button icon={<ReloadOutlined />} onClick={() => void load()}>{t("common.retry")}</Button>}
        />
      ) : null}
      <Space className="memory-cloud-resource-actions">
        <Button icon={<ReloadOutlined />} onClick={() => void load()}>{t("common.refresh")}</Button>
      </Space>
      <Table<CloudResourceItem>
        rowKey="resource_id"
        loading={sessionLoading || loading}
        dataSource={items}
        columns={columns}
        pagination={{ pageSize: 20, showSizeChanger: false }}
      />
    </div>
  );
}
