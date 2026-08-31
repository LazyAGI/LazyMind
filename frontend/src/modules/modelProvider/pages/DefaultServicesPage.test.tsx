import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import DefaultServicesPage from "./DefaultServicesPage";

const mocks = vi.hoisted(() => ({
  listModels: vi.fn(),
  listProviderGroups: vi.fn(),
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersModelsGet: mocks.listModels,
    apiCoreModelProvidersProviderGroupsGet: mocks.listProviderGroups,
  },
  unwrapModelProviderData: (data: unknown) => data,
}));

vi.mock("../components/DefaultModelConfigPanel", () => ({
  default: ({ modelProviderSetupState }: { modelProviderSetupState: string }) => (
    <div data-testid="model-provider-setup-state">{modelProviderSetupState}</div>
  ),
}));

describe("DefaultServicesPage", () => {
  beforeEach(() => {
    mocks.listModels.mockReset();
    mocks.listProviderGroups.mockReset();
    mocks.listProviderGroups.mockResolvedValue({ data: { groups: [] } });
  });

  it("allows configuration when any verified provider model exists without an LLM", async () => {
    mocks.listModels.mockResolvedValue({
      data: {
        models: [{ id: "model-multimodal", model_type: "multimodal_embedding" }],
      },
    });

    render(
      <DefaultServicesPage
        onConfigureCloudService={vi.fn()}
        onConfigureProviders={vi.fn()}
        onModelSelectionChanged={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("ready");
    });
    expect(mocks.listModels).toHaveBeenCalledWith(
      {},
      expect.objectContaining({ silentError: true }),
    );
  });

  it("supports a backend that still requires model_type during a rolling update", async () => {
    mocks.listModels.mockImplementation(({ modelType }: { modelType?: string } = {}) => {
      if (!modelType) return Promise.reject(new Error("model_type is required"));
      return Promise.resolve({
        data: {
          models: modelType === "cross_modal_embed"
            ? [{ id: "model-multimodal", model_type: "multimodal_embedding" }]
            : [],
        },
      });
    });

    render(
      <DefaultServicesPage
        onConfigureCloudService={vi.fn()}
        onConfigureProviders={vi.fn()}
        onModelSelectionChanged={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("ready");
    });
    expect(mocks.listModels).toHaveBeenCalledWith(
      {},
      expect.objectContaining({ silentError: true }),
    );
    expect(mocks.listModels).toHaveBeenCalledWith(
      { modelType: "cross_modal_embed" },
      expect.objectContaining({ silentError: true }),
    );
  });
});
