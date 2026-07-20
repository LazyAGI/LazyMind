import { loadPendingCloudOAuthSession } from "./storage";
import type { CloudDataSourceProvider } from "./types";
import { getCloudDocumentsUrl } from "@/modules/modelProvider/utils/cloudDocumentUrls";

function getBaseName() {
  return ((window as Window & { BASENAME?: string }).BASENAME || "").trim();
}

export function getAppUrl(path: string) {
  const baseName = getBaseName().replace(/\/$/, "");
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `${window.location.origin}${baseName}${normalizedPath}`;
}

export function normalizeSameOriginReturnUrl(value?: string) {
  const fallbackUrl = getDataSourceManagementUrl();

  if (!value) {
    return fallbackUrl;
  }

  try {
    const url = new URL(value, window.location.href);
    if (url.origin !== window.location.origin) {
      return fallbackUrl;
    }

    if (/\/oauth\/(feishu|notion|googledrive)(\/data-source)?\/callback$/.test(url.pathname)) {
      return fallbackUrl;
    }

    return url.href;
  } catch {
    return fallbackUrl;
  }
}

export function getFeishuDataSourceCallbackUrl() {
  return getAppUrl("/oauth/feishu/callback");
}

export function getCloudDataSourceCallbackUrl(provider: CloudDataSourceProvider) {
  if (provider === "feishu") {
    return getFeishuDataSourceCallbackUrl();
  }
  return getAppUrl(`/oauth/${provider}/data-source/callback`);
}

export function getDataSourceManagementUrl(provider: CloudDataSourceProvider = "feishu") {
  return getCloudDocumentsUrl(provider);
}

export function getFeishuDataSourceOAuthReturnUrl(state?: string | null) {
  return getCloudDataSourceOAuthReturnUrl("feishu", state);
}

export function getCloudDataSourceOAuthReturnUrl(
  provider: CloudDataSourceProvider,
  state?: string | null,
) {
  if (!state) {
    return getDataSourceManagementUrl(provider);
  }

  const pending = loadPendingCloudOAuthSession(provider, state);
  return normalizeSameOriginReturnUrl(pending?.returnUrl);
}

export function openCenteredPopup(url: string, title: string) {
  const width = 560;
  const height = 760;
  const dualScreenLeft =
    window.screenLeft !== undefined ? window.screenLeft : window.screenX;
  const dualScreenTop =
    window.screenTop !== undefined ? window.screenTop : window.screenY;
  const viewportWidth = window.innerWidth || document.documentElement.clientWidth;
  const viewportHeight =
    window.innerHeight || document.documentElement.clientHeight;
  const left = Math.max(0, dualScreenLeft + (viewportWidth - width) / 2);
  const top = Math.max(0, dualScreenTop + (viewportHeight - height) / 2);

  return window.open(
    url,
    title,
    [
      `width=${width}`,
      `height=${height}`,
      `left=${Math.round(left)}`,
      `top=${Math.round(top)}`,
      "resizable=yes",
      "scrollbars=yes",
    ].join(","),
  );
}
