export interface ProviderStatus {
  model_call_id: string;
  http_status: number | null;
  finish_reason: string | null;
  error_body?: string;
}

export interface ModelRetry {
  model_call_id: string;
  retry_index: number;
  max_retries: number;
  delay_ms: number;
}

export interface ModelTransportError {
  model_call_id: string;
  http_status: number | null;
  finish_reason: string | null;
  error_type: string;
  error_message?: string;
}

export interface ProviderEventState {
  provider_status?: ProviderStatus;
  model_retry?: ModelRetry;
  model_transport_error?: ModelTransportError;
}

export function nextProviderEventState(
  previous: ProviderEventState,
  result: Record<string, any>,
): ProviderEventState {
  const finalEvent =
    !!result.finish_reason &&
    result.finish_reason !== "FINISH_REASON_UNSPECIFIED";
  const providerFinalEvent =
    !!result.provider_status || !!result.model_transport_error;
  const hasNewContent = !!(result.delta || result.reasoning_content);
  return {
    provider_status: result.provider_status ?? previous.provider_status,
    model_retry:
      result.model_retry ??
      (hasNewContent || finalEvent || providerFinalEvent
        ? undefined
        : previous.model_retry),
    model_transport_error:
      result.model_transport_error ?? previous.model_transport_error,
  };
}

export function shouldShowProviderDiagnostic(
  status?: ProviderStatus,
  transportError?: ModelTransportError,
) {
  if (transportError) return true;
  if (!status) return false;
  if (status.http_status !== 200) return true;
  if (!status.finish_reason) return true;
  return !["stop", "tool_calls"].includes(status.finish_reason);
}

export function formatProviderDetail(value?: string) {
  if (!value) return "原始响应仅在错误发生时可用。";
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}
