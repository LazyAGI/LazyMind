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

const actionCodes: Record<string,string> = {
  "2005300":"invalid_configuration", "2005301":"invalid_token",
  "2005302":"credentials_unavailable", "2005303":"connection_unavailable",
  "2005304":"rate_limited", "2005305":"service_unavailable",
  "2005306":"word_not_found", "2005307":"study_word_not_added", "2005308":"storage_failed",
};
export function cloudActionErrorMessage(error: unknown, translate:(key:string)=>string):string {
  const value=error as {response?:{data?:{code?:unknown}};code?:unknown} | null;
  const code=value?.response?.data?.code;
  const key=(typeof code==="number" || typeof code==="string") && Object.prototype.hasOwnProperty.call(actionCodes,String(code)) ? actionCodes[String(code)] : undefined;
  if(key)return translate(`vocabularyCloud.errors.${key}`);
  if(typeof value?.code==="string" && ["ERR_NETWORK","ECONNABORTED","ETIMEDOUT"].includes(value.code))return translate("vocabularyCloud.errors.request_failed");
  return translate("vocabularyCloud.actionFailed");
}
