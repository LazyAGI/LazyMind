import { useEffect, useRef, useState } from "react";
import { Button, Collapse, Modal, Popover, Spin, Tooltip } from "antd";
import {
  DashboardOutlined,
  DownloadOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import {
  estimateContextUsage,
  exportContextPrompt,
  type ContextUsageReport,
} from "../../utils/request";

type UsageStatus = "empty" | "loading" | "fresh" | "stale" | "error";

interface ContextUsageButtonProps {
  staleKey: string;
  resetKey: string;
  buildRequest: () => Record<string, unknown>;
  disabled?: boolean;
}

const COLORS = ["#777", "#9588d8", "#55a982", "#c58931", "#3f82ad", "#548994", "#7d93b5"];

function formatTokens(value?: number) {
  const count = value ?? 0;
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
  if (count >= 1_000) return `${(count / 1_000).toFixed(1)}K`;
  return String(count);
}

export default function ContextUsageButton({
  staleKey,
  resetKey,
  buildRequest,
  disabled,
}: ContextUsageButtonProps) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UsageStatus>("empty");
  const [report, setReport] = useState<ContextUsageReport>();
  const [open, setOpen] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [exporting, setExporting] = useState(false);
  const versionRef = useRef(0);
  const calculatedKeyRef = useRef("");
  const requestRef = useRef<Promise<void> | null>(null);
  const requestIdRef = useRef(0);

  useEffect(() => {
    versionRef.current += 1;
    if (report && calculatedKeyRef.current !== staleKey) setStatus("stale");
  }, [staleKey, report]);

  useEffect(() => {
    versionRef.current += 1;
    requestIdRef.current += 1;
    calculatedKeyRef.current = "";
    requestRef.current = null;
    setReport(undefined);
    setStatus("empty");
    setOpen(false);
    setDetailOpen(false);
  }, [resetKey]);

  useEffect(() => () => {
    requestIdRef.current += 1;
    requestRef.current = null;
  }, []);

  const calculate = () => {
    if (requestRef.current) return requestRef.current;
    const requestedVersion = versionRef.current;
    const requestedKey = staleKey;
    const requestId = ++requestIdRef.current;
    setStatus("loading");
    const request = estimateContextUsage(buildRequest())
      .then((nextReport) => {
        if (requestId !== requestIdRef.current) return;
        setReport(nextReport);
        calculatedKeyRef.current = requestedKey;
        setStatus(
          requestedVersion === versionRef.current && requestedKey === staleKey
            ? "fresh"
            : "stale",
        );
      })
      .catch(() => {
        if (requestId === requestIdRef.current) setStatus(report ? "stale" : "error");
      })
      .finally(() => {
        if (requestId === requestIdRef.current) requestRef.current = null;
      });
    requestRef.current = request;
    return request;
  };

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (nextOpen && !report && status !== "loading") void calculate();
  };
  const downloadPrompt = async () => {
    if (exporting) return;
    setExporting(true);
    try {
      const blob = await exportContextPrompt(buildRequest());
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "chatagent-context.md";
      anchor.click();
      URL.revokeObjectURL(url);
    } finally {
      setExporting(false);
    }
  };
  const ratio = report?.estimated_ratio != null
    ? Math.round(report.estimated_ratio * 100)
    : undefined;
  const categoryTitle = (categoryId: string, fallback: string) => {
    const key = `chat.contextUsageCategory.${categoryId}`;
    const translated = t(key);
    return translated === key ? fallback : translated;
  };
  const itemTitle = (categoryId: string, title: string) => {
    if (categoryId !== "conversation") return title;
    const separator = " · ";
    const [prefix, ...rest] = title.split(separator);
    const keyByPrefix: Record<string, string> = {
      "User message": "chat.contextUsageHistoryUser",
      "Assistant message": "chat.contextUsageHistoryAssistant",
      "Assistant tool call": "chat.contextUsageHistoryToolCall",
      "Tool result": "chat.contextUsageHistoryToolResult",
      "System message": "chat.contextUsageHistorySystem",
    };
    const key = keyByPrefix[prefix];
    if (!key) return title;
    return [t(key), ...rest].join(separator);
  };

  const content = (
    <div className="context-usage-popover">
      <div className="context-usage-heading">
        <span>
          <small>{t("chat.contextUsageEstimated")}</small>
          <strong>{t("chat.contextUsage")}</strong>
        </span>
        {report ? (
          <Button size="small" onClick={() => { setOpen(false); setDetailOpen(true); }}>
            {t("chat.contextUsageViewReport")}
          </Button>
        ) : null}
      </div>
      {status === "loading" && !report ? (
        <div className="context-usage-loading"><Spin size="small" /></div>
      ) : report ? (
        <>
          <div className="context-usage-summary">
            <strong>~{formatTokens(report.estimated_tokens)}</strong>
            <span>{report.max_input_tokens ? `/ ${formatTokens(report.max_input_tokens)} Tokens` : "Tokens"}</span>
            {ratio != null ? <b>{t("chat.contextUsageFull", { percent: ratio })}</b> : null}
          </div>
          <div className="context-usage-segments" aria-hidden="true">
            {report.categories.map((category, index) => (
              <i
                key={category.category_id}
                style={{
                  background: COLORS[index % COLORS.length],
                  flexGrow: category.estimated_tokens,
                }}
              />
            ))}
            {report.max_input_tokens && report.max_input_tokens > report.estimated_tokens ? (
              <i className="is-remaining" style={{ flexGrow: report.max_input_tokens - report.estimated_tokens }} />
            ) : null}
          </div>
          {status === "stale" ? (
            <div className="context-usage-stale">
              <span>{t("chat.contextUsageStale")}</span>
              <Button size="small" icon={<ReloadOutlined />} onClick={() => void calculate()}>
                {t("chat.contextUsageUpdate")}
              </Button>
            </div>
          ) : null}
          <div className="context-usage-categories">
            {report.categories.map((category, index) => (
              <div key={category.category_id} className="context-usage-category">
                <span><i style={{ background: COLORS[index % COLORS.length] }} />{categoryTitle(category.category_id, category.title)}</span>
                <b>~{formatTokens(category.estimated_tokens)}</b>
              </div>
            ))}
          </div>
        </>
      ) : (
        <div className="context-usage-error">
          <span>{t("chat.contextUsageError")}</span>
          <Button size="small" onClick={() => void calculate()}>{t("chat.contextUsageRetry")}</Button>
        </div>
      )}
    </div>
  );

  return (
    <>
      <Popover content={content} trigger="click" open={open} onOpenChange={handleOpenChange} placement="topRight">
        <Tooltip title={t("chat.contextUsageShow")}>
          <Button type="text" icon={<DashboardOutlined />} disabled={disabled} aria-label={t("chat.contextUsageShow")} />
        </Tooltip>
      </Popover>
      <Modal
        className="context-usage-report-modal"
        title={t("chat.contextUsage")}
        open={detailOpen}
        footer={null}
        width={680}
        onCancel={() => setDetailOpen(false)}
      >
        {report ? (
          <>
            <div className="context-usage-report-actions">
              <span>{t("chat.contextUsageExportHint")}</span>
              <Button
                icon={<DownloadOutlined />}
                loading={exporting}
                onClick={() => void downloadPrompt()}
              >
                {t("chat.contextUsageExport")}
              </Button>
            </div>
            <Collapse
              items={report.categories.map((category) => ({
                key: category.category_id,
                label: `${categoryTitle(category.category_id, category.title)} · ${category.item_count}`,
                extra: `~${formatTokens(category.estimated_tokens)}`,
                children: (
                  <div className="context-usage-items">
                    {category.items.map((item) => (
                      <Collapse
                        key={item.item_id}
                        ghost
                        className="context-usage-content-collapse"
                        items={[{
                          key: item.item_id,
                          label: (
                            <span className="context-usage-item-title">
                              <strong>{itemTitle(category.category_id, item.title)}</strong>
                              <small>{t("chat.contextUsageChars", { count: item.char_count })}</small>
                            </span>
                          ),
                          extra: `~${formatTokens(item.estimated_tokens)}`,
                          children: <pre className="context-usage-content">{item.content}</pre>,
                        }]}
                      />
                    ))}
                  </div>
                ),
              }))}
            />
            <p className="context-usage-note">{t("chat.contextUsageNote")}</p>
          </>
        ) : null}
      </Modal>
    </>
  );
}
