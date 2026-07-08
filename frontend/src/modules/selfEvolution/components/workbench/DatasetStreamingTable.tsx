import { useEffect, useRef, useState } from "react";
import { Table, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { buildDatasetStreamingColumns } from "../../hooks/controller/columns";
import type { DatasetStreamingRow } from "../../hooks/controller/types";

const { Text } = Typography;
const PAGE_SIZE = 10;

function getLastPage(rowCount: number) {
  return Math.max(1, Math.ceil(rowCount / PAGE_SIZE));
}

function getProgressPage(progressCurrent: number) {
  return Math.max(1, Math.ceil(progressCurrent / PAGE_SIZE));
}

export function DatasetStreamingTable({
  rows,
  current,
  total,
}: {
  rows: DatasetStreamingRow[];
  current: number;
  total: number;
}) {
  const { t } = useTranslation();
  const columns = buildDatasetStreamingColumns(t);
  const [currentPage, setCurrentPage] = useState(1);
  const prevProgressCurrentRef = useRef(0);
  const totalPages = getLastPage(rows.length);
  const pageRows = rows.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);

  useEffect(() => {
    const prevProgressCurrent = prevProgressCurrentRef.current;
    prevProgressCurrentRef.current = current;

    if (current > prevProgressCurrent) {
      const activePage = getProgressPage(current);
      const prevActivePage = getProgressPage(prevProgressCurrent);
      setCurrentPage((page) => (page === prevActivePage ? activePage : page));
      return;
    }

    setCurrentPage((page) => Math.min(page, getLastPage(rows.length)));
  }, [current, rows.length]);

  return (
    <section className="self-evolution-dataset-streaming" aria-label={t("selfEvolutionRun.datasetStreamingAria")}>
      <div className="self-evolution-dataset-cases-head">
        <Text>{t("selfEvolutionRun.datasetStreamingTitle")}</Text>
        <Text>
          {total > 0
            ? t("selfEvolutionRun.datasetStreamingProgress", { current, total })
            : t("selfEvolutionRun.datasetStreamingWaiting")}
        </Text>
      </div>
      <Table<DatasetStreamingRow>
        className="self-evolution-dataset-table self-evolution-dataset-streaming-table"
        size="small"
        rowKey="key"
        columns={columns}
        dataSource={pageRows}
        locale={{ emptyText: t("selfEvolutionRun.datasetStreamingEmpty") }}
        scroll={{ x: 320 }}
        pagination={false}
      />
      {rows.length > 0 ? (
        <div className="self-evolution-dataset-pagination" aria-label={t("selfEvolutionRun.datasetStreamingPaginationAria")}>
          <button
            type="button"
            className="self-evolution-dataset-page-btn"
            disabled={currentPage <= 1}
            onClick={() => setCurrentPage((page) => Math.max(1, page - 1))}
          >
            {t("selfEvolutionRun.datasetStreamingPrevPage")}
          </button>
          <Text className="self-evolution-dataset-page-indicator">
            {t("selfEvolutionRun.datasetStreamingPageIndicator", {
              current: currentPage,
              total: totalPages,
            })}
          </Text>
          <button
            type="button"
            className="self-evolution-dataset-page-btn"
            disabled={currentPage >= totalPages}
            onClick={() => setCurrentPage((page) => Math.min(totalPages, page + 1))}
          >
            {t("selfEvolutionRun.datasetStreamingNextPage")}
          </button>
        </div>
      ) : null}
    </section>
  );
}
