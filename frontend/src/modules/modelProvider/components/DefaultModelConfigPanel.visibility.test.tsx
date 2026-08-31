import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import DefaultModelConfigPanel from "./DefaultModelConfigPanel";

const mocks = vi.hoisted(() => ({
  runtimeFeatures: {
    hideUserGroupSurfaces: true,
  },
  getProviders: vi.fn(),
  getSelectedModels: vi.fn(),
  getSelectedProviders: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string) => key,
  }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: () => ({ role: "system-admin" }),
  },
}));

vi.mock("@/hooks/useModelFeatures", () => ({
  useModelFeatures: () => ({
    status: "ready",
    features: { image_embed_enabled: true },
  }),
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: mocks.runtimeFeatures,
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersSelectedModelsGet: mocks.getSelectedModels,
    apiCoreModelProvidersSelectedProvidersGet: mocks.getSelectedProviders,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));

function renderPanel() {
  return render(
    <DefaultModelConfigPanel
      cloudServiceSetupStates={{
        cloudParsing: "empty",
        searchEngine: "empty",
      }}
      modelProviderSetupState="ready"
      onConfigureCloudService={vi.fn()}
      onConfigureProviders={vi.fn()}
      onModelSelectionChanged={vi.fn()}
      onRetrySetup={vi.fn()}
    />,
  );
}

describe("DefaultModelConfigPanel collaboration visibility", () => {
  beforeEach(() => {
    mocks.runtimeFeatures.hideUserGroupSurfaces = true;
    mocks.getProviders.mockReset().mockResolvedValue({ data: { providers: [] } });
    mocks.getSelectedModels.mockReset().mockResolvedValue({ data: { selections: [] } });
    mocks.getSelectedProviders.mockReset().mockResolvedValue({ data: { selections: [] } });
  });

  it("hides model sharing controls when user/group surfaces are disabled", async () => {
    renderPanel();

    expect(screen.queryAllByRole("switch")).toHaveLength(0);
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());
  });

  it("keeps model sharing controls for cloud administrators", async () => {
    mocks.runtimeFeatures.hideUserGroupSurfaces = false;
    renderPanel();

    expect(screen.getAllByRole("switch").length).toBeGreaterThan(0);
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());
  });
});
