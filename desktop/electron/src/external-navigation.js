const EXTERNAL_PROTOCOLS = new Set(["http:", "https:", "mailto:"]);

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
    (target.protocol === "cursor:" &&
      target.hostname === "anysphere.cursor-deeplink" &&
      target.pathname === "/mcp/install")
  ));
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
};
