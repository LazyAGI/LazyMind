import { useId } from "react";
import { Slider } from "antd";
import type { PaperItem } from "./model";
import { DEFAULT_PAPER_FILTERS, getFilterBounds, hasPaperFilters, type PaperFilters } from "./paperFilters";
import "./PaperFilterPanel.scss";

interface Props {
  papers: PaperItem[];
  value: PaperFilters;
  onChange: (value: PaperFilters) => void;
}

function Choices<T extends string>({ label, value, options, onChange }: {
  label: string;
  value: T;
  options: Array<{ value: T; label: string }>;
  onChange: (value: T) => void;
}) {
  return <fieldset className="paper-filter-group">
    <legend>{label}</legend>
    <div className="paper-filter-options">
      {options.map((option) => <button key={option.value} type="button" aria-pressed={value === option.value} onClick={() => onChange(option.value)}>{option.label}</button>)}
    </div>
  </fieldset>;
}

export default function PaperFilterPanel({ papers, value, onChange }: Props) {
  const id = useId();
  const bounds = getFilterBounds(papers);
  const years = value.yearRange || bounds.years;
  const minYear = Math.min(bounds.years?.[0] ?? years?.[0] ?? 0, years?.[0] ?? Number.POSITIVE_INFINITY);
  const maxYear = Math.max(bounds.years?.[1] ?? 0, years?.[1] ?? 0);
  const citationMax = Math.max(bounds.maxCitations, value.minCitations, 1);
  const update = <K extends keyof PaperFilters>(key: K, next: PaperFilters[K]) => onChange({ ...value, [key]: next });
  const updateYear = (range: number[]) => {
    const next: [number, number] = [range[0], range[1]];
    update("yearRange", bounds.years && next[0] === bounds.years[0] && next[1] === bounds.years[1] ? null : next);
  };
  return <div className="paper-filter-panel">
    <div className="paper-filter-heading">
      <strong>筛选</strong>
      <button type="button" className="paper-filter-clear" aria-label="清除筛选" disabled={!hasPaperFilters(value)} onClick={() => onChange({ ...DEFAULT_PAPER_FILTERS })}>清除</button>
    </div>
    <Choices label="星标" value={value.star} onChange={(next) => update("star", next)} options={[
      { value: "all", label: "全部" }, { value: "starred", label: "已星标" }, { value: "unstarred", label: "未星标" },
    ]} />
    <Choices label="PDF" value={value.pdf} onChange={(next) => update("pdf", next)} options={[
      { value: "all", label: "全部" }, { value: "has-pdf", label: "有 PDF" }, { value: "needs-pdf", label: "无 PDF" }, { value: "uploaded", label: "已上传" },
    ]} />
    <Choices label="文件夹" value={value.folder} onChange={(next) => update("folder", next)} options={[
      { value: "all", label: "全部" }, { value: "filed", label: "已归档" }, { value: "unfiled", label: "未归档" },
    ]} />
    <Choices label="阅读状态" value={value.status} onChange={(next) => update("status", next)} options={[
      { value: "all", label: "全部" }, { value: "unread", label: "未读" }, { value: "reading", label: "阅读中" }, { value: "read", label: "已读" },
    ]} />
    <div className="paper-filter-range-group">
      <div className="paper-filter-range-title" id={`${id}-year`}>发表年份</div>
      {years ? <>
        <Slider range min={minYear} max={Math.max(maxYear, minYear + 1)} value={years} disabled={minYear === maxYear} onChange={updateYear} ariaLabelForHandle={["最早发表年份", "最晚发表年份"]} tooltip={{ formatter: (year: number | undefined) => `${year} 年` }} />
        <div className="paper-filter-range-values" aria-live="polite"><span>{years[0]}</span><span>{years[1]}</span></div>
      </> : <p className="paper-filter-empty">暂无年份数据</p>}
    </div>
    <div className="paper-filter-range-group paper-filter-citations">
      <div className="paper-filter-range-title" id={`${id}-citations`}>引用量</div>
      <Slider min={0} max={citationMax} value={value.minCitations} disabled={bounds.maxCitations === 0 && value.minCitations === 0} onChange={(next: number) => update("minCitations", next)} ariaLabelForHandle="最低引用量滑块" tooltip={{ formatter: (count: number | undefined) => `至少 ${count} 次` }} />
      <label className="paper-filter-citation-input">
        <span>至少</span>
        <input aria-label="最低引用量" type="number" min={0} step={1} value={value.minCitations} onChange={(event) => {
          const next = Number(event.target.value);
          if (Number.isSafeInteger(next) && next >= 0) update("minCitations", next);
        }} />
        <span>次引用</span>
      </label>
    </div>
  </div>;
}
