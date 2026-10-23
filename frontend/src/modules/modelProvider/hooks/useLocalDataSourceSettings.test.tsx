import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  listSources: vi.fn(),
  listBindingChatSettings: vi.fn(),
  ragAvailability: "missing",
}));

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) },
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceScanApi: {
    listSources: mocks.listSources,
    listBindingChatSettings: mocks.listBindingChatSettings,
  },
}));

vi.mock("../contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({
    availability: mocks.ragAvailability,
    refresh: vi.fn(),
  }),
}));

import { useLocalDataSourceSettings } from "./useLocalDataSourceSettings";

function wrapper({ children }: { children: ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>;
}

describe("useLocalDataSourceSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.ragAvailability = "missing";
    mocks.listSources.mockResolvedValue({ data: { items: [] } });
    mocks.listBindingChatSettings.mockResolvedValue({ data: { sources: [] } });
  });

  it("does not contact the scan service until the RAG component is available", async () => {
    const { result, rerender } = renderHook(useLocalDataSourceSettings, {
      wrapper,
    });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canCreateLocalSource).toBe(false);
    expect(mocks.listSources).not.toHaveBeenCalled();
    expect(mocks.listBindingChatSettings).not.toHaveBeenCalled();

    mocks.ragAvailability = "ready";
    rerender();

    await waitFor(() => expect(mocks.listSources).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(mocks.listBindingChatSettings).toHaveBeenCalledTimes(1),
    );
    expect(result.current.canCreateLocalSource).toBe(true);
  });
});
