import { Popover } from "antd";
import { useTranslation } from "react-i18next";

import type { SessionPerformanceStats } from "../../utils/performanceStats";

function formatTokens(value?: number) {
  if (value == null) return "-";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(Math.round(value));
}

function formatMs(value?: number) {
  if (value == null) return "-";
  if (value >= 60_000) return `${(value / 60_000).toFixed(1)}m`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}s`;
  return `${Math.round(value)}ms`;
}

function formatPercent(value?: number) {
  if (value == null) return "-";
  return `${(value * 100).toFixed(2)}%`;
}

function formatRate(value?: number) {
  if (value == null) return "-";
  return `${value.toFixed(1)}tok/s`;
}

export default function PerformanceStatsBar({
  stats,
  running,
}: {
  stats?: SessionPerformanceStats;
  running?: boolean;
}) {
  const { t } = useTranslation();
  const segments = [
    { key: "model", label: t("chat.perf.model"), value: stats?.model || "-" },
    { key: "turns", label: t("chat.perf.turns"), value: stats ? String(stats.turns) : "-" },
    { key: "steps", label: t("chat.perf.steps"), value: stats ? String(stats.steps) : "-" },
    { key: "wall", label: t("chat.perf.wall"), value: formatMs(stats?.wallMs) },
    { key: "llm", label: t("chat.perf.llm"), value: formatMs(stats?.modelMs) },
    { key: "tool", label: t("chat.perf.tool"), value: formatMs(stats?.toolMs) },
    { key: "ttft", label: t("chat.perf.ttft"), value: formatMs(stats?.ttftMs) },
    { key: "decode", label: t("chat.perf.decode"), value: formatRate(stats?.tokS) },
    { key: "sessionCache", label: t("chat.perf.sessionCache"), value: formatPercent(stats?.sessionCacheHitRate) },
    { key: "turnCache", label: t("chat.perf.turnCache"), value: formatPercent(stats?.turnCacheHitRate) },
    { key: "in", label: t("chat.perf.input"), value: formatTokens(stats?.promptTokens) },
    { key: "out", label: t("chat.perf.output"), value: formatTokens(stats?.completionTokens) },
  ];
  const summary = segments.map((item) => item.value === "-" ? `${item.label} -` : `${item.label} ${item.value}`).join(" · ");
  const content = (
    <div className="performance-stats-popover">
      {segments.map((item) => (
        <div key={item.key} className="performance-stats-row">
          <span>{item.label}</span>
          <b>{item.value}</b>
        </div>
      ))}
    </div>
  );

  return (
    <Popover content={content} trigger="click" placement="topLeft">
      <button type="button" className="performance-stats-bar" aria-label={t("chat.perf.aria")}>
        <i className={`performance-stats-dot${running ? " is-running" : ""}`} aria-hidden="true" />
        <span>{summary}</span>
      </button>
    </Popover>
  );
}
