import { describe, expect, it } from "vitest";
import { isPendingOAuthWizardDraft, shouldOpenWizardFromDraft } from "./restoreWizardDraft";
import type { FeishuDataSourceWizardDraft } from "./types";

function makeDraft(overrides: Partial<FeishuDataSourceWizardDraft> = {}): FeishuDataSourceWizardDraft {
  return {
    wizardOpen: false,
    wizardStep: 0,
    wizardMode: "create",
    selectedType: "feishu",
    editingId: null,
    oauthState: "pending",
    connectionVerified: false,
    oauthConnection: null,
    formValues: {},
    ...overrides,
  };
}

describe("isPendingOAuthWizardDraft", () => {
  it("returns false for a non-cloud provider like local", () => {
    const draft = makeDraft({ selectedType: "local" });
    expect(isPendingOAuthWizardDraft(draft)).toBe(false);
  });

  it("returns true when oauthState is explicitly waiting for a cloud provider", () => {
    const draft = makeDraft({ selectedType: "notion", oauthState: "waiting" });
    expect(isPendingOAuthWizardDraft(draft)).toBe(true);
  });

  it("returns true when connection is unverified and not connected", () => {
    const draft = makeDraft({
      connectionVerified: false,
      oauthConnection: { status: "pending" } as FeishuDataSourceWizardDraft["oauthConnection"],
    });
    expect(isPendingOAuthWizardDraft(draft)).toBe(true);
  });

  it("returns false when the connection is verified and connected", () => {
    const draft = makeDraft({
      connectionVerified: true,
      oauthConnection: { status: "connected" } as FeishuDataSourceWizardDraft["oauthConnection"],
    });
    expect(isPendingOAuthWizardDraft(draft)).toBe(false);
  });

  it("defaults oauthState to pending when missing", () => {
    const draft = makeDraft({ oauthState: undefined, connectionVerified: true, oauthConnection: { status: "connected" } as any });
    expect(isPendingOAuthWizardDraft(draft)).toBe(false);
  });
});

describe("shouldOpenWizardFromDraft", () => {
  it("opens the wizard on oauth success when openWizardAfterOAuth is true", () => {
    const draft = makeDraft({ openWizardAfterOAuth: true, wizardOpen: false });
    expect(shouldOpenWizardFromDraft(draft, true)).toBe(true);
  });

  it("falls back to wizardOpen on success when openWizardAfterOAuth is undefined", () => {
    const draft = makeDraft({ openWizardAfterOAuth: undefined, wizardOpen: true });
    expect(shouldOpenWizardFromDraft(draft, true)).toBe(true);
  });

  it("does not open the wizard while an oauth flow is still pending and it did not succeed", () => {
    const draft = makeDraft({ oauthState: "waiting", wizardOpen: true });
    expect(shouldOpenWizardFromDraft(draft, false)).toBe(false);
  });

  it("respects wizardOpen when there is no pending oauth and it did not succeed", () => {
    const draft = makeDraft({ selectedType: "local", wizardOpen: true });
    expect(shouldOpenWizardFromDraft(draft, false)).toBe(true);
  });
});
