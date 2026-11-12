import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  listSources: vi.fn(),
  listBindingChatSettings: vi.fn(),
  ragAvailability: "missing",
  role: "system-admin",
}));

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: mocks.role }) },
}));
vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceScanApi: {
    listSources: mocks.listSources,
    listBindingChatSettings: mocks.listBindingChatSettings,
  },
}));
vi.mock("../contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: mocks.ragAvailability, refresh: vi.fn() }),
}));

import { useLocalDataSourceSettings } from "./useLocalDataSourceSettings";

function wrapper({ children }: { children: ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>;
}

describe("local source settings without RAG", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.ragAvailability = "missing";
    mocks.role = "system-admin";
    mocks.listSources.mockResolvedValue({ data: { items: [] } });
    mocks.listBindingChatSettings.mockResolvedValue({ data: { sources: [] } });
  });

  it.each(["missing", "checking", "error"])("loads source settings while RAG is %s", async (availability) => {
    mocks.ragAvailability = availability;
    const { result } = renderHook(useLocalDataSourceSettings, { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(mocks.listSources).toHaveBeenCalledWith({ page: 1, pageSize: 200 });
    expect(mocks.listBindingChatSettings).toHaveBeenCalledTimes(1);
    expect(result.current.canCreateLocalSource).toBe(true);
  });

  it("keeps local source creation restricted to administrators", async () => {
    mocks.role = "user";
    const { result } = renderHook(useLocalDataSourceSettings, { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canCreateLocalSource).toBe(false);
  });

  it("reports a real source settings failure even when RAG is missing", async () => {
    mocks.listBindingChatSettings.mockRejectedValue(new Error("scan unavailable"));
    const { result } = renderHook(useLocalDataSourceSettings, { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.chatSettingsLoadFailed).toBe(true);
  });
});
