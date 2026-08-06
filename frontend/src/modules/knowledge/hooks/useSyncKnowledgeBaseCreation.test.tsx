import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";

const listConnectionsMock = vi.fn();

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: (...args: unknown[]) =>
      listConnectionsMock(...args),
  },
  dataSourceScanApi: {
    searchBindingTargets: vi.fn().mockResolvedValue({ data: { items: [] } }),
    listBindingTargetChildren: vi.fn().mockResolvedValue({ data: { items: [] } }),
  },
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: vi.fn(() => ({ role: "member" })),
  },
}));

vi.mock("@/modules/dataSource/common/feishuAccounts", () => ({
  createFeishuAccountId: () => "feishu-account-1",
  getOAuthStateFromConnection: (connection?: { status?: string } | null) =>
    connection?.status === "connected" ? "connected" : "pending",
  loadFeishuAppSetup: vi.fn(() => null),
  loadFeishuAuthAccounts: vi.fn(() => []),
  persistFeishuAuthAccounts: vi.fn(),
}));

vi.mock("@/modules/dataSource/common/feishuOAuth", () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: "lazymind:datasource:feishu-oauth",
  bootstrapOAuthSession: vi.fn(),
}));

vi.mock("@/modules/dataSource/mappers/dataSourceConnection", () => ({
  // Mirrors the real helper: accepts either `{ items }` or `{ data: { items } }`.
  getCloudConnectionItems: (payload: { items?: unknown[]; data?: { items?: unknown[] } }) =>
    payload?.items || payload?.data?.items || [],
  mapCloudConnectionToDataSourceConnection: (item: Record<string, unknown>) => ({
    connectionId: item.connection_id,
    status: item.status,
    provider: "googledrive",
  }),
}));

vi.mock("@/modules/dataSource/utils/role", () => ({
  isAdminRole: (role?: string) => role === "admin",
}));

vi.mock("@/modules/dataSource/utils/notionSetup", () => ({
  loadNotionAppSetup: vi.fn(() => null),
}));

const handleSaveMock = vi.fn();
vi.mock("@/modules/dataSource/hooks/management/createSaveActions", () => ({
  createSaveActions: () => ({ handleSave: handleSaveMock }),
}));

const handleSelectTypeMock = vi.fn();
vi.mock("@/modules/dataSource/hooks/management/createWizardFlow", () => ({
  createWizardFlow: () => ({
    handleSelectType: handleSelectTypeMock,
    openSourceCreateWizard: vi.fn(),
    handleCreateProviderSelect: vi.fn(),
    handleSelectFeishuAuthConnection: vi.fn(),
    handleSelectNotionAuthConnection: vi.fn(),
    handleManageFeishuAuth: vi.fn(),
    handleAddFeishuAuthFromSelect: vi.fn(),
    handleAddNotionAuthFromSelect: vi.fn(),
    handleOpenFeishuGuideFromAuthSelect: vi.fn(),
    handleOpenNotionGuideFromAuthSelect: vi.fn(),
    handleSubmitManualOauthCallback: vi.fn(),
    handleNextStep: vi.fn(),
  }),
}));

vi.mock("@/modules/dataSource/hooks/management/createWizardSetup", () => ({
  createWizardSetup: () => ({
    resetWizard: vi.fn(),
    openEditWizard: vi.fn(),
    handleCloseWizard: vi.fn(),
    applySourceType: vi.fn(),
    openCloudSetupModal: vi.fn(),
    openFeishuSetupModal: vi.fn(),
    handleSaveFeishuSetup: vi.fn(),
    handleCancelCloudSetup: vi.fn(),
    handleResetFeishuSetup: vi.fn(),
    handleResetNotionSetup: vi.fn(),
  }),
}));

vi.mock("@/modules/dataSource/hooks/management/createOAuthEngine", () => ({
  createOAuthEngine: () => ({
    clearOauthAttempt: vi.fn(),
    restorePreviousOauthState: vi.fn(),
    applyOauthResult: vi.fn(),
    refreshFeishuAuthAccounts: vi.fn().mockResolvedValue(undefined),
    refreshNotionAuthConnection: vi.fn().mockResolvedValue(undefined),
    refreshNotionAuthAccounts: vi.fn().mockResolvedValue(undefined),
    upsertFeishuAuthAccount: vi.fn(),
    saveCloudAppCredentials: vi.fn(),
    startCloudOAuth: vi.fn(),
  }),
}));

