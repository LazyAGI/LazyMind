import { Pagination } from "antd";
import { useTranslation } from "react-i18next";

interface MemoryPaginationProps {
  page: number;
  pageSize: number;
  total: number;
  disabled: boolean;
  label: string;
  onChange: (page: number) => void;
}

export default function MemoryPagination({ page, pageSize, total, disabled, label, onChange }: MemoryPaginationProps) {
  const { t } = useTranslation();
  const pages = Math.ceil(total / pageSize);
  if (pages < 2) return null;

  return (
    <nav className="memory-list-pagination" aria-label={label}>
      <span className="memory-list-range">
        {t("admin.memoryPageRange", {
          start: (page - 1) * pageSize + 1,
          end: Math.min(page * pageSize, total),
          total,
        })}
      </span>
      <Pagination
        size="small"
        current={page}
        pageSize={pageSize}
        total={total}
        disabled={disabled}
        simple={pages > 5}
        showSizeChanger={false}
        onChange={onChange}
      />
    </nav>
  );
}
