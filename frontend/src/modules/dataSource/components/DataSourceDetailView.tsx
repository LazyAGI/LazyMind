import {
  Alert,
  Button,
  Card,
  Col,
  Empty,
  Input,
  Row,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { ArrowLeftOutlined, SearchOutlined } from "@ant-design/icons";
import type { ReactNode } from "react";
import type { DataSourceSummary, DocumentStatusRow } from "../shared";
import { getStatusMeta } from "../shared";

const { Paragraph, Text, Title } = Typography;

interface LastOperation {
  syncedCount: number;
  ignoredCount: number;
  checkedCount: number;
  time: string;
}

interface DataSourceDetailViewProps {
  t: any;
  detailSource: DataSourceSummary | null;
  detailLoading: boolean;
  lastSync: string;
  documents: DocumentStatusRow[];
  lastOperation: LastOperation | null;
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
  lastSync,
  documents,
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
        <Space align="center" size={16} wrap>
          <Title level={2} className="data-source-detail-title">
            {detailSource.name}
          </Title>
          <Tag color={statusMeta.color} className="data-source-detail-title-tag">
            {statusMeta.text}
          </Tag>
        </Space>
        <Paragraph className="data-source-detail-description">
          {t("admin.dataSourceDetailLastSync", { time: lastSync })}
        </Paragraph>
      </div>

      <Row gutter={[16, 16]}>
        <Col xs={24} md={8}>
          <Card className="data-source-detail-stat-card">
            <Text className="data-source-detail-stat-label">
              {t("admin.dataSourceDetailSyncPath")}
            </Text>
            <Tooltip title={detailSource.target} placement="topLeft">
              <div className="data-source-detail-stat-value path">{detailSource.target}</div>
            </Tooltip>
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card className="data-source-detail-stat-card">
            <Text className="data-source-detail-stat-label">
              {t("admin.dataSourceDetailParsedDocs")}
            </Text>
            <div className="data-source-detail-stat-value">
              {documents.length}
              <span>{t("admin.dataSourceDetailFileUnit")}</span>
            </div>
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card className="data-source-detail-stat-card">
            <Text className="data-source-detail-stat-label">
              {t("admin.dataSourceDetailStorageUsed")}
            </Text>
            <div className="data-source-detail-stat-value">{detailSource.storageUsed}</div>
          </Card>
        </Col>
      </Row>

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

      <Card
        className="data-source-detail-doc-card"
        title={t("admin.dataSourceDetailDocChangeTitle")}
        extra={
          <Space wrap>
            <Button
              type="primary"
              loading={detailLoading}
              disabled={detailLoading}
              onClick={onOpenSyncPicker}
            >
              {t("admin.dataSourceDetailSyncNow")}
            </Button>
            <Input
              allowClear
              prefix={<SearchOutlined />}
              placeholder={t("admin.dataSourceDetailSearchDocPlaceholder")}
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              className="data-source-detail-search"
            />
          </Space>
        }
      >
        <Table<DocumentStatusRow>
          rowKey="id"
          columns={columns}
          dataSource={filteredDocuments}
          loading={detailLoading}
          pagination={{ pageSize: 8, showSizeChanger: false }}
          className="admin-page-table data-source-detail-table"
          locale={{ emptyText: t("admin.dataSourceDetailNoDocStatus") }}
          scroll={{ x: 1520, y: "max(280px, calc(100vh - 720px))" }}
        />
      </Card>

      {syncPickerModal}
    </div>
  );
}
