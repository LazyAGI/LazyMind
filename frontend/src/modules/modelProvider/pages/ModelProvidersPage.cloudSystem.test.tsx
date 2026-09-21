import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getProvidersWithGroups: vi.fn(),
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
