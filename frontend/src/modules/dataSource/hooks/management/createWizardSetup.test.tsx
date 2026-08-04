import { act } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn(), put: vi.fn() },
  getLocalizedErrorMessage: (error: unknown) => `localized:${(error as Error)?.message || error}`,
  localizeErrorCode: (code?: string) => `localized-code:${code}`,
}));

const getSourceMock = vi.fn();

vi.mock("../../api/clients", () => ({
  dataSourceScanApi: {
    getSource: (...args: unknown[]) => getSourceMock(...args),
  },
}));

const clearFeishuDataSourceWizardDraftMock = vi.fn();
const peekFeishuDataSourceWizardDraftMock = vi.fn();

vi.mock("@/modules/dataSource/common/feishuOAuth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/modules/dataSource/common/feishuOAuth")>();
  return {
    ...actual,
    clearFeishuDataSourceWizardDraft: (...args: unknown[]) =>
      clearFeishuDataSourceWizardDraftMock(...args),
    peekFeishuDataSourceWizardDraft: (...args: unknown[]) =>
      peekFeishuDataSourceWizardDraftMock(...args),
  };
});

import { createWizardSetup } from "./createWizardSetup";
import type { ManagementContext } from "./context";

const t = ((key: string) => key) as TFunction;

function makeForm(overrides: Record<string, unknown> = {}) {
  return {
    resetFields: vi.fn(),
    setFieldsValue: vi.fn(),
    getFieldsValue: vi.fn(() => ({})),
    ...overrides,
  };
}

function makeContext(overrides: Partial<ManagementContext> = {}): ManagementContext {
  const base = {
    t,
    form: makeForm(),
    feishuSetupForm: makeForm({ validateFields: vi.fn().mockResolvedValue({ appId: "app-1", appSecret: "secret-1", name: "My App" }) }),
    setWizardMode: vi.fn(),
    setWizardStep: vi.fn(),
    setWizardOpen: vi.fn(),
    setSelectedType: vi.fn(),
    setEditingId: vi.fn(),
    setCreateProviderModalOpen: vi.fn(),
    setAuthSelectModalOpen: vi.fn(),
    setAuthSelectProvider: vi.fn(),
    setOauthState: vi.fn(),
    setConnectionVerified: vi.fn(),
    setOauthConnection: vi.fn(),
    setNotionOauthConnection: vi.fn(),
    setValidatedAgentId: vi.fn(),
    setManualOauthModalOpen: vi.fn(),
    setManualOauthCallbackValue: vi.fn(),
    setManualOauthSubmitting: vi.fn(),
    setCloudSetupProvider: vi.fn(),
    setFeishuSetupIntent: vi.fn(),
    setEditingFeishuAccountId: vi.fn(),
    setFeishuSetupModalOpen: vi.fn(),
    setFeishuSetupSubmitting: vi.fn(),
    setFeishuAppSetup: vi.fn(),
    setNotionAppSetup: vi.fn(),
    resetLocalPathBrowseOptions: vi.fn(),
    resetFeishuTargetBrowseOptions: vi.fn(),
    seedFeishuTargetTree: vi.fn(),
    setSources: vi.fn(),
    feishuAuthAccounts: [],
    notionAuthAccounts: [],
    feishuAppSetup: null,
    notionAppSetup: null,
    cloudSetupProvider: "feishu",
    feishuSetupIntent: null,
    feishuSetupSubmitting: false,
    clearOauthAttempt: vi.fn(),
    upsertFeishuAuthAccount: vi.fn().mockReturnValue({ id: "acc-1" }),
    saveCloudAppCredentials: vi.fn().mockResolvedValue(undefined),
    startCloudOAuth: vi.fn().mockResolvedValue(true),
    ...overrides,
  };
  return base as unknown as ManagementContext;
}

