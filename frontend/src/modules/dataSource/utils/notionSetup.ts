import { NOTION_APP_SETUP_STORAGE_KEY } from "../constants/options";
import type { FeishuAppSetup } from "../constants/types";

export function loadNotionAppSetup(): FeishuAppSetup | null {
  try {
    const raw = localStorage.getItem(NOTION_APP_SETUP_STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<FeishuAppSetup>;
    const appId = typeof parsed.appId === "string" ? parsed.appId.trim() : "";
    const appSecret =
      typeof parsed.appSecret === "string" ? parsed.appSecret.trim() : "";
    if (!appId || !appSecret) {
      return null;
    }
    return { appId, appSecret };
  } catch {
    return null;
  }
}

export function persistNotionAppSetup(setup: FeishuAppSetup) {
  localStorage.setItem(NOTION_APP_SETUP_STORAGE_KEY, JSON.stringify(setup));
}

export function clearNotionAppSetup() {
  localStorage.removeItem(NOTION_APP_SETUP_STORAGE_KEY);
}
