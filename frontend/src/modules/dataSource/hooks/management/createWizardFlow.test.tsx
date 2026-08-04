import { act } from "@testing-library/react";
import { message } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn(), put: vi.fn() },
  getLocalizedErrorMessage: (error: unknown) => `localized:${(error as Error)?.message || error}`,
  localizeErrorCode: (code?: string) => `localized-code:${code}`,
}));

const finishFeishuDataSourceOAuthMock = vi.fn();
const saveFeishuDataSourceWizardDraftMock = vi.fn();

vi.mock("@/modules/dataSource/common/feishuOAuth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/modules/dataSource/common/feishuOAuth")>();
  return {
    ...actual,
    finishFeishuDataSourceOAuth: (...args: unknown[]) => finishFeishuDataSourceOAuthMock(...args),
    saveFeishuDataSourceWizardDraft: (...args: unknown[]) =>
      saveFeishuDataSourceWizardDraftMock(...args),
  };
});

import { createWizardFlow } from "./createWizardFlow";
import type { ManagementContext } from "./context";
import {
  CLOUD_DOCUMENTS_FEISHU_SETUP_PATH,
  CLOUD_DOCUMENTS_NOTION_SETUP_PATH,
} from "@/modules/modelProvider/utils/cloudDocumentUrls";

const t = ((key: string) => key) as TFunction;

function makeContext(overrides: Partial<ManagementContext> = {}): ManagementContext {
  const base = {
    t,
    navigate: vi.fn(),
    form: { getFieldsValue: vi.fn(() => ({})) },
    setCreateProviderModalOpen: vi.fn(),
    setAuthSelectModalOpen: vi.fn(),
    setAuthSelectProvider: vi.fn(),
    setWizardMode: vi.fn(),
    setEditingId: vi.fn(),
    setWizardStep: vi.fn(),
    setWizardOpen: vi.fn(),
    setOauthConnection: vi.fn(),
    setNotionOauthConnection: vi.fn(),
    setOauthState: vi.fn(),
    setConnectionVerified: vi.fn(),
    setManualOauthModalOpen: vi.fn(),
    setManualOauthCallbackValue: vi.fn(),
    setManualOauthSubmitting: vi.fn(),
    canCreateLocalSource: true,
    isFeishuSetupReady: true,
    isNotionSetupReady: true,
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    feishuAppSetup: { appId: "app-1", appSecret: "secret-1" },
    notionAppSetup: { appId: "app-2", appSecret: "secret-2" },
    oauthConnection: null,
    notionOauthConnection: null,
    oauthState: "pending",
    connectionVerified: false,
    wizardStep: 0,
    wizardMode: "create",
    editingId: null,
    selectedType: null,
    manualOauthCallbackValue: "",
    applySourceType: vi.fn(),
    resetWizard: vi.fn(),
    openCloudSetupModal: vi.fn(),
    startCloudOAuth: vi.fn().mockResolvedValue(true),
    applyOauthResult: vi.fn(),
    restorePreviousOauthState: vi.fn(),
    ...overrides,
  };
  return base as unknown as ManagementContext;
}