describe("createWizardSetup", () => {
  beforeEach(() => {
    getSourceMock.mockReset();
    clearFeishuDataSourceWizardDraftMock.mockReset();
    peekFeishuDataSourceWizardDraftMock.mockReset();
  });

  it("resets the wizard form and all wizard-related state", () => {
    const form = makeForm();
    const ctx = makeContext({ form });
    const { resetWizard } = createWizardSetup(ctx);

    resetWizard();

    expect(form.resetFields).toHaveBeenCalled();
    expect(ctx.setWizardMode).toHaveBeenCalledWith("create");
    expect(ctx.setSelectedType).toHaveBeenCalledWith(null);
    expect(ctx.resetLocalPathBrowseOptions).toHaveBeenCalled();
    expect(ctx.resetFeishuTargetBrowseOptions).toHaveBeenCalled();
  });

  it("loads a local source into the edit wizard from the server", async () => {
    getSourceMock.mockResolvedValue({
      data: {
        source: { source_id: "src-1", name: "My Source", updated_at: "2024-01-01T00:00:00Z" },
        bindings: [{ binding_id: "bind-1", target_ref: "/root", connector_type: "local" }],
      },
    });
    const setSources = vi.fn();
    const setWizardOpen = vi.fn();
    const ctx = makeContext({ setSources, setWizardOpen });
    const { openEditWizard } = createWizardSetup(ctx);

    await act(async () => {
      await openEditWizard({
        id: "src-1",
        name: "My Source",
        type: "local",
      } as any);
    });

    expect(getSourceMock).toHaveBeenCalledWith({ sourceId: "src-1" });
    expect(setSources).toHaveBeenCalled();
    expect(setWizardOpen).toHaveBeenCalledWith(true);
  });

  it("swallows errors when loading a source for editing fails", async () => {
    getSourceMock.mockRejectedValue(new Error("boom"));
    const setWizardOpen = vi.fn();
    const ctx = makeContext({ setWizardOpen });
    const { openEditWizard } = createWizardSetup(ctx);

    await act(async () => {
      await expect(
        openEditWizard({ id: "src-1", name: "My Source", type: "local" } as any),
      ).resolves.toBeUndefined();
    });

    expect(setWizardOpen).not.toHaveBeenCalled();
  });

  it("closes the wizard and clears the pending oauth draft", () => {
    const setWizardOpen = vi.fn();
    const form = makeForm();
    const ctx = makeContext({ setWizardOpen, form });
    const { handleCloseWizard } = createWizardSetup(ctx);

    handleCloseWizard();

    expect(setWizardOpen).toHaveBeenCalledWith(false);
    expect(clearFeishuDataSourceWizardDraftMock).toHaveBeenCalled();
    expect(form.resetFields).toHaveBeenCalled();
  });

  it("applies a source type and resets connection-related state", () => {
    const setSelectedType = vi.fn();
    const setConnectionVerified = vi.fn();
    const form = makeForm();
    const ctx = makeContext({ setSelectedType, setConnectionVerified, form });
    const { applySourceType } = createWizardSetup(ctx);

    applySourceType("feishu");

    expect(setSelectedType).toHaveBeenCalledWith("feishu");
    expect(setConnectionVerified).toHaveBeenCalledWith(false);
    expect(form.setFieldsValue).toHaveBeenCalledWith(
      expect.objectContaining({ target: [], targetType: "wiki_space" }),
    );
  });

  it("opens the cloud setup modal and seeds the setup form with account values", () => {
    const feishuSetupForm = makeForm({ validateFields: vi.fn() });
    const setFeishuSetupModalOpen = vi.fn();
    const ctx = makeContext({ feishuSetupForm, setFeishuSetupModalOpen });
    const { openCloudSetupModal } = createWizardSetup(ctx);

    openCloudSetupModal("feishu", "create", {
      id: "acc-1",
      name: "My App",
      appId: "app-1",
      appSecret: "secret-1",
    } as any);

    expect(feishuSetupForm.setFieldsValue).toHaveBeenCalledWith({
      name: "My App",
      appId: "app-1",
      appSecret: "secret-1",
    });
    expect(setFeishuSetupModalOpen).toHaveBeenCalledWith(true);
  });

  it("does nothing when cancelling cloud setup while submitting", () => {
    const setFeishuSetupModalOpen = vi.fn();
    const ctx = makeContext({ feishuSetupSubmitting: true, setFeishuSetupModalOpen });
    const { handleCancelCloudSetup } = createWizardSetup(ctx);

    handleCancelCloudSetup();

    expect(setFeishuSetupModalOpen).not.toHaveBeenCalled();
  });

  it("clears feishu app setup and closes the modal when cancelling a create intent", () => {
    const setFeishuAppSetup = vi.fn();
    const setFeishuSetupModalOpen = vi.fn();
    const setSelectedType = vi.fn();
    const ctx = makeContext({
      feishuSetupIntent: "create",
      cloudSetupProvider: "feishu",
      setFeishuAppSetup,
      setFeishuSetupModalOpen,
      setSelectedType,
    });
    const { handleCancelCloudSetup } = createWizardSetup(ctx);

    handleCancelCloudSetup();

    expect(setFeishuAppSetup).toHaveBeenCalledWith(null);
    expect(setFeishuSetupModalOpen).toHaveBeenCalledWith(false);
    expect(setSelectedType).toHaveBeenCalledWith(null);
  });

  it("saves feishu setup credentials and starts the oauth flow", async () => {
    const startCloudOAuth = vi.fn().mockResolvedValue(true);
    const feishuSetupForm = makeForm({
      validateFields: vi.fn().mockResolvedValue({ appId: "app-1", appSecret: "secret-1", name: "My App" }),
    });
    const ctx = makeContext({
      feishuSetupForm,
      feishuSetupIntent: "create",
      cloudSetupProvider: "feishu",
      startCloudOAuth,
    });
    const { handleSaveFeishuSetup } = createWizardSetup(ctx);

    await act(async () => {
      await handleSaveFeishuSetup();
    });

    expect(ctx.saveCloudAppCredentials).toHaveBeenCalledWith("feishu", {
      appId: "app-1",
      appSecret: "secret-1",
    });
    expect(startCloudOAuth).toHaveBeenCalledWith(
      "feishu",
      expect.objectContaining({ setup: { appId: "app-1", appSecret: "secret-1" } }),
    );
  });

  it("shows a confirm modal when resetting the feishu credential setup", () => {
    const ctx = makeContext();
    const { handleResetFeishuSetup } = createWizardSetup(ctx);

    expect(() => handleResetFeishuSetup()).not.toThrow();
  });
});
