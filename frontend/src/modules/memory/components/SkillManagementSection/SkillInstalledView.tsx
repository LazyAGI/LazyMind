import { useEffect, useState } from "react";
import type { SkillCallMode, SkillOrganizeDepth } from "../../skillApi";
import { Button, Dropdown, Empty, Popconfirm, Table } from "antd";
import { ApartmentOutlined, DownOutlined } from "@ant-design/icons";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import type { ColumnsType } from "antd/es/table";
import type { SkillTreeNode, StructuredAsset } from "../../shared";
import {
  canSubmitSkillOrganize,
  isSkillOrganizeEligible,
  isSkillOrganizeInScope,
  MAX_SKILL_ORGANIZE_SELECTION,
} from "./skillOrganizeRules";
import { getSkillCallModeMenuItems } from "./SkillCallModeControl";

interface SkillInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  loading: boolean;
  skillAssets: StructuredAsset[];
  dataSource: SkillTreeNode[];
  category?: string;
  onCategoryChange: (value?: string) => void;
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
  category,
  onCategoryChange,
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
  const [batchManageMode, setBatchManageMode] = useState(false);
  useEffect(() => {
    if (organizeMode) {
      setBatchManageMode(false);
    }
  }, [organizeMode]);
  const isDeepOrganize = organizeMode && organizeDepth === "deep";
  const depthHint = t(organizeDepth === "light" ? "admin.memorySkillOrganizeLightHint" : "admin.memorySkillOrganizeDeepHint");
  const organizeActionTitle = t(organizeDepth === "light" ? "admin.memorySkillOrganizeLight" : "admin.memorySkillOrganizeDeep");
  const organizeActionScope = t(organizeDepth === "light" ? "admin.memorySkillOrganizeLightScope" : "admin.memorySkillOrganizeDeepScope");
  const tableData = isDeepOrganize
    ? dataSource.filter((row) => isSkillOrganizeInScope(row, "deep"))
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
  const visibleColumns: ColumnsType<SkillTreeNode> = columns.filter((column) => {
    if (column.key === "tags") {
      return false;
    }
    return !(organizeMode && (column.key === "actions" || column.key === "callMode"));
  });
  if (organizeMode && tableData.some((record) => record.draft?.hasUncommittedDraft)) {
    visibleColumns.push({
      key: "organizeDraftStatus",
      title: t("admin.memorySkillPendingDrafts"),
      width: 180,
      render: (_, record) => record.draft?.hasUncommittedDraft
        ? t("admin.memorySkillOrganizePendingDraftRow", {name: record.name})
        : null,
    });
  }
  const canSubmitOrganize = canSubmitSkillOrganize(
    selectedOrganizeSkillIds.length,
  );
  const normalSelectionEnabled = Boolean(
    batchManageMode && onSkillSelectionChange && onBatchCallMode,
  );

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
        {!organizeMode && onSkillSelectionChange && onBatchCallMode ? (
          <button
            type="button"
            className={`memory-skill-batch-toggle${batchManageMode ? " is-active" : ""}`}
            aria-pressed={batchManageMode}
            onClick={() => {
              if (batchManageMode) {
                onClearSkillSelection?.();
                setBatchManageMode(false);
                return;
              }
              setBatchManageMode(true);
            }}
          >
            {t(batchManageMode ? "admin.memorySkillBatchManageDone" : "admin.memorySkillBatchManage")}
          </button>
        ) : null}
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
          className={`admin-page-table memory-table memory-skill-installed-table${organizeMode || normalSelectionEnabled ? " is-selectable" : ""}${organizeMode ? " is-organize" : ""}`}
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
                          : record.draft?.hasUncommittedDraft
                            ? "admin.memorySkillOrganizePendingDraftRow"
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
          // Let the existing flex layout own the viewport height. A cached pixel
          // measurement from selection/review mode can leave only two rows visible.
          scroll={{ x: tableScroll?.x ?? 656, y: "100%" }}
        />
      </div>
    </div>
  );
}
