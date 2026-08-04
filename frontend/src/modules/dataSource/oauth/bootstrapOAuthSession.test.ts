import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { bootstrapOAuthSession } from "./bootstrapOAuthSession";
import { saveCloudDataSourceOAuthResult, saveFeishuDataSourceWizardDraft } from "./storage";
import type { FeishuDataSourceWizardDraft } from "./types";

function makeDraft(overrides: Partial<FeishuDataSourceWizardDraft> = {}): FeishuDataSourceWizardDraft {
  return {
    wizardOpen: true,
    wizardStep: 1,
    wizardMode: "create",
    selectedType: "feishu",
    editingId: null,
    validatedAgentId: null,
    oauthState: "waiting",
    connectionVerified: false,
    oauthConnection: null,
    formValues: { knowledgeBase: "KB" },
    ...overrides,
  };
}

function makeOptions(overrides: Record<string, unknown> = {}) {
  return {
    form: { setFieldsValue: vi.fn() },
    setAuthSelectModalOpen: vi.fn(),
    setAuthSelectProvider: vi.fn(),
    setWizardMode: vi.fn(),
    setWizardOpen: vi.fn(),
    setWizardStep: vi.fn(),
    setSelectedType: vi.fn(),
    setEditingId: vi.fn(),
    setValidatedAgentId: vi.fn(),
    setOauthState: vi.fn(),
    setConnectionVerified: vi.fn(),
    setOauthConnection: vi.fn(),
    applyOauthResult: vi.fn(),
    reopenCloudSetupModal: vi.fn(),
    ...overrides,
  } as any;
}

describe("bootstrapOAuthSession", () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does nothing when there is no stored draft or oauth result", () => {
    const options = makeOptions();
    bootstrapOAuthSession(options);

    expect(options.setWizardOpen).not.toHaveBeenCalled();
    expect(options.applyOauthResult).not.toHaveBeenCalled();
  });

  it("restores wizard state from a stored draft", () => {
    saveFeishuDataSourceWizardDraft(makeDraft());
    const options = makeOptions();

    bootstrapOAuthSession(options);

    expect(options.setWizardMode).toHaveBeenCalledWith("create");
    expect(options.setWizardStep).toHaveBeenCalledWith(1);
    expect(options.setSelectedType).toHaveBeenCalledWith("feishu");
    expect(options.setOauthState).toHaveBeenCalledWith("waiting");

    vi.runAllTimers();
    expect(options.form.setFieldsValue).toHaveBeenCalledWith(
      expect.objectContaining({ knowledgeBase: "KB" }),
    );
  });

  it("applies a stored feishu oauth result after consuming the draft", () => {
    saveFeishuDataSourceWizardDraft(makeDraft({ openWizardAfterOAuth: true }));
    saveCloudDataSourceOAuthResult("feishu", {
      channel: "lazymind:datasource:feishu-oauth",
      source: "feishu-data-source",
      status: "success",
      connection: { provider: "feishu", connectionId: "conn-1", status: "connected" },
    } as never);
    const options = makeOptions();

    bootstrapOAuthSession(options);
    vi.runAllTimers();

    expect(options.applyOauthResult).toHaveBeenCalledWith(
      expect.objectContaining({ status: "success" }),
      { openWizardOnSuccess: true },
    );
  });

  it("prefers a stored notion oauth result over a feishu one when both are present", () => {
    saveFeishuDataSourceWizardDraft(makeDraft({ selectedType: "notion" }));
    saveCloudDataSourceOAuthResult("feishu", {
      channel: "lazymind:datasource:feishu-oauth",
      source: "feishu-data-source",
      status: "error",
      provider: "feishu",
      message: "denied",
    } as never);
    saveCloudDataSourceOAuthResult("notion", {
      channel: "lazymind:datasource:feishu-oauth",
      source: "feishu-data-source",
      status: "success",
      connection: { provider: "notion", connectionId: "conn-2", status: "connected" },
    } as never);
    const options = makeOptions();

    bootstrapOAuthSession(options);
    vi.runAllTimers();

    expect(options.applyOauthResult).toHaveBeenCalledWith(
      expect.objectContaining({
        status: "success",
        connection: expect.objectContaining({ provider: "notion" }),
      }),
      expect.anything(),
    );
  });

  it("reopens the cloud setup modal when the draft is pending and no result was recorded", () => {
    saveFeishuDataSourceWizardDraft(
      makeDraft({ oauthState: "waiting", connectionVerified: false, oauthConnection: null }),
    );
    const reopenCloudSetupModal = vi.fn();
    const options = makeOptions({ reopenCloudSetupModal });

    bootstrapOAuthSession(options);
    vi.runAllTimers();

    expect(reopenCloudSetupModal).toHaveBeenCalledWith("feishu");
    expect(options.applyOauthResult).not.toHaveBeenCalled();
  });

  it("does not reopen the setup modal when the draft connection is already verified", () => {
    saveFeishuDataSourceWizardDraft(
      makeDraft({
        oauthState: "connected",
        connectionVerified: true,
        oauthConnection: { provider: "feishu", connectionId: "conn-1", status: "connected" },
      }),
    );
    const reopenCloudSetupModal = vi.fn();
    const options = makeOptions({ reopenCloudSetupModal });

    bootstrapOAuthSession(options);
    vi.runAllTimers();

    expect(reopenCloudSetupModal).not.toHaveBeenCalled();
  });

  it("clamps the wizard step from the draft between 0 and 1", () => {
    saveFeishuDataSourceWizardDraft(makeDraft({ wizardStep: 5 }));
    const options = makeOptions();

    bootstrapOAuthSession(options);

    expect(options.setWizardStep).toHaveBeenCalledWith(1);
  });
});
