import {
  Alert,
  Button,
  Card,
  Empty,
  Input,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { ArrowLeftOutlined, SearchOutlined } from "@ant-design/icons";
import type { ReactNode } from "react";
import type { DataSourceSummary, DocumentStatusRow } from "../../constants/types";
import { getStatusMeta } from "../../utils/status";

const { Text, Title } = Typography;

export interface DataSourceDetailLastOperation {
  syncedCount: number;
  ignoredCount: number;
  checkedCount: number;
  time: string;
}

export interface DataSourceDetailViewProps {
  t: any;
  detailSource: DataSourceSummary | null;
  detailLoading: boolean;
  documentLoading?: boolean;
  lastSync: string;
  lastOperation: DataSourceDetailLastOperation | null;
  keyword: string;
  setKeyword: (value: string) => void;
  filteredDocuments: DocumentStatusRow[];
  columns: ColumnsType<DocumentStatusRow>;
  onBack: () => void;
  onOpenSyncPicker: () => void;
  syncPickerModal: ReactNode;
}

export default function DataSourceDetailView({
  t,
  detailSource,
  detailLoading,
  documentLoading,
  lastSync,
  lastOperation,
  keyword,
  setKeyword,
  filteredDocuments,
  columns,
  onBack,
  onOpenSyncPicker,
  syncPickerModal,
}: DataSourceDetailViewProps) {
  if (!detailSource && detailLoading) {
    return (
      <div className="admin-page data-source-detail-page">
        <Button
          type="link"
          icon={<ArrowLeftOutlined />}
          className="data-source-detail-back"
          onClick={onBack}
        >
          {t("admin.dataSourceBackToList")}
        </Button>
        <Card loading />
      </div>
    );
  }

  if (!detailSource) {
    return (
      <div className="admin-page data-source-detail-page">
        <Button
          type="link"
          icon={<ArrowLeftOutlined />}
          className="data-source-detail-back"
          onClick={onBack}
        >
          {t("admin.dataSourceBackToList")}
        </Button>
        <Card>
          <Empty description={t("admin.dataSourceDetailNotFound")} />
        </Card>
      </div>
    );
  }

  const statusMeta = getStatusMeta(detailSource.status, t);
  const compactLastSync = `${lastSync}`.replace(/^最近同步时间：|^Last sync:\s*/i, "");

  return (
    <div className="admin-page data-source-detail-page">
      <Button
        type="link"
        icon={<ArrowLeftOutlined />}
        className="data-source-detail-back"
        onClick={onBack}
      >
        {t("admin.dataSourceBackToList")}
      </Button>

      <div className="data-source-detail-header">
        <div className="data-source-detail-title-row">
          <Space align="center" size={12} wrap>
            <Title level={2} className="data-source-detail-title">
              {detailSource.name}
            </Title>
            <Tag color={statusMeta.color} className="data-source-detail-title-tag">
              {statusMeta.text}
            </Tag>
          </Space>
          <Button
            type="primary"
            size="large"
            loading={detailLoading}
            disabled={detailLoading}
            onClick={onOpenSyncPicker}
          >
            {t("admin.dataSourceDetailSyncNow")}
          </Button>
        </div>
      </div>

      <div className="data-source-detail-meta-bar">
        <div className="data-source-detail-meta-item data-source-detail-meta-path">
          <Text className="data-source-detail-stat-label">
            {t("admin.dataSourceDetailSyncPath")}
          </Text>
          <Tooltip title={detailSource.target} placement="topLeft">
            <div className="data-source-detail-stat-value path">{detailSource.target}</div>
          </Tooltip>
        </div>
        <div className="data-source-detail-meta-item">
          <Text className="data-source-detail-stat-label">
            {t("admin.dataSourceDetailParsedDocs")}
          </Text>
          <div className="data-source-detail-stat-value">
            {detailSource.parsedDocumentCount ?? 0}
          </div>
        </div>
        <div className="data-source-detail-meta-item">
          <Text className="data-source-detail-stat-label">
            {t("admin.dataSourceDetailStorageUsed")}
          </Text>
          <div className="data-source-detail-stat-value">{detailSource.storageUsed}</div>
        </div>
        <div className="data-source-detail-meta-item">
          <Text className="data-source-detail-stat-label">
            {t("admin.dataSourceDetailLastSync", { time: "" }).replace(/[：:]\s*$/, "")}
          </Text>
          <div className="data-source-detail-stat-value">{compactLastSync}</div>
        </div>
      </div>

      {lastOperation ? (
        <Alert
          showIcon
          type={lastOperation.syncedCount > 0 ? "success" : "warning"}
          message={t("admin.dataSourceDetailLastManualPull")}
          description={t("admin.dataSourceDetailLastManualPullDesc", {
            time: lastOperation.time,
            checked: lastOperation.checkedCount,
            synced: lastOperation.syncedCount,
            ignored: lastOperation.ignoredCount,
          })}
        />
      ) : null}

      <section className="data-source-detail-doc-card">
        <div className="data-source-detail-doc-toolbar">
          <Title level={4} className="data-source-detail-section-title">
            {t("admin.dataSourceDetailDocChangeTitle")}
          </Title>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder={t("admin.dataSourceDetailSearchDocPlaceholder")}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            className="data-source-detail-search"
          />
        </div>
        <Table<DocumentStatusRow>
          rowKey="id"
          columns={columns}
          dataSource={filteredDocuments}
          loading={documentLoading ?? detailLoading}
          pagination={{ pageSize: 8, showSizeChanger: false }}
          className="admin-page-table data-source-detail-table"
          locale={{ emptyText: t("admin.dataSourceDetailNoDocStatus") }}
          scroll={{ x: 1120, y: "52vh" }}
        />
      </section>

      {syncPickerModal}
    </div>
  );
}
