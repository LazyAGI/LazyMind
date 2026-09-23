import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudSession } from "@/runtime/cloud/session";

const mocks = vi.hoisted(() => ({
  session: vi.fn(), cli: vi.fn(), managed: vi.fn(), availability: vi.fn(),
  t: (key: string) => key,
}));
vi.mock("react-i18next", async (original) => ({
  ...await original<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: mocks.t }),
}));
vi.mock("@/runtime/cloud/session", async (original) => ({
  ...await original<typeof import("@/runtime/cloud/session")>(),
  getCloudSession: mocks.session,
}));
vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: async () => ({ data: { data: { items: [] } } }),
    getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet: async () => ({ data: { secret_configured: false } }),
  },
}));
vi.mock("./useFeishuOAuthFlow", () => ({ useFeishuOAuthFlow: () => ({ clearOauthAttempt: vi.fn() }) }));
vi.mock("./useLocalDataSourceSettings", () => ({
  useLocalDataSourceSettings: () => ({ loading: false, canCreateLocalSource: false, localSourceCount: 0 }),
}));
vi.mock("../utils/cloudDocumentOnboarding", () => ({ markCloudDocumentConnectionSuccess: vi.fn() }));
vi.mock("@/modules/dataSource/hooks/management/createOAuthEngine", () => ({
  startFeishuCLISession: mocks.cli,
  createOAuthEngine: (ctx: { cloudManagedOAuthAvailable?: boolean }) => ({
    refreshFeishuAuthAccounts: async () => {},
    refreshNotionAuthConnection: async () => {},
    refreshNotionAuthAccounts: async () => {},
    clearOauthAttempt: vi.fn(),
    startCloudOAuth: (...args: unknown[]) => {
      mocks.availability(ctx.cloudManagedOAuthAvailable);
      return mocks.managed(...args);
    },
  }),
}));
import { useFeishuAccounts } from "./useFeishuAccounts";
import { useCloudDocumentProviders } from "./useCloudDocumentProviders";

function wrapper({ children }: { children: ReactNode }) { return <MemoryRouter>{children}</MemoryRouter>; }
const reachable: CloudSession = { configured: true, state: "signed_in", reachability: "reachable" };
const cases: Array<{ name: string; session: CloudSession | null; managed: boolean }> = [
  { name: "signed out", session: { ...reachable, state: "signed_out" }, managed: false },
  { name: "signed in and reachable", session: reachable, managed: true },
  { name: "unreachable", session: { ...reachable, reachability: "unreachable" }, managed: false },
  { name: "reachability unknown", session: { ...reachable, reachability: "unknown" }, managed: false },
  { name: "not configured", session: { ...reachable, configured: false }, managed: false },
  { name: "restoring", session: { ...reachable, state: "restoring" }, managed: false },
  { name: "refreshing", session: { ...reachable, state: "refreshing" }, managed: false },
  { name: "reauth required", session: { ...reachable, state: "reauth_required" }, managed: false },
  { name: "offline", session: { ...reachable, state: "offline" }, managed: false },
  { name: "snapshot failed", session: null, managed: false },
];
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  sessionStorage.clear();
  mocks.cli.mockResolvedValue("fixture-connection");
  mocks.managed.mockResolvedValue(true);
});
afterEach(cleanup);

function setSession(session: CloudSession | null) {
  if (session) mocks.session.mockResolvedValue(session);
  else mocks.session.mockRejectedValue(new Error("fixture snapshot unavailable"));
}

describe("one account-creation entry with Cloud/BYO routing", () => {
  it.each(cases)("Feishu account page: $name", async ({ session, managed }) => {
    setSession(session);
    const { result } = renderHook(useFeishuAccounts, { wrapper });
    await act(async () => { await result.current.handleAddAccount(); });
    expect(result.current.modalOpen).toBe(!managed);
    expect(result.current.addingAccount).toBe(false);
    expect(mocks.cli).toHaveBeenCalledTimes(managed ? 1 : 0);
  });

  for (const provider of ["feishu", "notion"] as const) {
    it.each(cases)(`${provider} document entry: $name`, async ({ session, managed }) => {
      // The click must use the latest snapshot, not the state at page mount.
      setSession(managed ? { ...reachable, state: "signed_out" } : reachable);
      const { result } = renderHook(useCloudDocumentProviders, { wrapper });
      await waitFor(() => expect(result.current.loading).toBe(false));
      setSession(session);
      await act(async () => {
        if (provider === "feishu") await result.current.handleManageFeishuAuth();
        else await result.current.handleOpenNotionSetup();
      });
      expect(result.current.feishuSetupModalOpen).toBe(!managed);
      if (!managed) expect(result.current.cloudSetupProvider).toBe(provider);
      expect(mocks.cli).toHaveBeenCalledTimes(managed && provider === "feishu" ? 1 : 0);
      expect(mocks.managed).toHaveBeenCalledTimes(managed && provider === "notion" ? 1 : 0);
      if (managed && provider === "notion") expect(mocks.availability).toHaveBeenCalledWith(true);
    });
  }
});
