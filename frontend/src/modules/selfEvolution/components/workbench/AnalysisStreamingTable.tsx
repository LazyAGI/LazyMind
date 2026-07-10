import { useEffect, useRef, useState } from "react";
import { Table, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { buildAnalysisStreamingColumns } from "../../hooks/controller/columns";
import type { AnalysisStreamingRow } from "../../hooks/controller/types";

const { Text } = Typography;
const PAGE_SIZE = 10;

function getLastPage(rowCount: number) {
  return Math.max(1, Math.ceil(rowCount / PAGE_SIZE));
}

export function AnalysisStreamingTable({
  rows,
  current,
  total,
}: {
  rows: AnalysisStreamingRow[];
  current: number;
  total: number;
}) {
  const { t } = useTranslation();
  const columns = buildAnalysisStreamingColumns(t);
  const progressTotal = total || rows.length;
  const progressCurrent = current;
  const [currentPage, setCurrentPage] = useState(1);
  const prevProgressCurrentRef = useRef(0);
  const totalPages = getLastPage(rows.length);
  const pageRows = rows.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);

  useEffect(() => {
    const prevProgressCurrent = prevProgressCurrentRef.current;
    prevProgressCurrentRef.current = current;

    if (current > prevProgressCurrent) {
      const activePage = Math.max(1, Math.ceil(current / PAGE_SIZE));
      const prevActivePage = Math.max(1, Math.ceil(prevProgressCurrent / PAGE_SIZE));
      setCurrentPage((page) => (page === prevActivePage ? activePage : page));
      return;
    }

    setCurrentPage((page) => Math.min(page, getLastPage(rows.length)));
  }, [current, rows.length]);

  return (
    <section
      className="self-evolution-dataset-streaming"
      aria-label={t("selfEvolutionRun.analysisStreamingAria")}
    >
      <div className="self-evolution-dataset-cases-head">
        <Text>{t("selfEvolutionRun.analysisStreamingTitle")}</Text>
        <Text>
          {progressTotal > 0
            ? t("selfEvolutionRun.analysisStreamingProgress", {
                current: progressCurrent,
                total: progressTotal,
              })
            : t("selfEvolutionRun.analysisStreamingWaiting")}
        </Text>
      </div>
      <Table<AnalysisStreamingRow>
        className="self-evolution-dataset-table self-evolution-dataset-streaming-table"
        size="small"
        rowKey="key"
        columns={columns}
        dataSource={pageRows}
        locale={{ emptyText: t("selfEvolutionRun.analysisStreamingEmpty") }}
        scroll={{ x: 360 }}
        pagination={false}
      />
      {rows.length > 0 ? (
        <div
          className="self-evolution-dataset-pagination"
          aria-label={t("selfEvolutionRun.analysisStreamingPaginationAria")}
        >
          <button
            type="button"
            className="self-evolution-dataset-page-btn"
            disabled={currentPage <= 1}
            onClick={() => setCurrentPage((page) => Math.max(1, page - 1))}
          >
            {t("selfEvolutionRun.analysisStreamingPrevPage")}
          </button>
          <Text className="self-evolution-dataset-page-indicator">
            {t("selfEvolutionRun.analysisStreamingPageIndicator", {
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
            {t("selfEvolutionRun.analysisStreamingNextPage")}
          </button>
        </div>
      ) : null}
    </section>
  );
}
