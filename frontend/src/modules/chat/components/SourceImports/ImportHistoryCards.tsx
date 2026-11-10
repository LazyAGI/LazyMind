import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Modal } from "antd";
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

export default function ImportHistoryCards({
  imports,
  onRetry,
  retrying,
  onRefreshImports,
}: {
  imports: ImportItem[];
  onRetry: (rows: ImportItem[]) => void;
  retrying: boolean;
  onRefreshImports?: () => Promise<void>;
}) {
  useTranslation();
  const [refreshing, setRefreshing] = useState(false);
  const groups = new Map<string, { name: string; created: boolean; items: ImportItem[] }>();
  for (const item of imports) {
    const group = groups.get(item.target_dataset_id);
    if (!group) {
      groups.set(item.target_dataset_id, {
        name: item.target_name || tr("library"), created: item.created_library, items: [item],
      });
      continue;
    }
    group.name = item.target_name || group.name;
    group.created ||= item.created_library;
    group.items.push(item);
  }

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
      {[...groups].map(([id, { name, created, items }]) => {
        const papers = [...new Map(items.map(item => [item.paper_key || item.item_id, item])).values()];
        const library = <Link to={`/lib/knowledge/detail/${encodeURIComponent(id)}`}>{name}</Link>;
        return (
          <section className="import-history-group" key={id}>
            {groups.size > 1 ? (
              <h3>{library}{created && <small>{tr("createdHere")}</small>}</h3>
            ) : created && (
              <div className="import-history-row">{tr("createdLibraryPrefix")}{library}</div>
            )}
            <div className="import-history-summary">
              {tr("addedPrefix")}
              {papers.filter((row) => row.status === "added").length}
              {tr("existsPrefix")}
              {papers.filter((row) => row.status === "exists").length}
              {tr("failedPrefix")}
              {papers.filter((row) => row.status === "failed").length}
              {(["needs_confirmation", "waiting_retry", "running", "pending"] as const).map(status => {
                const count = papers.filter(row => row.status === status).length;
                if (!count) return null;
                const label = { needs_confirmation: "needsPaperConfirmation", waiting_retry: "waitingRetry", running: "running", pending: "pending" } as const;
                return <span key={status}> · {tr(label[status])} {count}</span>;
              })}
            </div>
            {items.map((item) => (
              <div className="import-history-row" key={item.item_id}>
                {item.document_id ? (
                  <Link
                    to={`/lib/knowledge/knowledge/${encodeURIComponent(id)}/${encodeURIComponent(item.document_id)}`}
                  >
                    {item.title}
                  </Link>
                ) : (
                  item.title
                )}
                <small>{tr("importAccepted")}</small>
                <small>
                  {tr("currentStatusPrefix")}
                  {importStatusLabel(item)}
                </small>
                {item.error_message && (
                  <small
                    className={item.status === "failed" ? "result-error" : ""}
                  >
                    {item.error_message}
                  </small>
                )}
                {(item.status === "failed" ||
                  item.processing_status === "failed") && (
                  <Button
                    type="link"
                    size="small"
                    onClick={() =>
                      Modal.error({
                        title: tr("importFailed"),
                        content: `${item.title}：${item.error_message || tr("processingFailed")}`,
                      })
                    }
                  >
                    {tr("failureReason")}
                  </Button>
                )}
                {item.retryable && (
                  <Button
                    size="small"
                    loading={retrying}
                    onClick={() => onRetry([item])}
                  >
                    {tr(item.document_id ? "retryProcessing" : "retry")}
                  </Button>
                )}
              </div>
            ))}
          </section>
        );
      })}
    </div>
  );
}
