import type { OAuthState } from "../constants/types";
import type { FeishuDataSourceWizardDraft } from "./types";

export function isPendingOAuthWizardDraft(draft: FeishuDataSourceWizardDraft): boolean {
  const oauthState = (draft.oauthState || "pending") as OAuthState;
  const isCloudProvider = draft.selectedType === "feishu" || draft.selectedType === "notion";

  if (!isCloudProvider) {
    return false;
  }

  if (oauthState === "waiting") {
    return true;
  }

  return !draft.connectionVerified && draft.oauthConnection?.status !== "connected";
}

export function shouldOpenWizardFromDraft(
  draft: FeishuDataSourceWizardDraft,
  oauthSucceeded: boolean,
): boolean {
  if (oauthSucceeded) {
    return Boolean(draft.openWizardAfterOAuth ?? draft.wizardOpen);
  }

  if (isPendingOAuthWizardDraft(draft)) {
    return false;
  }

  return Boolean(draft.wizardOpen);
}
