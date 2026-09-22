const EXTERNAL_PROTOCOLS = new Set(["http:", "https:", "mailto:"]);
const CLOUD_LOGIN_PATH = /^\/(?:zh|en)\/desktop\/authorize\/?$/;
const CLOUD_REGISTER_PATH = /^\/(?:zh|en)\/register\/?$/;
const CLOUD_TOKEN_PLAN_PATH = /^\/(?:zh|en)\/console\/?$/;
const MANAGED_PROVIDER_AUTHORIZATION_PATH = /^\/v1\/provider-connections\/authorize\/[A-Za-z0-9_-]{43,256}$/;

function parseUrl(value) {
  try {
    return new URL(value);
  } catch {
    return null;
  }
}

function isSameOrigin(url, currentUrl) {
  const target = parseUrl(url);
  const current = parseUrl(currentUrl);
  return Boolean(target && current && target.origin === current.origin);
}

function isOAuthPopup({ frameName, features = "" }) {
  return Boolean(
    frameName &&
    frameName !== "_blank" &&
    /(?:^|,)width=\d+/.test(features) &&
    /(?:^|,)height=\d+/.test(features),
  );
}

function canOpenExternally(url) {
  const target = parseUrl(url);
  return Boolean(target && (
    EXTERNAL_PROTOCOLS.has(target.protocol) ||
    (target.protocol === "obsidian:" && target.host === "open" &&
      !target.username && !target.password && !target.pathname && !target.hash &&
      [...target.searchParams.keys()].length === 1 && target.searchParams.has("path") &&
      /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(target.searchParams.get("path")) &&
      !/[\u0000-\u001f\u007f]/.test(target.searchParams.get("path"))) ||
    (target.protocol === "cursor:" &&
      target.hostname === "anysphere.cursor-deeplink" &&
      target.pathname === "/mcp/install")
  ));
}

function isTrustedCloudNavigation(value, configuredOrigin, purpose) {
  const target = parseUrl(value);
  const configured = parseUrl(configuredOrigin);
  if (!target || !configured || !isAllowedCloudProtocol(configured) || target.protocol !== configured.protocol) {
    return false;
  }
  if (target.origin !== configured.origin || target.username || target.password) {
    return false;
  }
  if (purpose === "login") {
    return CLOUD_LOGIN_PATH.test(target.pathname) && target.hash === "";
  }
  if (purpose === "register") {
    return CLOUD_REGISTER_PATH.test(target.pathname) && target.search === "" && target.hash === "";
  }
  if (purpose === "token-plan") {
    return CLOUD_TOKEN_PLAN_PATH.test(target.pathname) && target.search === "" && target.hash === "#token-plan";
  }
  if (purpose === "provider-authorization") {
    return MANAGED_PROVIDER_AUTHORIZATION_PATH.test(target.pathname) && target.search === "" && target.hash === "";
  }
  return false;
}

function isAllowedCloudProtocol(url) {
  if (url.protocol === "https:") return true;
  return url.protocol === "http:" && isLoopbackHostname(url.hostname);
}

function isTrustedFeishuCLINavigation(value) {
  const target = parseUrl(value);
  if (!target || target.protocol !== "https:" || target.username || target.password || target.port || target.hash) {
    return false;
  }
  const hostname = target.hostname.toLowerCase();
  if (new Set(["accounts.feishu.cn", "accounts.larksuite.com"]).has(hostname)) {
    return target.toString().length <= 4096 && target.pathname === "/oauth/v1/device/verify";
  }
  if (new Set(["open.feishu.cn", "open.larksuite.com"]).has(hostname)) {
    return target.toString().length <= 4096 && target.pathname === "/page/cli";
  }
  return false;
}

function isLoopbackHostname(hostname) {
  const value = String(hostname || "").toLowerCase();
  if (value === "localhost" || value === "127.0.0.1" || value === "::1") return true;
  return /^127(?:\.\d{1,3}){3}$/.test(value);
}

function installExternalNavigationHandler(webContents, openExternal, reportError = () => {}) {
  webContents.setWindowOpenHandler((details) => {
    if (isOAuthPopup(details) || isSameOrigin(details.url, webContents.getURL())) {
      return { action: "allow" };
    }

    if (canOpenExternally(details.url)) {
      void openExternal(details.url).catch(reportError);
    }
    return { action: "deny" };
  });

  webContents.on("did-create-window", (childWindow) => {
    installExternalNavigationHandler(childWindow.webContents, openExternal, reportError);
  });
}

module.exports = {
  canOpenExternally,
  installExternalNavigationHandler,
  isOAuthPopup,
  isSameOrigin,
  isTrustedFeishuCLINavigation,
  isTrustedCloudNavigation,
};
