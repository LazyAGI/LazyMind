import type { BrowserExtensionTarget } from "../api/systemDependencies";

export type BrowserExtensionTargetID = "chrome" | "edge";

export const DEFAULT_BROWSER_EXTENSION_TARGETS: BrowserExtensionTarget[] = [
  { id: "chrome", name: "Google Chrome", settingsUrl: "chrome://extensions" },
  { id: "edge", name: "Microsoft Edge", settingsUrl: "edge://extensions" },
];

export function detectPreferredBrowserExtensionTarget(
  userAgent = typeof navigator === "undefined" ? "" : navigator.userAgent,
): BrowserExtensionTargetID {
  return /Edg(?:A|iOS)?\//i.test(userAgent) ? "edge" : "chrome";
}

export function resolveBrowserExtensionTargets(
  targets?: BrowserExtensionTarget[],
): BrowserExtensionTarget[] {
  const supported = (targets || []).filter((target) => (
    (target.id === "chrome" || target.id === "edge")
    && Boolean(target.name?.trim())
    && Boolean(target.settingsUrl?.trim())
  ));
  return supported.length > 0 ? supported : DEFAULT_BROWSER_EXTENSION_TARGETS;
}
