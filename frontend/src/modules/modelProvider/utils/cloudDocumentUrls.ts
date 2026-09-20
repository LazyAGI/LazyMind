function getBaseName() {
  return ((window as Window & { BASENAME?: string }).BASENAME || "").trim();
}

export function getCloudDocumentsUrl(
  provider?: "feishu" | "notion" | "github" | "local" | "googledrive" | "gmail" | "wechat",
) {
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
  if (provider === "gmail") {
    return `${window.location.origin}${baseName}/cloud-documents/mail`;
  }
  if (provider === "wechat") {
    return `${window.location.origin}${baseName}/cloud-documents/wechat-official-account`;
  }
  return `${window.location.origin}${baseName}/cloud-documents`;
}

export const CLOUD_DOCUMENTS_PATH = "/cloud-documents";
export const CLOUD_DOCUMENTS_LOCAL_PATH = "/cloud-documents/local";
export const CLOUD_DOCUMENTS_FEISHU_PATH = "/cloud-documents/feishu";
export const CLOUD_DOCUMENTS_WECHAT_OFFICIAL_ACCOUNT_PATH =
  "/cloud-documents/wechat-official-account";
export const WECHAT_OFFICIAL_ACCOUNT_PLATFORM_URL =
  "https://developers.weixin.qq.com/console/index?tab1=business&tab2=dev";
export const CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH =
  "/cloud-documents/google-drive";
export const CLOUD_DOCUMENTS_FEISHU_SETUP_PATH =
  "/cloud-documents/docs/feishu-setup";
export const CLOUD_DOCUMENTS_WECHAT_SETUP_PATH =
  "/cloud-documents/docs/wechat-official-account-setup";
export const CLOUD_DOCUMENTS_NOTION_SETUP_PATH =
  "/cloud-documents/docs/notion-setup";
export const CLOUD_DOCUMENTS_GOOGLE_DRIVE_SETUP_PATH =
  "/cloud-documents/docs/google-drive-setup";
export const CLOUD_DOCUMENTS_MAIL_PATH = "/cloud-documents/mail";

export const CLOUD_DOCUMENTS_GITHUB_SETUP_PATH =
  "/cloud-documents/docs/github-setup";
