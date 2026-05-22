import { useEffect, useRef, useState } from "react";
import { Alert, Button, Empty, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import type { TFunction } from "i18next";
import type { GlossaryAsset, GlossarySource } from "../shared";

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
  handleBatchDeleteGlossary: () => void;
  handleBatchMergeGlossary: () => void;
  query: string;
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
    handleBatchDeleteGlossary,
    handleBatchMergeGlossary,
    query,
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
    <div className="memory-glossary-section" ref={sectionRef}>
      {glossaryLoadError ? (
        <Alert
          type="error"
          showIcon
          className="memory-skill-share-alert"
          message={glossaryLoadError}
          action={
            <Button
              size="small"
              onClick={() =>
                refreshGlossaryAssets({
                  keyword: query,
                  page: glossaryListPage,
                  pageSize: glossaryListPageSize,
                  source: glossarySource,
                })
              }
            >
              {t("common.retry")}
            </Button>
          }
        />
      ) : null}

      <div className="memory-glossary-batch-toolbar">
        <span>
          {t("admin.memoryGlossaryBatchStats", {
            defaultValue: "已选 {{selected}} 条 / 共 {{total}} 条",
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
        className="admin-page-table memory-table memory-glossary-table"
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
            pageSizeOptions: [4, 8, 12, 20],
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
  );
}
