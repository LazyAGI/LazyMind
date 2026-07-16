import { Button, Empty, Input, Select, Table } from "antd";
import { ApartmentOutlined } from "@ant-design/icons";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import type { ColumnsType } from "antd/es/table";
import type { SkillTreeNode, StructuredAsset } from "../../shared";

interface SkillInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  loading: boolean;
  skillAssets: StructuredAsset[];
  dataSource: SkillTreeNode[];
  searchInput: string;
  onSearchInputChange: (value: string) => void;
  onSearch: (value: string) => void;
  category?: string;
  onCategoryChange: (value?: string) => void;
  categories: string[];
  categoriesLoading: boolean;
  source: "all" | "builtin" | "admin" | "personal";
  onSourceChange: (value: "all" | "builtin" | "admin" | "personal") => void;
  onReset: () => void;
  organizeMode: boolean;
  organizeLoading: boolean;
  selectedOrganizeSkillIds: string[];
  onOrganizeSelectionChange: (
    records: StructuredAsset[],
    selected: boolean,
  ) => void;
  onOrganizeCancel: () => void;
  onOrganizeSubmit: () => void;
  columns: ColumnsType<StructuredAsset>;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number, pageSize: number) => void;
  tableScroll?: { x?: number; y?: number };
  listContentRef: React.RefObject<HTMLDivElement>;
}

const defaultPageSizeOptions = [6, 12, 20, 50];

export default function SkillInstalledView({
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
  source,
  onSourceChange,
  onReset,
  organizeMode,
  organizeLoading,
  selectedOrganizeSkillIds,
  onOrganizeSelectionChange,
  onOrganizeCancel,
  onOrganizeSubmit,
  columns,
  page,
  pageSize,
  total,
  onPageChange,
  tableScroll,
  listContentRef,
}: SkillInstalledViewProps) {
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

  return (
    <div className="memory-skill-installed">
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
        <Select
          value={source}
          className="memory-skill-installed-select"
          options={[
            { value: "all", label: t("admin.memorySkillInstalledSourceAll") },
            { value: "builtin", label: t("admin.memorySkillSourceBuiltin") },
            { value: "admin", label: t("admin.memorySkillSourceAdmin") },
            { value: "personal", label: t("admin.memorySkillSourcePersonal") },
          ]}
          onChange={onSourceChange}
        />
        <Button type="default" className="memory-skill-reset-button" onClick={onReset}>
          {t("admin.memoryReset")}
        </Button>
      </div>

      {organizeMode ? (
        <div
          className="memory-skill-organize-bar"
          role="status"
          aria-live="polite"
        >
          <div className="memory-skill-organize-bar__summary">
            <span className="memory-skill-organize-bar__icon" aria-hidden="true">
              <ApartmentOutlined />
            </span>
            <span className="memory-skill-organize-bar__copy">
              <strong>
                {t("admin.memorySkillOrganizeSelected", {
                  count: selectedOrganizeSkillIds.length,
                })}
              </strong>
              <span>{t("admin.memorySkillOrganizeLimit")}</span>
            </span>
          </div>
          <div className="memory-skill-organize-bar__actions">
            <Button onClick={onOrganizeCancel} disabled={organizeLoading}>
              {t("common.cancel")}
            </Button>
            <Button
              type="primary"
              icon={<ApartmentOutlined />}
              loading={organizeLoading}
              disabled={selectedOrganizeSkillIds.length === 0}
              onClick={onOrganizeSubmit}
            >
              {t("admin.memorySkillOrganizeSubmit")}
            </Button>
          </div>
        </div>
      ) : null}

      <div className="memory-list-content" ref={listContentRef}>
        <Table<StructuredAsset>
          className="admin-page-table memory-table memory-skill-installed-table"
          rowKey="id"
          loading={loading}
          dataSource={dataSource}
          columns={columns}
          rowSelection={
            organizeMode
              ? {
                  selectedRowKeys: selectedOrganizeSkillIds,
                  preserveSelectedRowKeys: true,
                  columnWidth: 48,
                  onSelect: (record: StructuredAsset, selected: boolean) =>
                    onOrganizeSelectionChange([record], selected),
                  onSelectAll: (
                    selected: boolean,
                    _selectedRows: StructuredAsset[],
                    changedRows: StructuredAsset[],
                  ) =>
                    onOrganizeSelectionChange(changedRows, selected),
                  getCheckboxProps: (record: StructuredAsset) => ({
                    disabled:
                      selectedOrganizeSkillIds.length >= 20 &&
                      !selectedOrganizeSkillIds.includes(record.id),
                    "aria-label": t("admin.memorySkillOrganizeSelectRow", {
                      name: record.name,
                    }),
                  }),
                }
              : undefined
          }
          pagination={pagination}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t("admin.memoryEmpty")}
              />
            ),
          }}
          scroll={tableScroll}
        />
      </div>
    </div>
  );
}