describe("createWizardFlow", () => {
  beforeEach(() => {
    finishFeishuDataSourceOAuthMock.mockReset();
    saveFeishuDataSourceWizardDraftMock.mockReset();
  });

  it("blocks selecting the local type for a non-admin user", async () => {
    const ctx = makeContext({ canCreateLocalSource: false });
    const { handleSelectType } = createWizardFlow(ctx);

    await act(async () => {
      handleSelectType("local");
      await Promise.resolve();
    });

    expect(ctx.applySourceType).not.toHaveBeenCalled();
  });

  it("opens the cloud setup modal when selecting feishu without ready setup", () => {
    const ctx = makeContext({ isFeishuSetupReady: false });
    const { handleSelectType } = createWizardFlow(ctx);

    handleSelectType("feishu");

    expect(ctx.openCloudSetupModal).toHaveBeenCalledWith("feishu", "create");
    expect(ctx.applySourceType).not.toHaveBeenCalled();
  });

  it("applies the source type directly for a ready local type", () => {
    const ctx = makeContext();
    const { handleSelectType } = createWizardFlow(ctx);

    handleSelectType("local");

    expect(ctx.applySourceType).toHaveBeenCalledWith("local");
  });

  it("opens the create wizard for a non-cloud type from the provider select", () => {
    const setCreateProviderModalOpen = vi.fn();
    const setWizardOpen = vi.fn();
    const ctx = makeContext({ setCreateProviderModalOpen, setWizardOpen });
    const { handleCreateProviderSelect } = createWizardFlow(ctx);

    handleCreateProviderSelect("local");

    expect(setCreateProviderModalOpen).toHaveBeenCalledWith(false);
    expect(setWizardOpen).toHaveBeenCalledWith(true);
  });

  it("opens the auth select modal for feishu when an existing valid auth is present", () => {
    const setAuthSelectModalOpen = vi.fn();
    const setAuthSelectProvider = vi.fn();
    const ctx = makeContext({
      isFeishuAuthValid: true,
      setAuthSelectModalOpen,
      setAuthSelectProvider,
    });
    const { handleCreateProviderSelect } = createWizardFlow(ctx);

    handleCreateProviderSelect("feishu");

    expect(setAuthSelectProvider).toHaveBeenCalledWith("feishu");
    expect(setAuthSelectModalOpen).toHaveBeenCalledWith(true);
  });

  it("starts cloud oauth for feishu when no existing auth and setup is ready", () => {
    const startCloudOAuth = vi.fn().mockResolvedValue(true);
    const ctx = makeContext({ isFeishuAuthValid: false, startCloudOAuth });
    const { handleCreateProviderSelect } = createWizardFlow(ctx);

    handleCreateProviderSelect("feishu");

    expect(startCloudOAuth).toHaveBeenCalledWith(
      "feishu",
      expect.objectContaining({ setup: ctx.feishuAppSetup }),
    );
  });

  it("opens the cloud setup modal for feishu when setup is not ready and no auth exists", () => {
    const ctx = makeContext({ isFeishuAuthValid: false, isFeishuSetupReady: false });
    const { handleCreateProviderSelect } = createWizardFlow(ctx);

    handleCreateProviderSelect("feishu");

    expect(ctx.openCloudSetupModal).toHaveBeenCalledWith("feishu", "create");
  });

  it("selects a feishu auth connection and opens the wizard with that connection", () => {
    const setAuthSelectModalOpen = vi.fn();
    const setOauthConnection = vi.fn();
    const setWizardOpen = vi.fn();
    const ctx = makeContext({ setAuthSelectModalOpen, setOauthConnection, setWizardOpen });
    const { handleSelectFeishuAuthConnection } = createWizardFlow(ctx);

    const connection = {
      provider: "feishu" as const,
      connectionId: "conn-1",
      status: "connected" as const,
      accountName: "My Feishu",
      grantedScopes: [],
    };
    handleSelectFeishuAuthConnection(connection);

    expect(setAuthSelectModalOpen).toHaveBeenCalledWith(false);
    expect(setOauthConnection).toHaveBeenCalledWith(connection);
  });

  it("saves a draft and navigates to the feishu setup guide", () => {
    const navigate = vi.fn();
    const ctx = makeContext({ navigate });
    const { handleOpenFeishuGuideFromAuthSelect } = createWizardFlow(ctx);

    handleOpenFeishuGuideFromAuthSelect();

    expect(saveFeishuDataSourceWizardDraftMock).toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith(CLOUD_DOCUMENTS_FEISHU_SETUP_PATH);
  });

  it("saves a draft and navigates to the notion setup guide", () => {
    const navigate = vi.fn();
    const ctx = makeContext({ navigate });
    const { handleOpenNotionGuideFromAuthSelect } = createWizardFlow(ctx);

    handleOpenNotionGuideFromAuthSelect();

    expect(navigate).toHaveBeenCalledWith(CLOUD_DOCUMENTS_NOTION_SETUP_PATH);
  });

  it("warns when the manual oauth callback value cannot be parsed", async () => {
    const messageModule = await import("antd");
    const warnSpy = vi.spyOn(messageModule.message, "warning").mockImplementation(() => "" as any);

    const ctx = makeContext({ manualOauthCallbackValue: "not-a-valid-callback" });
    const { handleSubmitManualOauthCallback } = createWizardFlow(ctx);

    await handleSubmitManualOauthCallback();

    expect(warnSpy).toHaveBeenCalledWith("admin.dataSourceOauthManualCallbackInvalid");
    expect(finishFeishuDataSourceOAuthMock).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });

  it("submits a valid manual oauth callback and applies the result", async () => {
    finishFeishuDataSourceOAuthMock.mockResolvedValue({
      provider: "feishu",
      connectionId: "conn-1",
      status: "connected",
      accountName: "My Feishu",
    });

    const setManualOauthModalOpen = vi.fn();
    const setManualOauthCallbackValue = vi.fn();
    const ctx = makeContext({
      manualOauthCallbackValue: "https://cb?code=abc&state=state-1",
      setManualOauthModalOpen,
      setManualOauthCallbackValue,
    });
    const { handleSubmitManualOauthCallback } = createWizardFlow(ctx);

    await handleSubmitManualOauthCallback();

    expect(finishFeishuDataSourceOAuthMock).toHaveBeenCalledWith("abc", "state-1");
    expect(ctx.applyOauthResult).toHaveBeenCalledWith(
      expect.objectContaining({ status: "success" }),
    );
    expect(setManualOauthModalOpen).toHaveBeenCalledWith(false);
  });

  it("restores previous oauth state on a network error during manual callback submission", async () => {
    const networkError = { request: {} };
    finishFeishuDataSourceOAuthMock.mockRejectedValue(networkError);

    const ctx = makeContext({ manualOauthCallbackValue: "https://cb?code=abc&state=state-1" });
    const { handleSubmitManualOauthCallback } = createWizardFlow(ctx);

    await handleSubmitManualOauthCallback();

    expect(ctx.restorePreviousOauthState).toHaveBeenCalled();
    expect(ctx.applyOauthResult).not.toHaveBeenCalled();
  });

  it("warns when advancing from step 0 without selecting a type", async () => {
    const warnSpy = vi.spyOn(message, "warning").mockImplementation(() => "" as any);

    const ctx = makeContext({ wizardStep: 0, selectedType: null });
    const { handleNextStep } = createWizardFlow(ctx);

    await act(async () => {
      handleNextStep();
      await Promise.resolve();
    });

    expect(warnSpy).toHaveBeenCalledWith("admin.dataSourceSelectOneTypeFirst");
    warnSpy.mockRestore();
  });

  it("advances to step 1 for a local type", () => {
    const setWizardStep = vi.fn();
    const ctx = makeContext({ wizardStep: 0, selectedType: "local", setWizardStep });
    const { handleNextStep } = createWizardFlow(ctx);

    handleNextStep();

    expect(setWizardStep).toHaveBeenCalledWith(1);
  });

  it("warns and requires oauth before advancing for feishu without a connection", async () => {
    const warnSpy = vi.spyOn(message, "warning").mockImplementation(() => "" as any);

    const setWizardStep = vi.fn();
    const ctx = makeContext({
      wizardStep: 0,
      selectedType: "feishu",
      oauthConnection: null,
      setWizardStep,
    });
    const { handleNextStep } = createWizardFlow(ctx);

    await act(async () => {
      handleNextStep();
      await Promise.resolve();
    });

    expect(warnSpy).toHaveBeenCalledWith("admin.dataSourceOauthRequiredBeforeSave");
    expect(setWizardStep).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });
});
