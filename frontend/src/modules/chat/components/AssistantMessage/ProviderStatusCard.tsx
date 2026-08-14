import type {
  ModelRetry,
  ModelTransportError,
  ProviderStatus,
} from "@/modules/chat/utils/providerStatus";
import {
  formatProviderDetail,
  shouldShowProviderDiagnostic,
} from "@/modules/chat/utils/providerStatus";

export default function ProviderStatusCard({
  status,
  retry,
  transportError,
}: {
  status?: ProviderStatus;
  retry?: ModelRetry;
  transportError?: ModelTransportError;
}) {
  const showDiagnostic = shouldShowProviderDiagnostic(status, transportError);
  const detail = status?.error_body || transportError?.error_message;
  const titleParts: string[] = [];
  const providerEvent = status || transportError;
  if (providerEvent) {
    titleParts.push(`HTTP ${providerEvent.http_status ?? "null"}`);
    titleParts.push(`finish_reason: ${providerEvent.finish_reason ?? "null"}`);
  }
  if (transportError) {
    titleParts.push(transportError.error_type);
  }

  if (!retry && !showDiagnostic) return null;

  return (
    <div className="provider-state-wrap">
      {retry && (
        <div className="provider-retry-status" role="status">
          网络波动，正在重试 {retry.retry_index}/{retry.max_retries}
          {retry.delay_ms > 0 ? `（${retry.delay_ms} ms）` : ""}
        </div>
      )}
      {showDiagnostic && (
        <section className="provider-diagnostic-card" aria-label="Provider status">
          <div className="provider-diagnostic-title">{titleParts.join(" · ")}</div>
          <details>
            <summary>查看原始响应</summary>
            <pre>{formatProviderDetail(detail)}</pre>
          </details>
        </section>
      )}
    </div>
  );
}
