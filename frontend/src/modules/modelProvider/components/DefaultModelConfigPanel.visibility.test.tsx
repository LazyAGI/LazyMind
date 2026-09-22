import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import DefaultModelConfigPanel from "./DefaultModelConfigPanel";

const mocks = vi.hoisted(() => ({
  runtimeFeatures: {
    hideUserGroupSurfaces: true,
  },
  getProviders: vi.fn(),
  saveModels: vi.fn(),
  translate: (key: string) => key,
  getSelectedModels: vi.fn(),
  getSelectedProviders: vi.fn(),
}));

vi.mock("antd", async (importOriginal) => {
  const actual = await importOriginal<typeof import("antd")>();
  const Select = ({ id, onChange }: { id?: string; onChange: (value: string) => void }) => (
    <button aria-label={id} onClick={() => onChange("own:provider:group:model")}>Select</button>
  );
  Select.Option = actual.Select.Option;
  return { ...actual, Select };
});

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: mocks.translate,
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

vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: vi.fn().mockResolvedValue({ state: "signed_out" }),
	isCloudBusinessAvailable: () => false,
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersSelectedModelsPut: mocks.saveModels,
    apiCoreModelProvidersModelsGet: vi.fn().mockResolvedValue({ data: { models: [{
      id: "model", name: "Demo model", provider_name: "Demo",
      user_model_provider_id: "provider", user_model_provider_group_id: "group", group_name: "Demo group",
    }] } }),
    apiCoreModelProvidersSelectedModelsGet: mocks.getSelectedModels,
    apiCoreModelProvidersSelectedProvidersGet: mocks.getSelectedProviders,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));

function renderPanel(highlightTarget?: "image_generator", onHighlightResolved = vi.fn()) {
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
      highlightTarget={highlightTarget}
      onHighlightResolved={onHighlightResolved}
    />,
  );
}

describe("DefaultModelConfigPanel collaboration visibility", () => {
  beforeEach(() => {
    mocks.runtimeFeatures.hideUserGroupSurfaces = true;
    mocks.saveModels.mockReset().mockResolvedValue({ data: { selections: [] } });
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
    mocks.getSelectedModels.mockResolvedValue({ data: { selections: [{
      model_key: "llm", model_id: "model", name: "Demo model", provider_name: "Demo",
      user_model_provider_id: "provider", user_model_provider_group_id: "group", group_name: "Demo group",
    }] } });
    renderPanel();

    expect(await screen.findByRole("switch")).toBeInTheDocument();
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());
  });

  it("highlights the requested model capability", async () => {
    Object.defineProperty(Element.prototype, "scrollIntoView", {
      configurable: true,
      value: vi.fn(),
    });
    const { container } = renderPanel("image_generator");

    await waitFor(() => {
      expect(
        container.querySelector(
          ".model-provider-default-row.is-config-highlighted",
        ),
      ).toBeInTheDocument();
    });
  });
  it("clears the target highlight only after a successful model selection save", async () => {
    let finishSave!: (value: unknown) => void;
    mocks.saveModels.mockImplementation(() => new Promise((resolve) => { finishSave = resolve; }));
    const resolved = vi.fn();
    renderPanel("image_generator", resolved);
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());
    fireEvent.click(await screen.findByRole("button", { name: "model-provider-image_generator" }));
    expect(resolved).not.toHaveBeenCalled();
    finishSave({ data: { selections: [{ model_key: "text2image", model_id: "model", availability: "available" }] } });
    await waitFor(() => expect(resolved).toHaveBeenCalledTimes(1));
  });

  it("keeps the target highlight when saving fails", async () => {
    mocks.saveModels.mockRejectedValue(new Error("save failed"));
    const resolved = vi.fn();
    renderPanel("image_generator", resolved);
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());
    fireEvent.click(await screen.findByRole("button", { name: "model-provider-image_generator" }));
    await waitFor(() => expect(mocks.saveModels).toHaveBeenCalled());
    expect(resolved).not.toHaveBeenCalled();
  });

});
