import { Button, Empty, Input, Modal, Select, Space, Table, Tooltip } from "antd";
import {
  DeleteOutlined,
  RedoOutlined,
  RestOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import type { StructuredAsset } from "../../shared";

interface SkillTrashedViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  loading: boolean;
  dataSource: StructuredAsset[];
  searchInput: string;
  onSearchInputChange: (value: string) => void;
  onSearch: (value: string) => void;
  category?: string;
  onCategoryChange: (value?: string) => void;
  categories: string[];
  categoriesLoading: boolean;
  onReset: () => void;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number, pageSize: number) => void;
  actionLoading: Set<string>;
  emptyTrashLoading: boolean;
  onRestore: (item: StructuredAsset) => void;
  onPurge: (item: StructuredAsset) => void;
  onEmptyTrash: () => void;
  tableScroll?: { x?: number; y?: number };
  listContentRef: React.RefObject<HTMLDivElement>;
}

const defaultPageSizeOptions = [6, 12, 20, 50];

const formatDeletedAt = (value?: string) => {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
};

export default function SkillTrashedView({
  t,
  loading,
  dataSource,
  searchInput,
  onSearchInputChange,
  onSearch,
  category,
  onCategoryChange,
  categories,
  categoriesLoading,
  onReset,
  page,
  pageSize,
  total,
  onPageChange,
  actionLoading,
  emptyTrashLoading,
  onRestore,
  onPurge,
  onEmptyTrash,
  tableScroll,
  listContentRef,
}: SkillTrashedViewProps) {
  const pagination = getLocalizedTablePagination(
    {
      current: page,
      pageSize,
      total,
      showSizeChanger: true,
      pageSizeOptions: defaultPageSizeOptions,
      showTotal: (itemTotal) => t("common.totalItems", { total: itemTotal }),
      onChange: onPageChange,
      onShowSizeChange: (_current, nextPageSize) => onPageChange(1, nextPageSize),
    },
    t,
  );

  const columns: ColumnsType<StructuredAsset> = [
    {
      title: t("admin.memoryName"),
      dataIndex: "name",
      key: "name",
      width: 220,
      ellipsis: true,
    },
    {
      title: t("admin.memoryCategory"),
      dataIndex: "category",
      key: "category",
      width: 140,
      render: (value: string) => value || "-",
    },
    {
      title: t("admin.memorySkillTrashDeletedAt"),
      key: "deletedAt",
      width: 180,
      render: (_value, record) =>
        formatDeletedAt((record as StructuredAsset & { deletedAt?: string }).deletedAt),
    },
    {
      title: t("admin.memoryOperations"),
      key: "actions",
      width: 140,
      fixed: "right",
      render: (_value, record) => (
        <Space size={4}>
          <Tooltip title={t("admin.memorySkillTrashRestore")}>
            <Button
              type="text"
              icon={<RedoOutlined />}
              loading={actionLoading.has(`restore:${record.id}`)}
              onClick={() => onRestore(record)}
            />
          </Tooltip>
          <Tooltip title={t("admin.memorySkillTrashPurge")}>
            <Button
              type="text"
              danger
              icon={<DeleteOutlined />}
              loading={actionLoading.has(`purge:${record.id}`)}
              onClick={() => onPurge(record)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  const handleEmptyTrash = () => {
    Modal.confirm({
      title: t("admin.memorySkillTrashEmptyConfirmTitle"),
      content: t("admin.memorySkillTrashEmptyConfirmContent"),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: onEmptyTrash,
    });
  };

  return (
    <div className="memory-skill-installed memory-skill-trashed">
      <div className="memory-skill-installed-filters">
        <Input.Search
          allowClear
          value={searchInput}
          onChange={(event) => onSearchInputChange(event.target.value)}
          onSearch={onSearch}
          placeholder={t("admin.memorySkillSearchPlaceholder")}
          className="memory-skill-installed-search"
        />
        <Select
          allowClear
          value={category}
          placeholder={t("admin.memoryAllCategories")}
          loading={categoriesLoading}
          options={categories.map((item) => ({
            label: item,
            value: item,
          }))}
          className="memory-skill-installed-select"
          onChange={onCategoryChange}
        />
        <Button type="default" className="memory-skill-reset-button" onClick={onReset}>
          {t("admin.memoryReset")}
        </Button>
        <Button
          danger
          icon={<RestOutlined />}
          loading={emptyTrashLoading}
          disabled={total <= 0}
          onClick={handleEmptyTrash}
        >
          {t("admin.memorySkillTrashEmpty")}
        </Button>
      </div>

      <div className="memory-list-content" ref={listContentRef}>
        <Table<StructuredAsset>
          className="admin-page-table memory-table memory-skill-installed-table"
          rowKey="id"
          loading={loading}
          dataSource={dataSource}
          columns={columns}
          pagination={pagination}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t("admin.memorySkillTrashEmptyState")}
              />
            ),
          }}
          scroll={tableScroll}
        />
      </div>
    </div>
  );
}
