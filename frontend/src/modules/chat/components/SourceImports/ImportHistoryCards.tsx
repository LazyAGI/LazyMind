import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Collapse } from "antd";
import { ExportOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import type { ImportItem } from "./api";
import { tr } from "./translations";

const processingLabels = (): Record<string, string> => ({
  submitted: tr("parsingSubmitted"),
  pending: tr("pending"),
  running: tr("processingBody"),
  failed: tr("processingFailed"),
  not_requested: tr("originalOnly"),
  not_submitted: tr("pending"),
});
function currentImportStatus(item: ImportItem) {
  const stages: Record<string, string> = {
    download: tr("download"),
    processing: tr("parseIndexStage"),
    import: tr("saveOriginal"),
    parse: tr("parse"),
    chunk: tr("chunk"),
    index: tr("index"),
  };
  const stage = stages[item.stage];
  if (item.status === "exists" || item.status === "added") {
    const levels: Record<string, string> = { parsed: tr("parsed"), chunked: tr("chunked"), indexed: tr("indexed") };
    const processing = item.processing_status === "ready"
      ? levels[item.processing_level || ""] || tr("processingReady")
      : processingLabels()[item.processing_status || ""];
    const suffix = processing ? ` · ${processing}` : "";
    return item.status === "exists" ? tr("existsStatus", { v0: suffix }) : tr("added") + suffix;
  }
  if (item.status === "original_unavailable") return tr("originalUnavailable");
  if (item.status === "failed")
    return tr("failedStatus", { v0: stage ? ` · ${stage}` : "" });
  if (item.status === "running")
    return item.document_id ? tr("savedProcessing") : tr("runningStatus", { v0: stage ? ` · ${stage}` : "" });
  if (item.status === "waiting_retry")
    return item.retry_at ? tr("waitingRetryAt", { time: new Date(item.retry_at).toLocaleTimeString() }) : tr("waitingRetry");
  if (item.status === "needs_confirmation") return tr("needsPaperConfirmation");
  if (item.status === "pending") return tr("pending");
  return tr("unknown");
}

export function importStatusLabel(item: ImportItem) {
  const status = currentImportStatus(item);
  return item.state_unavailable && status !== tr("unknown") ? status + tr("unavailableSuffix") : status;
}

function ImportBatchItems({ items, target, language, onRetry, retrying }: {
  items: ImportItem[];
  target: string;
  language: string;
  onRetry: (rows: ImportItem[]) => void;
  retrying: boolean;
}) {
  const added: ImportItem[] = [];
  const remaining: ImportItem[] = [];
  const failures = new Map<string, ImportItem[]>();
  for (const item of items) {
    if (item.status === "added") {
      added.push(item);
    } else if (item.status === "failed") {
      const reason = item.error_message || tr("importFailed");
      const group = failures.get(reason);
      if (group) group.push(item);
      else failures.set(reason, [item]);
    } else {
      remaining.push(item);
    }
  }

  const retryAvailableAt = (date: string) =>
    tr("retryAvailableAt", { time: new Date(date).toLocaleTimeString(language) });

  const renderRows = (rows: ImportItem[], sharedRetryAt?: string) => (
    <ul className="import-history-rows">
      {rows.map(item => (
        <li className="import-history-row" key={item.item_id}>
          <div className="import-history-paper">
            {item.document_id ? (
              <Link to={`/lib/knowledge/knowledge/${target}/${encodeURIComponent(item.document_id)}`}>
                {item.title}
              </Link>
            ) : item.title}
            {(item.status !== "failed" || item.state_unavailable) && <small>{importStatusLabel(item)}</small>}
            {item.status !== "failed" && item.error_message && <small>{item.error_message}</small>}
            {item.status === "failed" && !item.retryable && item.retry_at && !sharedRetryAt && (
              <small>{retryAvailableAt(item.retry_at)}</small>
            )}
          </div>
          {item.retryable && (
            <Button type="link" size="small" loading={retrying} onClick={() => onRetry([item])}>
              {tr(item.document_id ? "retryProcessing" : "retry")}
            </Button>
          )}
        </li>
      ))}
    </ul>
  );

  const groupLabel = (title: string, count: number, retryAt?: string) => (
    <span className="import-history-result-heading">
      <span>
        {title}
        {retryAt && <small>{retryAvailableAt(retryAt)}</small>}
      </span>
      <small>{tr("resultPaperCount", { count })}</small>
    </span>
  );

  const groups = [...failures].map(([reason, rows]) => {
    const retryAt = rows[0].retry_at;
    const sharedRetryAt = rows.every(item => !item.retryable && item.retry_at === retryAt) ? retryAt : undefined;
    return {
      key: `failure:${reason}`,
      label: groupLabel(reason, rows.length, sharedRetryAt),
      children: renderRows(rows, sharedRetryAt),
    };
  });
  if (added.length > 1) {
    groups.push({
      key: "added",
      label: groupLabel(tr("addedPapers"), added.length),
      children: renderRows(added),
    });
  } else {
    remaining.unshift(...added);
  }

  return (
    <div className="import-history-batch-items">
      {groups.length > 0 && (
        <Collapse
          className="import-history-result-groups"
          size="small"
          expandIconPosition="end"
          items={groups}
        />
      )}
      {remaining.length > 0 && renderRows(remaining)}
    </div>
  );
}

export default function ImportHistoryCards({
  imports,
  initialOpenBatchId,
  onRetry,
  retrying,
  onRefreshImports,
}: {
  imports: ImportItem[];
  initialOpenBatchId?: string;
  onRetry: (rows: ImportItem[]) => void;
  retrying: boolean;
  onRefreshImports?: () => Promise<void>;
}) {
  const { i18n } = useTranslation();
  const [refreshing, setRefreshing] = useState(false);
  const groups = new Map<string, { datasetId: string; name: string; created: boolean; createdAt?: string; items: ImportItem[] }>();
  for (const item of imports) {
    const group = groups.get(item.batch_id);
    if (!group) {
      groups.set(item.batch_id, {
        datasetId: item.target_dataset_id,
        name: item.target_name || tr("library"),
        created: item.created_library,
        createdAt: item.batch_created_at,
        items: [item],
      });
      continue;
    }
    group.name = item.target_name || group.name;
    group.items.push(item);
  }
  // The API sends oldest items first, which also gives untimed entries a stable order.
  const batches = [...groups].reverse().sort(([, left], [, right]) =>
    (right.createdAt ? Date.parse(right.createdAt) : 0) - (left.createdAt ? Date.parse(left.createdAt) : 0));
  const additionalStatuses = {
    needs_confirmation: "needsPaperConfirmation",
    waiting_retry: "waitingRetry",
    running: "running",
    pending: "pending",
    original_unavailable: "originalUnavailable",
    unknown: "unknown",
  };

  return (
    <div aria-live="polite" className="import-history-body">
      {imports.length > 0 && onRefreshImports && (
        <Button
          size="small"
          loading={refreshing}
          onClick={async () => {
            setRefreshing(true);
            try {
              await onRefreshImports();
            } finally {
              setRefreshing(false);
            }
          }}
        >
          {tr("refreshStatus")}
        </Button>
      )}
      {batches.length > 0 && <Collapse
        className="import-history-tasks"
        size="small"
        collapsible="header"
        defaultActiveKey={initialOpenBatchId}
        items={batches.map(([batchId, { datasetId, name, created, createdAt, items }]) => {
          const papers = [...new Map(items.map(item => [item.paper_key || item.item_id, item])).values()];
          const target = encodeURIComponent(datasetId);
          return {
            key: batchId,
            extra: <Link to={`/lib/knowledge/detail/${target}`} aria-label={name} title={tr("viewLibrary")}>
              <ExportOutlined aria-hidden />
            </Link>,
            label: <>
              <span className="import-history-task-heading">
                <span className="import-history-task-name"><strong>{name}</strong>{created && <small>{tr("createdHere")}</small>}</span>
                {createdAt && <time dateTime={createdAt}>{new Date(createdAt).toLocaleString(i18n.language, {
                  year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
                })}</time>}
              </span>
              <span className="import-history-summary">
                {tr("addedPrefix")}
                {papers.filter(row => row.status === "added").length}
                {tr("existsPrefix")}
                {papers.filter(row => row.status === "exists").length}
                {tr("failedPrefix")}
                {papers.filter(row => row.status === "failed").length}
                {Object.entries(additionalStatuses).map(([status, label]) => {
                  const count = papers.filter(row => row.status === status).length;
                  return count > 0 ? <span key={status}> · {tr(label)} {count}</span> : null;
                })}
              </span>
            </>,
            children: <ImportBatchItems
              items={items}
              target={target}
              language={i18n.language}
              onRetry={onRetry}
              retrying={retrying}
            />,
          };
        })}
      />}
    </div>
  );
}
