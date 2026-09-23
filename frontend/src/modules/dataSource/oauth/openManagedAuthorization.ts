export type ManagedAuthorizationOpenResult =
  | { ok: true }
  | { ok: false; reason: "invalid" | "blocked" };

type ManagedDesktopBridge = {
  openManagedProviderAuthorization?: (url: string) => Promise<unknown> | unknown;
  openFeishuCLIAuthorization?: (url: string) => Promise<unknown> | unknown;
};

export type ReservedManagedAuthorizationPopup = Window | null | undefined;

export function reserveManagedAuthorizationPopup(): ReservedManagedAuthorizationPopup {
  if (typeof window === "undefined") {
    return undefined;
  }
  const electron = (window as Window & { lazymindDesktop?: ManagedDesktopBridge })
    .lazymindDesktop;
  if (electron?.openManagedProviderAuthorization || electron?.openFeishuCLIAuthorization) {
    return undefined;
  }
  const popup = window.open("about:blank", "_blank");
  if (!popup) {
    return null;
  }
  try {
    popup.opener = null;
    return popup;
  } catch {
    popup.close();
    return null;
  }
}

export async function openFeishuCLIAuthorization(
  verificationUrl: string,
  popup?: ReservedManagedAuthorizationPopup,
): Promise<ManagedAuthorizationOpenResult> {
  if (typeof window === "undefined" || verificationUrl.length > 4096) {
    closeManagedAuthorizationPopup(popup);
    return { ok: false, reason: "blocked" };
  }
  let target: URL;
  try {
    target = new URL(verificationUrl);
  } catch {
    closeManagedAuthorizationPopup(popup);
    return { ok: false, reason: "invalid" };
  }
  if (
    target.protocol !== "https:" ||
    target.username ||
    target.password ||
    target.port ||
    target.hash ||
    !(
      (["accounts.feishu.cn", "accounts.larksuite.com"].includes(target.hostname) &&
        target.pathname === "/oauth/v1/device/verify") ||
      (["open.feishu.cn", "open.larksuite.com"].includes(target.hostname) &&
        target.pathname === "/page/cli")
    )
  ) {
    closeManagedAuthorizationPopup(popup);
    return { ok: false, reason: "invalid" };
  }
  const electron = (window as Window & { lazymindDesktop?: ManagedDesktopBridge })
    .lazymindDesktop;
  if (electron?.openFeishuCLIAuthorization) {
    closeManagedAuthorizationPopup(popup);
    await electron.openFeishuCLIAuthorization(target.toString());
    return { ok: true };
  }
  if (popup !== undefined) {
    if (!popup || popup.closed) {
      return { ok: false, reason: "blocked" };
    }
    try {
      popup.location.replace(target.toString());
      return { ok: true };
    } catch {
      closeManagedAuthorizationPopup(popup);
      return { ok: false, reason: "blocked" };
    }
  }
  const opened = window.open(target.toString(), "_blank", "noopener,noreferrer");
  return opened ? { ok: true } : { ok: false, reason: "blocked" };
}

export function closeManagedAuthorizationPopup(
  popup: ReservedManagedAuthorizationPopup,
): void {
  if (popup && !popup.closed) {
    popup.close();
  }
}

export async function openManagedAuthorization(
  authorization_start_url: string,
  popup?: ReservedManagedAuthorizationPopup,
): Promise<ManagedAuthorizationOpenResult> {
  if (typeof window === "undefined") {
    closeManagedAuthorizationPopup(popup);
    return { ok: false, reason: "blocked" };
  }
  let target: URL;
  try {
    target = new URL(authorization_start_url);
  } catch {
    closeManagedAuthorizationPopup(popup);
    return { ok: false, reason: "invalid" };
  }
  const loopback =
    target.protocol === "http:" &&
    ["localhost", "127.0.0.1", "::1"].includes(target.hostname);
  if (
    (target.protocol !== "https:" && !loopback) ||
    target.username ||
    target.password ||
    target.search ||
    target.hash ||
    !/^\/v1\/provider-connections\/authorize\/[A-Za-z0-9_-]{43,256}$/.test(
      target.pathname,
    )
  ) {
    closeManagedAuthorizationPopup(popup);
    return { ok: false, reason: "invalid" };
  }
  const electron = (window as Window & { lazymindDesktop?: ManagedDesktopBridge })
    .lazymindDesktop;
  if (electron?.openManagedProviderAuthorization) {
    closeManagedAuthorizationPopup(popup);
    await electron.openManagedProviderAuthorization(target.toString());
    return { ok: true };
  }
  if (popup !== undefined) {
    if (!popup || popup.closed) {
      return { ok: false, reason: "blocked" };
    }
    try {
      popup.location.replace(target.toString());
      return { ok: true };
    } catch {
      closeManagedAuthorizationPopup(popup);
      return { ok: false, reason: "blocked" };
    }
  }
  const opened = window.open(target.toString(), "_blank", "noopener,noreferrer");
  return opened ? { ok: true } : { ok: false, reason: "blocked" };
}