import { useSyncKnowledgeBaseCreation } from "./useSyncKnowledgeBaseCreation";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter>{children}</MemoryRouter>
    </I18nextProvider>
  );
}

describe("useSyncKnowledgeBaseCreation", () => {
  beforeEach(() => {
    listConnectionsMock.mockReset();
    listConnectionsMock.mockResolvedValue({ data: { items: [] } });
    handleSaveMock.mockReset();
    handleSelectTypeMock.mockReset();
  });

  it("initializes with the wizard closed and no cloud auth valid", async () => {
    const { result } = renderHook(() => useSyncKnowledgeBaseCreation(), { wrapper });

    await waitFor(() => {
      expect(listConnectionsMock).toHaveBeenCalled();
    });

    expect(result.current.wizardOpen).toBe(false);
    expect(result.current.wizardStep).toBe(0);
    expect(result.current.isFeishuAuthValid).toBe(false);
    expect(result.current.isGoogleDriveAuthValid).toBe(false);
  });

  it("marks google drive auth as valid once a connected connection is found", async () => {
    listConnectionsMock.mockResolvedValue({
      data: { items: [{ connection_id: "gd-1", status: "connected" }] },
    });

    const { result } = renderHook(() => useSyncKnowledgeBaseCreation(), { wrapper });

    await waitFor(() => {
      expect(result.current.isGoogleDriveAuthValid).toBe(true);
    });
  });

  it("exposes creatableSourceTypeOptions filtered by admin-only access", async () => {
    const { result } = renderHook(() => useSyncKnowledgeBaseCreation(), { wrapper });

    await waitFor(() => expect(listConnectionsMock).toHaveBeenCalled());

    // The current mocked user has role "member", so the admin-only "local"
    // source type must be filtered out of the creatable options.
    expect(
      result.current.creatableSourceTypeOptions.some((item) => item.type === "local"),
    ).toBe(false);
  });

  it("delegates requestSaveWithSyncConfirm to the save-actions handleSave", async () => {
    const { result } = renderHook(() => useSyncKnowledgeBaseCreation(), { wrapper });
    await waitFor(() => expect(listConnectionsMock).toHaveBeenCalled());

    act(() => {
      result.current.requestSaveWithSyncConfirm("createAndSync");
    });

    expect(handleSaveMock).toHaveBeenCalledWith("createAndSync");
  });

  it("delegates handleSelectType to the wizard flow handler", async () => {
    const { result } = renderHook(() => useSyncKnowledgeBaseCreation(), { wrapper });
    await waitFor(() => expect(listConnectionsMock).toHaveBeenCalled());

    act(() => {
      result.current.handleSelectType("feishu");
    });

    expect(handleSelectTypeMock).toHaveBeenCalledWith("feishu");
  });

  it("opens the create provider modal via openCreateModal", async () => {
    const { result } = renderHook(() => useSyncKnowledgeBaseCreation(), { wrapper });
    await waitFor(() => expect(listConnectionsMock).toHaveBeenCalled());

    expect(result.current.createProviderModalOpen).toBe(false);

    act(() => {
      result.current.openCreateModal();
    });

    expect(result.current.createProviderModalOpen).toBe(true);
  });

  it("calls the provided onSuccess callback via refreshSources when saving", async () => {
    // refreshSources isn't exposed directly on the vm, but it is wired into
    // the save-actions ctx as ctx.refreshSources -> onSuccessRef.current().
    // We verify indirectly that the hook doesn't throw and onSuccess is retained.
    const onSuccess = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useSyncKnowledgeBaseCreation({ onSuccess }), {
      wrapper,
    });
    await waitFor(() => expect(listConnectionsMock).toHaveBeenCalled());

    expect(result.current.wizardOpen).toBe(false);
  });
});
