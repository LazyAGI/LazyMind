// Only known server-owned codes are rendered; upstream text never becomes UI copy.
const supportedCodes = new Set([
  "invalid_configuration", "invalid_name", "invalid_request", "invalid_response",
  "invalid_word", "request_cancelled", "request_failed", "response_failed",
  "response_too_large", "study_word_not_added", "too_many_wordbooks",
  "upstream_rejected", "word_not_found", "invalid_token", "rate_limited",
]);
export function cloudErrorMessage(error: string, translate: (key: string) => string): string {
  const code = error.startsWith("cloud vocabulary: ") ? error.slice("cloud vocabulary: ".length) : "";
  if (supportedCodes.has(code)) return translate(`vocabularyCloud.errors.${code}`);
  if (error === "vocabulary cloud credentials unavailable") {
    return translate("vocabularyCloud.errors.credentials_unavailable");
  }
  return translate("vocabularyCloud.errors.sync_failed");
}
