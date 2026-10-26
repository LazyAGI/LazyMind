import { useEffect, useRef, useState } from "react";
import { Button, Empty, Space, Table } from "antd";
import { PlusOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import type { TFunction } from "i18next";
import type { GlossaryAsset, GlossarySource } from "../shared";
import ExpandableTitleSearch from "./SkillManagementSection/ExpandableTitleSearch";
import SkillModuleState from "./SkillManagementSection/SkillModuleState";
import "./SkillManagementSection/index.scss";

interface GlossaryListSectionProps {
  t: TFunction;
  assets: GlossaryAsset[];
  columns: ColumnsType<GlossaryAsset>;
  filteredItems: GlossaryAsset[];
  glossaryListPage: number;
  glossaryListPageSize: number;
  glossaryListTotal: number;
  glossaryLoadError: string;
  glossaryLoading: boolean;
  glossarySource?: GlossarySource;
  availableGlossarySourceOptions: Array<{ value: GlossarySource; label: string }>;
  searchInput: string;
  query: string;
  inboxCount: number;
  showInbox: boolean;
  onSearchInputChange: (value: string) => void;
  onSearch: (value: string) => void;
  onSourceChange: (value?: GlossarySource) => void;
  onCreate: () => void;
  onOpenInbox: () => void;
  handleBatchDeleteGlossary: () => void;
  handleBatchMergeGlossary: () => void;
  refreshGlossaryAssets: (options?: {
    keyword?: string;
    page?: number;
    pageSize?: number;
    silent?: boolean;
    source?: GlossarySource;
  }) => void;
  selectedGlossaryAssetIds: string[];
  selectedGlossaryAssets: GlossaryAsset[];
  setGlossaryListPage: (page: number) => void;
  setGlossaryListPageSize: (pageSize: number) => void;
  setSelectedGlossaryAssetIds: (ids: string[]) => void;
}

const defaultPageSizeOptions = [20, 50, 100];

export default function GlossaryListSection(props: GlossaryListSectionProps) {
  const {
    t,
    assets,
    columns,
    filteredItems,
    glossaryListPage,
    glossaryListPageSize,
    glossaryListTotal,
    glossaryLoadError,
    glossaryLoading,
    glossarySource,
    availableGlossarySourceOptions,
    searchInput,
    query,
    inboxCount,
    showInbox,
    onSearchInputChange,
    onSearch,
    onSourceChange,
    onCreate,
    onOpenInbox,
    handleBatchDeleteGlossary,
    handleBatchMergeGlossary,
    refreshGlossaryAssets,
    selectedGlossaryAssetIds,
    selectedGlossaryAssets,
    setGlossaryListPage,
    setGlossaryListPageSize,
    setSelectedGlossaryAssetIds,
  } = props;
  const sectionRef = useRef<HTMLDivElement>(null);
  const [tableBodyHeight, setTableBodyHeight] = useState<number>();

  useEffect(() => {
    const sectionElement = sectionRef.current;
    if (!sectionElement) {
      return undefined;
    }

    const updateRowHeight = () => {
      const toolbarElement = sectionElement.querySelector<HTMLElement>(
        ".memory-glossary-batch-toolbar",
      );
      const headerElement = sectionElement.querySelector<HTMLElement>(".ant-table-thead");
      const paginationElement =
        sectionElement.querySelector<HTMLElement>(".ant-table-pagination");
      const sectionStyle = window.getComputedStyle(sectionElement);
      const rowGap = Number.parseFloat(sectionStyle.rowGap || sectionStyle.gap || "0") || 0;
      const availableHeight =
        sectionElement.getBoundingClientRect().height -
        (toolbarElement?.getBoundingClientRect().height ?? 0) -
        (headerElement?.getBoundingClientRect().height ?? 0) -
        (paginationElement?.getBoundingClientRect().height ?? 0) -
        rowGap -
        8;
      const nextBodyHeight = Math.max(240, Math.floor(availableHeight));
      setTableBodyHeight((previous) =>
        previous === nextBodyHeight ? previous : nextBodyHeight,
      );
    };

    updateRowHeight();
    const resizeObserver = new ResizeObserver(updateRowHeight);
    resizeObserver.observe(sectionElement);
    window.addEventListener("resize", updateRowHeight);

    return () => {
      resizeObserver.disconnect();
      window.removeEventListener("resize", updateRowHeight);
    };
  }, []);

  const glossaryTableScroll = tableBodyHeight
    ? { x: 1120, y: tableBodyHeight }
    : { x: 1120 };

  return (
    <div className="memory-glossary-shell">
      <div className="memory-skill-toolbar">
        <div className="memory-skill-heading-group">
          <h2>{t("admin.memoryTabGlossary")}</h2>
          <ExpandableTitleSearch
            value={searchInput}
            placeholder={t("admin.memorySearchPlaceholder")}
            onChange={onSearchInputChange}
            onSearch={onSearch}
          />
        </div>
        <div className="memory-skill-toolbar-actions">
          {showInbox ? (
            <button type="button" className="memory-skill-review-drafts" onClick={onOpenInbox}>
              {t("admin.memoryGlossaryInboxButton", { count: inboxCount })}
            </button>
          ) : null}
          <button
            type="button"
            className="memory-skill-create-split is-single"
            onClick={onCreate}
          >
            <span className="memory-skill-create-split__main">
              <PlusOutlined aria-hidden="true" />
              {t("admin.memoryCreateGlossaryButton")}
            </span>
          </button>
        </div>
      </div>

      <div className="memory-skill-installed-filters">
        <div className="memory-skill-source-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={!glossarySource}
            className={!glossarySource ? "is-active" : undefined}
            onClick={() => onSourceChange(undefined)}
          >
            {t("admin.memoryAllSources")}
          </button>
          {availableGlossarySourceOptions.map((option) => (
            <button
              key={option.value}
              type="button"
              role="tab"
              aria-selected={glossarySource === option.value}
              className={glossarySource === option.value ? "is-active" : undefined}
              onClick={() => onSourceChange(option.value)}
            >
              {option.label}
            </button>
          ))}
        </div>
      </div>

      <div className="memory-glossary-section" ref={sectionRef}>
        {glossaryLoadError ? (
          <SkillModuleState
            tone="error"
            retryLabel={t("common.retry")}
            onRetry={() =>
              refreshGlossaryAssets({
                keyword: query,
                page: glossaryListPage,
                pageSize: glossaryListPageSize,
                source: glossarySource,
              })
            }
          >
            {glossaryLoadError}
          </SkillModuleState>
        ) : null}

        <div className="memory-glossary-batch-toolbar">
          <span>
            {t("admin.memoryGlossaryBatchStats", {
              selected: selectedGlossaryAssets.length,
              total: glossaryListTotal || assets.length,
            })}
          </span>
          <Space size={8} wrap>
            <Button
              type="primary"
              disabled={!selectedGlossaryAssets.length || glossaryLoading}
              onClick={handleBatchMergeGlossary}
            >
              {t("admin.memoryGlossaryBatchMerge")}
            </Button>
            <Button
              danger
              disabled={!selectedGlossaryAssets.length || glossaryLoading}
              onClick={handleBatchDeleteGlossary}
            >
              {t("admin.memoryGlossaryBatchDelete")}
            </Button>
          </Space>
        </div>

        <Table<GlossaryAsset>
          className="admin-page-table memory-table memory-glossary-table memory-skill-installed-table"
          rowKey="id"
          loading={glossaryLoading}
          dataSource={filteredItems}
          columns={columns}
          rowSelection={{
            selectedRowKeys: selectedGlossaryAssetIds,
            preserveSelectedRowKeys: true,
            onChange: (selectedRowKeys) =>
              setSelectedGlossaryAssetIds(selectedRowKeys.map((key) => String(key))),
          }}
          tableLayout="fixed"
          pagination={getLocalizedTablePagination(
            {
              current: glossaryListPage,
              pageSize: glossaryListPageSize,
              total: glossaryListTotal,
              showSizeChanger: true,
              pageSizeOptions: defaultPageSizeOptions,
              showTotal: (total: number) => t("common.totalItems", { total }),
              onChange: (page: number, nextPageSize: number) => {
                setGlossaryListPage(page);
                setGlossaryListPageSize(nextPageSize);
              },
              onShowSizeChange: (_current: number, nextPageSize: number) => {
                setGlossaryListPage(1);
                setGlossaryListPageSize(nextPageSize);
              },
            },
            t,
          )}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t("admin.memoryEmpty")}
              />
            ),
          }}
          scroll={glossaryTableScroll}
        />
      </div>
    </div>
  );
}
