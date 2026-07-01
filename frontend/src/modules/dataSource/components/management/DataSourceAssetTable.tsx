import { Button, Input, Space, Table, Tag, Tooltip, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import type { DataSourceItem, SourceType } from "../../constants/types";
import {
  getConnectionMeta,
  getSourceTypeTitle,
  getStatusMeta,
  getSyncModeLabel,
} from "../../utils/status";
import { sourceTypeOptions } from "../../constants/sourceTypeOptions";
import type { DataSourceManagementVm } from "../../hooks/useDataSourceManagement";

const { Text } = Typography;

export default function DataSourceAssetTable({ vm }: { vm: DataSourceManagementVm }) {
  const {
    t,
    assetSearchValue,
    setAssetSearchValue,
    setCreateProviderModalOpen,
    sources,
    scanLoading,
    sourceListPage,
    sourceListPageSize,
    sourceListTotal,
    refreshSources,
    openDetailPage,
    openEditWizard,
    requestDeleteSourceConfirm,
  } = vm;

  const assetColumns: ColumnsType<DataSourceItem> = [
    {
      title: t("admin.dataSourceTableSource"),
      dataIndex: "name",
      key: "name",
      width: 260,
      render: (_value, record) => (
        <div className="data-source-table-name">
          <span className={`data-source-icon data-source-icon-${record.type}`}>
            {sourceTypeOptions.find((item) => item.type === record.type)?.icon}
          </span>
          <div className="data-source-table-copy">
            <Button
              type="link"
              className="data-source-link-button"
              onClick={() => openDetailPage(record)}
            >
              {record.name}
            </Button>
            <Tooltip title={record.description} placement="topLeft">
              <Text
                type="secondary"
                className="data-source-ellipsis"
                tabIndex={0}
              >
                {record.description}
              </Text>
            </Tooltip>
          </div>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableType"),
      dataIndex: "type",
      key: "type",
      width: 180,
      render: (type: SourceType) => (
        <Tag className="data-source-type-tag">{getSourceTypeTitle(type, t)}</Tag>
      ),
    },
    {
      title: t("admin.dataSourceTableKnowledgeBase"),
      dataIndex: "knowledgeBase",
      key: "knowledgeBase",
      width: 130,
      ellipsis: {
        showTitle: false,
      },
      render: (knowledgeBase: string) => (
        <Tooltip title={knowledgeBase} placement="topLeft">
          <span className="data-source-ellipsis">{knowledgeBase}</span>
        </Tooltip>
      ),
    },
    {
      title: t("admin.dataSourceTableSyncStrategy"),
      key: "syncMode",
      width: 205,
      render: (_value, record) => (
        <div className="data-source-sync-cell">
          <Text strong>{getSyncModeLabel(record.syncMode, t)}</Text>
          <Text type="secondary">{record.scheduleLabel}</Text>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableConnectionStatus"),
      key: "status",
      width: 105,
      render: (_value, record) => {
        const statusMeta = getStatusMeta(record.status, t);
        const connectionMeta = getConnectionMeta(record.connectionState, t);
        return (
          <Space direction="vertical" size={4}>
            <Tag color={statusMeta.color}>{statusMeta.text}</Tag>
            <Tag color={connectionMeta.color}>{connectionMeta.text}</Tag>
          </Space>
        );
      },
    },
    {
      title: t("admin.dataSourceTableLastSync"),
      key: "lastSync",
      width: 190,
      render: (_value, record) => (
        <div className="data-source-sync-cell">
          <Text>{record.lastSync}</Text>
          <Text type="secondary">{record.nextSync}</Text>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableSummary"),
      key: "summary",
      width: 150,
      render: (_value, record) => (
        <div className="data-source-sync-cell">
          <Text type="secondary">
            {t("admin.dataSourceSummaryChanges", {
              add: record.addCount,
              change: record.changeCount,
              del: record.deleteCount,
            })}
          </Text>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableActions"),
      key: "actions",
      width: 220,
      fixed: "right",
      className: "data-source-action-column",
      render: (_value, record) => (
        <Space size={12} className="data-source-table-actions">
          <Button type="link" icon={<EyeOutlined />} onClick={() => openDetailPage(record)}>
            {t("admin.dataSourceActionDetail")}
          </Button>
          <Button type="link" icon={<EditOutlined />} onClick={() => openEditWizard(record)}>
            {t("admin.dataSourceActionConfig")}
          </Button>
          <Button
            type="link"
            danger
            icon={<DeleteOutlined />}
            onClick={() => requestDeleteSourceConfirm(record)}
          >
            {t("common.delete")}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <main className="data-source-asset-directory">
      <div className="data-source-asset-toolbar">
        <Input
          allowClear
          prefix={<SearchOutlined />}
          value={assetSearchValue}
          onChange={(event) => setAssetSearchValue(event.target.value)}
          placeholder={t("admin.dataSourceAssetSearchPlaceholder")}
          className="data-source-asset-search"
        />
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setCreateProviderModalOpen(true)}
        >
          {t("admin.dataSourceCreateKnowledgeSource")}
        </Button>
      </div>
      <div className="data-source-asset-table-wrap">
        <Table<DataSourceItem>
          className="admin-page-table data-source-asset-table"
          rowKey="id"
          columns={assetColumns}
          dataSource={sources}
          loading={scanLoading}
          pagination={{
            current: sourceListPage,
            pageSize: sourceListPageSize,
            total: sourceListTotal,
            showSizeChanger: true,
            showTotal: (total) => t("common.totalItems", { total }),
            onChange: (page, pageSize) => {
              void refreshSources(false, {
                page,
                pageSize,
                keyword: assetSearchValue,
              });
            },
          }}
          tableLayout="fixed"
          scroll={{ x: 1480, y: "calc(100vh - 380px)" }}
          locale={{
            emptyText: (
              <div className="data-source-asset-empty">
                <DatabaseOutlined />
                <Text strong>{t("admin.dataSourceAssetNoResultTitle")}</Text>
                <Text type="secondary">{t("admin.dataSourceAssetNoResultDesc")}</Text>
              </div>
            ),
          }}
        />
      </div>
    </main>
  );
}
