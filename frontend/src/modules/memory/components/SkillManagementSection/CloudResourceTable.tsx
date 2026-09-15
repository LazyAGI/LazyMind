import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Empty, Space, Table, Tag, message } from "antd";
import { CloudDownloadOutlined, ReloadOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";

import { getCloudSession, isCloudBusinessAvailable } from "@/runtime/cloud/session";
import { openCloudRegister } from "@/runtime/desktopBridge";
import {
  downloadCloudResource,
  listCloudResources,
  type CloudPresenceStatus,
  type CloudResourceItem,
  type CloudResourceType,
} from "../../cloudResourceApi";

interface CloudResourceTableProps {
  resourceType: CloudResourceType;
  t: (key: string, options?: Record<string, unknown>) => string;
  onDownloaded?: () => void | Promise<void>;
  refreshKey?: number;
}

const downloadableStatuses = new Set<CloudPresenceStatus>(["download_required", "local_missing"]);

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

export default function CloudResourceTable({ resourceType, t, onDownloaded, refreshKey = 0 }: CloudResourceTableProps) {
  const [signedIn, setSignedIn] = useState(false);
  const [registrationURL, setRegistrationURL] = useState("");
  const [sessionLoading, setSessionLoading] = useState(true);
  const [items, setItems] = useState<CloudResourceItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [downloading, setDownloading] = useState<Set<string>>(new Set());
  const requestSequence = useRef(0);

  const load = useCallback(async () => {
    const sequence = ++requestSequence.current;
    setSessionLoading(true);
    setLoadFailed(false);
    try {
      const session = await getCloudSession();
      if (sequence !== requestSequence.current) return;
	  const active = isCloudBusinessAvailable(session);
      setRegistrationURL(session.registration_url || "");
      setSignedIn(active);
      if (!active) {
        setItems([]);
        return;
      }
      setLoading(true);
      const resources = await listCloudResources(resourceType);
      if (sequence !== requestSequence.current) return;
      setItems(resources);
    } catch {
      if (sequence !== requestSequence.current) return;
      setItems([]);
      setLoadFailed(true);
    } finally {
      if (sequence === requestSequence.current) {
        setLoading(false);
        setSessionLoading(false);
      }
    }
  }, [resourceType]);

  useEffect(() => {
    void load();
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void load();
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      requestSequence.current++;
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [load, refreshKey]);

  const handleDownload = async (item: CloudResourceItem) => {
    if (downloading.has(item.resource_id)) return;
    setDownloading((current) => new Set(current).add(item.resource_id));
    try {
      await downloadCloudResource(resourceType, item.resource_id);
      message.success(t("admin.memoryCloudDownloadSuccess", { name: item.resource_name }));
      await onDownloaded?.();
      await load();
    } catch {
      message.error(t("admin.memoryCloudDownloadFailed"));
    } finally {
      setDownloading((current) => {
        const next = new Set(current);
        next.delete(item.resource_id);
        return next;
      });
    }
  };

  const statusMeta: Record<CloudPresenceStatus, { color: string; label: string }> = {
    present_current: { color: "success", label: t("admin.memoryCloudStatusCurrent") },
    download_required: { color: "processing", label: t("admin.memoryCloudStatusDownload") },
    local_missing: { color: "warning", label: t("admin.memoryCloudStatusLocalMissing") },
    cloud_updated: { color: "warning", label: t("admin.memoryCloudStatusCloudUpdated") },
    local_modified: { color: "orange", label: t("admin.memoryCloudStatusLocalModified") },
    diverged: { color: "error", label: t("admin.memoryCloudStatusDiverged") },
    incompatible: { color: "default", label: t("admin.memoryCloudStatusIncompatible") },
  };

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
        const canDownload = downloadableStatuses.has(item.presence_status);
        return (
          <Button
            type="link"
            icon={<CloudDownloadOutlined />}
            disabled={!canDownload}
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
        <Button onClick={() => void openCloudRegister(registrationURL)}>{t("admin.memoryCloudRegister")}</Button>
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
