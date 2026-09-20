import type { SkillCallMode, SkillOrganizeDepth } from "../../skillApi";
import { Button, Dropdown, Empty, Input, Popconfirm, Select, Table } from "antd";
import { ApartmentOutlined, DownOutlined } from "@ant-design/icons";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import type { ColumnsType } from "antd/es/table";
import type { SkillTreeNode, StructuredAsset } from "../../shared";
import {
  canSubmitSkillOrganize,
  isSkillOrganizeEligible,
  MAX_SKILL_ORGANIZE_SELECTION,
} from "./skillOrganizeRules";
import { getSkillCallModeMenuItems } from "./SkillCallModeControl";

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
  onReset: () => void;
  organizeMode: boolean;
  organizeDepth: SkillOrganizeDepth;
  organizeLoading: boolean;
  selectedOrganizeSkillIds: string[];
  onOrganizeSelectionChange: (
    records: StructuredAsset[],
    selected: boolean,
  ) => void;
  onOrganizeCancel: () => void;
  onOrganizeSubmit: (mode: SkillOrganizeDepth) => void;
  columns: ColumnsType<StructuredAsset>;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number, pageSize: number) => void;
  tableScroll?: { x?: number; y?: number };
  listContentRef: React.RefObject<HTMLDivElement>;
  selectedSkillIds?: string[];
  onSkillSelectionChange?: (
    records: StructuredAsset[],
    selected: boolean,
  ) => void;
  onClearSkillSelection?: () => void;
  onBatchCallMode?: (mode: SkillCallMode) => void;
  batchCallModeLoading?: boolean;
}

