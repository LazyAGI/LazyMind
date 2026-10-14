import { useEffect, useState } from "react";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

export default function KnowledgeMarketTaskPagination({ page, total, scope, disabled, onChange }: {
  page: number; total: number; scope: "tasks" | "files"; disabled?: boolean; onChange: (page: number) => void;
}) {
  const { t } = useTranslation();
  const [input, setInput] = useState(String(page));
  const pages = Math.max(1, Math.ceil(total / 10));
  useEffect(() => { setInput(String(page)); }, [page]);
  const jump = () => {
    const value = Number(input);
    const target = Number.isInteger(value) ? Math.max(1, Math.min(pages, value)) : page;
    setInput(String(target));
    if (target !== page) onChange(target);
  };
  return <form className="market-task-pagination" aria-label={t(`knowledge.taskPagination_${scope}`)} onSubmit={(event) => { event.preventDefault(); jump(); }}>
    <span>{t("knowledge.taskPageInfo", { page, pages, total })}</span>
    <span>{t("knowledge.taskPageSize")}</span>
    <button type="button" aria-label={t(`knowledge.taskPrevious_${scope}`)} disabled={disabled || page <= 1} onClick={() => onChange(page - 1)}><LeftOutlined /></button>
    <button type="button" aria-label={t(`knowledge.taskNext_${scope}`)} disabled={disabled || page >= pages} onClick={() => onChange(page + 1)}><RightOutlined /></button>
    <label>{t("knowledge.taskJumpTo")} <input aria-label={t(`knowledge.taskJump_${scope}`)} type="number" min={1} max={pages} step={1} value={input} disabled={disabled || total === 0} onChange={(event) => setInput(event.target.value)} onBlur={jump} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); jump(); } }} /> {t("knowledge.taskPageUnit")}</label>
  </form>;
}
