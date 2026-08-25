export type CloudDocumentGuideProvider =
  | "local"
  | "feishu"
  | "notion"
  | "googledrive";

const CONNECTION_SUCCESS_KEY =
  "lazymind.cloud-documents.connection-success.v1";
export const CLOUD_DOCUMENT_CONNECTION_SUCCESS_EVENT =
  "lazymind:cloud-documents:connection-success";
const CONNECTION_PROVIDERS = new Set<CloudDocumentGuideProvider>([
  "local",
  "feishu",
  "notion",
  "googledrive",
]);

export function markCloudDocumentConnectionSuccess(
  provider: CloudDocumentGuideProvider,
) {
  try {
    window.sessionStorage.setItem(CONNECTION_SUCCESS_KEY, provider);
  } catch {
    // The main page still reflects the current connection state when storage is blocked.
  }
  window.dispatchEvent(
    new CustomEvent<CloudDocumentGuideProvider>(
      CLOUD_DOCUMENT_CONNECTION_SUCCESS_EVENT,
      { detail: provider },
    ),
  );
}

export function consumeCloudDocumentConnectionSuccess() {
  try {
    const provider = window.sessionStorage.getItem(CONNECTION_SUCCESS_KEY);
    window.sessionStorage.removeItem(CONNECTION_SUCCESS_KEY);
    return CONNECTION_PROVIDERS.has(provider as CloudDocumentGuideProvider)
      ? (provider as CloudDocumentGuideProvider)
      : null;
  } catch {
    return null;
  }
}