const defaultPageSizeOptions = [20, 50, 100];

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
  onReset,
  organizeMode,
  organizeDepth,
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
  selectedSkillIds = [],
  onSkillSelectionChange,
  onClearSkillSelection,
  onBatchCallMode,
  batchCallModeLoading = false,
}: SkillInstalledViewProps) {
  const isDeepOrganize = organizeMode && organizeDepth === "deep";
  const depthHint = t(organizeDepth === "light" ? "admin.memorySkillOrganizeLightHint" : "admin.memorySkillOrganizeDeepHint");
  const organizeActionTitle = t(organizeDepth === "light" ? "admin.memorySkillOrganizeLight" : "admin.memorySkillOrganizeDeep");
  const organizeActionScope = t(organizeDepth === "light" ? "admin.memorySkillOrganizeLightScope" : "admin.memorySkillOrganizeDeepScope");
  const tableData = isDeepOrganize
    ? dataSource.filter((row) => isSkillOrganizeEligible(row, "deep"))
    : dataSource;
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
  const visibleColumns = columns.filter((column) => column.key !== "tags");
  const canSubmitOrganize = canSubmitSkillOrganize(
    selectedOrganizeSkillIds.length,
  );
  const normalSelectionEnabled = Boolean(onSkillSelectionChange && onBatchCallMode);
  const legacyCategories = categories.filter(
    (item) => item !== "internal" && item !== "external",
  );
  const selectedLegacyCategory = legacyCategories.includes(category || "")
    ? category
    : undefined;

  return (
    <div className="memory-skill-installed">
      <div className="memory-skill-installed-filters">
        <div className="memory-skill-source-tabs" role="tablist">
          {([
            [undefined, "admin.memorySkillSourceAll"],
            ["__builtin", "admin.memorySkillOriginBuiltin"],
            ["internal", "admin.memorySkillSourceInternal"],
            ["external", "admin.memorySkillSourceExternal"],
          ] as const).map(([value, labelKey]) => {
            const selected = isDeepOrganize
              ? value === "internal"
              : (category || undefined) === value;
            return (
            <button
              type="button"
              role="tab"
              aria-selected={selected}
              className={selected ? "is-active" : undefined}
              key={value || "all"}
              disabled={isDeepOrganize && value !== "internal"}
              onClick={() => {
                if (isDeepOrganize && value !== "internal") return;
                onCategoryChange(value);
              }}
            >
              {t(labelKey)}
            </button>
            );
          })}
        </div>
        <Input.Search
          allowClear
          value={searchInput}
          onChange={(event) => onSearchInputChange(event.target.value)}
          onSearch={onSearch}
          placeholder={t("admin.memorySkillSearchPlaceholder")}
          className="memory-skill-installed-search"
        />
        {legacyCategories.length > 0 && !isDeepOrganize ? (
          <Select
            allowClear
            aria-label={t("admin.memorySkillLegacyCategoryFilter")}
            value={selectedLegacyCategory}
            placeholder={t("admin.memorySkillLegacyCategoryFilter")}
            loading={categoriesLoading}
            options={legacyCategories.map((item) => ({
              label: item,
              value: item,
            }))}
            className="memory-skill-installed-select"
            onChange={(value) => onCategoryChange(value)}
          />
        ) : null}
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
              <span>{organizeActionTitle} · {organizeActionScope}</span>
              <span>{depthHint}</span>
              <span>{t("admin.memorySkillOrganizeRequirement")}</span>
            </span>
          </div>
          <div className="memory-skill-organize-bar__actions">
            <Button onClick={onOrganizeCancel} disabled={organizeLoading}>
              {t("common.cancel")}
            </Button>
            <Popconfirm
              title={t(organizeDepth === "light" ? "admin.memorySkillOrganizeConfirmTitleLight" : "admin.memorySkillOrganizeConfirmTitleDeep", {
                count: selectedOrganizeSkillIds.length,
              })}
              description={<>{depthHint}<br />{t("admin.memorySkillOrganizeConfirmContent")}</>}
              okText={t(organizeDepth === "light" ? "admin.memorySkillOrganizeConfirmSubmitLight" : "admin.memorySkillOrganizeConfirmSubmitDeep")}
              cancelText={t("common.cancel")}
              disabled={!canSubmitOrganize || organizeLoading}
              onConfirm={() => {
                void onOrganizeSubmit(organizeDepth);
              }}
            >
              <Button
                type="primary"
                icon={<ApartmentOutlined aria-hidden="true" />}
                loading={organizeLoading}
                disabled={!canSubmitOrganize}
              >
                {t(organizeDepth === "light" ? "admin.memorySkillOrganizeSubmitLight" : "admin.memorySkillOrganizeSubmitDeep")}
              </Button>
            </Popconfirm>
          </div>
        </div>
      ) : null}

      {!organizeMode && normalSelectionEnabled && selectedSkillIds.length > 0 ? (
        <div className="memory-skill-batch-bar" role="status" aria-live="polite">
          <strong>{t("admin.memorySkillBatchSelected", { count: selectedSkillIds.length })}</strong>
          <Dropdown
            overlayClassName="memory-skill-call-mode-menu"
            disabled={batchCallModeLoading}
            trigger={["click"]}
            menu={{
              items: getSkillCallModeMenuItems(t),
              onClick: ({ key }) => onBatchCallMode?.(key as SkillCallMode),
            }}
          >
            <Button loading={batchCallModeLoading} disabled={batchCallModeLoading}>
              {t("admin.memorySkillBatchCallMode")}
              <DownOutlined aria-hidden="true" />
            </Button>
          </Dropdown>
          <Button
            className="memory-skill-batch-clear"
            disabled={batchCallModeLoading}
            onClick={onClearSkillSelection}
          >
            {t("admin.memorySkillClearSelection")}
          </Button>
        </div>
      ) : null}

      <div className="memory-list-content" ref={listContentRef}>
        <Table<StructuredAsset>
          className="admin-page-table memory-table memory-skill-installed-table"
          rowKey="id"
          loading={loading}
          dataSource={tableData}
          columns={visibleColumns}
          rowSelection={
            organizeMode
              ? {
                  selectedRowKeys: selectedOrganizeSkillIds,
                  preserveSelectedRowKeys: true,
                  columnWidth: 40,
                  onSelect: (record: StructuredAsset, selected: boolean) =>
                    onOrganizeSelectionChange([record], selected),
                  onSelectAll: (
                    selected: boolean,
                    _selectedRows: StructuredAsset[],
                    changedRows: StructuredAsset[],
                  ) =>
                    onOrganizeSelectionChange(changedRows, selected),
                  getCheckboxProps: (record: StructuredAsset) => {
                    const eligible = isSkillOrganizeEligible(record, organizeDepth);
                    return {
                      disabled:
                        !eligible ||
                        (selectedOrganizeSkillIds.length >=
                          MAX_SKILL_ORGANIZE_SELECTION &&
                          !selectedOrganizeSkillIds.includes(record.id)),
                      "aria-label": t(
                        eligible
                          ? "admin.memorySkillOrganizeSelectRow"
                          : record.readonly || record.cloudResourceId
                            ? "admin.memorySkillOrganizeLocalEditableOnlyRow"
                            : "admin.memorySkillOrganizeInternalOnlyRow",
                        { name: record.name },
                      ),
                    };
                  },
                }
              : normalSelectionEnabled
                ? {
                    selectedRowKeys: selectedSkillIds,
                    preserveSelectedRowKeys: true,
                    columnWidth: 40,
                    onSelect: (record: StructuredAsset, selected: boolean) =>
                      onSkillSelectionChange?.([record], selected),
                    onSelectAll: (
                      selected: boolean,
                      _selectedRows: StructuredAsset[],
                      changedRows: StructuredAsset[],
                    ) => onSkillSelectionChange?.(changedRows, selected),
                    getCheckboxProps: (record: StructuredAsset) => ({
                      disabled:
                        Boolean(record.readonly) ||
                        Boolean(record.cloudResourceId) ||
                        batchCallModeLoading,
                      "aria-label": t("admin.memorySkillBatchSelectRow", { name: record.name }),
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
