import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getProvidersWithGroups: vi.fn(),
  getGroups: vi.fn(),
  checkGroup: vi.fn(),
  listModels: vi.fn(),
  getCredentialBackupStatus: vi.fn(),
  getCredentialRestoreDiscovery: vi.fn(),
  getCloudSession: vi.fn(),
  translate: (key: string) => key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: mocks.translate,
  }),
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdCheckPost: mocks.checkGroup,
    apiCoreModelProvidersModelsGet: mocks.listModels,
  },
  modelProvidersDefaultApi: {
    apiCoreModelProvidersModelsReadyGet: vi.fn(),
  },
  withModelProviderJsonOptions: (options: unknown) => options,
  unwrapModelProviderData: (data: unknown) => data,
  getCredentialBackupStatus: mocks.getCredentialBackupStatus,
  getCredentialRestoreDiscovery: mocks.getCredentialRestoreDiscovery,
  getCredentialRestoreOperation: vi.fn(),
  setCredentialBackupEnabled: vi.fn(),
  startCredentialRestore: vi.fn(),
  cancelCredentialRestore: vi.fn(),
}));

vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: mocks.getCloudSession,
	isCloudBusinessAvailable: (session: any) => session?.state === "signed_in" && session?.configured !== false && session?.reachability !== "unreachable",
  beginCloudLogin: vi.fn(),
}));

vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(() => undefined),
  closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(),
  openCloudTokenPlan: vi.fn(),
}));

vi.mock("../components/CredentialBackupPanel", () => ({
  CredentialBackupPanel: () => null,
}));

vi.mock("../components/CredentialRestorePanel", () => ({
  CredentialRestorePanel: () => null,
}));

vi.mock("../components/CloudSystemProviderCard", () => ({
  default: () => <div data-testid="cloud-system-provider-card">LazyMind Cloud</div>,
}));

describe("Model Provider service list", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getProviders.mockResolvedValue({ data: { providers: [] } });
    mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [] } });
    mocks.listModels.mockResolvedValue({
      data: {
        models: [{
          id: "lazymind-text-default",
          source: "cloud",
          provider_id: "lazymind-cloud",
          provider_name: "LazyMind Cloud",
          model_type: "llm",
          name: "LazyMind Text",
          availability: "available",
          read_only: true,
        }],
      },
    });
    mocks.getCloudSession.mockResolvedValue({
      configured: true,
      reachability: "reachable",
      state: "signed_in",
    });
    mocks.getCredentialBackupStatus.mockResolvedValue({
      available: false, enabled: false, backedUp: 0, pending: 0, failed: 0,
    });
    mocks.getCredentialRestoreDiscovery.mockResolvedValue({
      available: false, requiresExplicitAction: true, records: [],
    });
  });

  it("places the read-only LazyMind Cloud card in the existing Provider service list", async () => {
    render(<ModelProviderPage />);

    expect(await screen.findByTestId("cloud-system-provider-card")).toBeInTheDocument();
    await waitFor(() => expect(mocks.listModels).toHaveBeenCalled());
    expect(screen.getByText("LazyMind Cloud")).toBeInTheDocument();
  });

  it.each(["business failure", "upstream error"])(
    "keeps a failed verification pending after remount for %s",
    async (failure) => {
      const provider = { id: "fixture-provider", name: "OpenAI", base_url: "https://api.example.test/v1" };
      let verified = true;
      mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
      mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [provider] } });
      mocks.getGroups.mockImplementation(async () => ({ data: { groups: [{
        id: "fixture-group", name: "Fixture Model", user_model_provider_id: provider.id,
        base_url: "https://custom.example.test/v1", has_api_key: false, is_verified: verified,
      }] } }));
      mocks.getCloudSession.mockResolvedValue({ configured: false, state: "signed_out" });
      mocks.checkGroup.mockImplementation(async () => {
        verified = false;
        if (failure === "upstream error") {
          throw { response: { status: 502, data: { data: { success: false } } } };
        }
        return { data: { success: false } };
      });
      const onConfigurationChanged = vi.fn();
      const page = render(<ModelProviderPage onConfigurationChanged={onConfigurationChanged} />);
      fireEvent.click(await screen.findByRole("button", { name: /modelProvider.reverify/ }));
      fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "modelProvider.verify" }));

      expect(await screen.findByText("modelProvider.pendingVerify")).toBeInTheDocument();
      expect(onConfigurationChanged).toHaveBeenCalled();
      page.unmount();
      render(<ModelProviderPage />);
      expect(await screen.findByText("modelProvider.pendingVerify")).toBeInTheDocument();
      expect(screen.queryByText("modelProvider.verified")).not.toBeInTheDocument();
    },
  );

  it.each([
    {
      name: "Cloud is not configured",
      session: { configured: false, reachability: "unknown", state: "signed_out" },
    },
    {
      name: "Cloud is configured but unreachable",
      session: { configured: true, reachability: "unreachable", state: "signed_in" },
    },
    {
      name: "Cloud is reachable but signed out",
      session: { configured: true, reachability: "reachable", state: "signed_out" },
    },
  ])("keeps the Provider page local when $name", async ({ session }) => {
    mocks.getCloudSession.mockResolvedValue(session);
    render(<ModelProviderPage />);

    await waitFor(() => expect(mocks.getProviders).toHaveBeenCalled());
    await waitFor(() => expect(mocks.getCloudSession).toHaveBeenCalled());
    expect(mocks.listModels).not.toHaveBeenCalled();
    expect(mocks.getCredentialRestoreDiscovery).not.toHaveBeenCalled();
	expect(screen.queryByTestId("cloud-system-provider-card")).not.toBeInTheDocument();
  });
});
