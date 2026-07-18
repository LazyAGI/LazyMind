function getBaseName() {
  return ((window as Window & { BASENAME?: string }).BASENAME || "").trim();
}

export function getCloudDocumentsUrl(provider?: "feishu" | "notion" | "local" | "googledrive") {
  const baseName = getBaseName().replace(/\/$/, "");
  if (provider === "feishu") {
    return `${window.location.origin}${baseName}/cloud-documents/feishu`;
  }
  if (provider === "local") {
    return `${window.location.origin}${baseName}/cloud-documents/local`;
  }
  if (provider === "googledrive") {
    return `${window.location.origin}${baseName}/cloud-documents/google-drive`;
  }
  return `${window.location.origin}${baseName}/cloud-documents`;
}

export const CLOUD_DOCUMENTS_PATH = "/cloud-documents";
export const CLOUD_DOCUMENTS_LOCAL_PATH = "/cloud-documents/local";
export const CLOUD_DOCUMENTS_FEISHU_PATH = "/cloud-documents/feishu";
export const CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH =
  "/cloud-documents/google-drive";
export const CLOUD_DOCUMENTS_FEISHU_SETUP_PATH =
  "/cloud-documents/docs/feishu-setup";
export const CLOUD_DOCUMENTS_NOTION_SETUP_PATH =
  "/cloud-documents/docs/notion-setup";
export const CLOUD_DOCUMENTS_GOOGLE_DRIVE_SETUP_PATH =
  "/cloud-documents/docs/google-drive-setup";
