import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import QuickModelSettings from "./QuickModelSettings";

const mocks = vi.hoisted(() => ({
  runtimeFeatures: {
    hideUserGroupSurfaces: true,
  },
  getSelectedModels: vi.fn(),
  getModels: vi.fn(),
  saveSelectedModels: vi.fn(),
  confirm: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: unknown; onClick?: () => void }) => (
    <button type="button" onClick={onClick}>{children as string}</button>
  ),
  Modal: {
    confirm: mocks.confirm,
  },
  Select: ({
    "aria-label": ariaLabel,
    disabled,
    onChange,
    options = [],
    value,
  }: {
    "aria-label"?: string;
    disabled?: boolean;
    onChange?: (value: string) => void;
    options?: Array<{ label: string; value: string }>;
    value?: string;
  }) => (
    <select
      aria-label={ariaLabel}
      disabled={disabled}
      value={value || ""}
      onChange={(event) => onChange?.(event.target.value)}
    >
      <option value="" />
      {options.map((option) => (
        <option key={option.value} value={option.value}>{option.label}</option>
      ))}
    </select>
  ),
  Tooltip: ({ children }: { children: unknown }) => <>{children}</>,
  message: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: mocks.runtimeFeatures,
}));

vi.mock("@/modules/modelProvider/api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersSelectedModelsGet: mocks.getSelectedModels,
    apiCoreModelProvidersModelsGet: mocks.getModels,
    apiCoreModelProvidersSelectedModelsPut: mocks.saveSelectedModels,
  },
  unwrapModelProviderData: (data: unknown) => data,
}));

const currentEmbedding = {
  id: "embedding-current",
  model_key: "embed_main",
  name: "Current embedding",
  provider_name: "Provider",
  group_name: "Default",
  share: true,
  user_model_provider_id: "provider-1",
  user_model_provider_group_id: "group-1",
};

const nextEmbedding = {
  ...currentEmbedding,
  id: "embedding-next",
  name: "Next embedding",
  share: false,
};

function renderSettings() {
  return render(
    <QuickModelSettings canConfigureEmbedding />,
  );
}

describe("QuickModelSettings collaboration visibility", () => {
  beforeEach(() => {
    mocks.runtimeFeatures.hideUserGroupSurfaces = true;
    mocks.confirm.mockReset();
    mocks.saveSelectedModels.mockReset().mockResolvedValue({ data: {} });
    mocks.getSelectedModels.mockReset().mockResolvedValue({
      data: { selections: [currentEmbedding] },
    });
    mocks.getModels.mockReset().mockImplementation(({ modelType }: { modelType: string }) =>
      Promise.resolve({
        data: {
          models: modelType === "embed_main"
            ? [currentEmbedding, nextEmbedding]
            : [],
        },
      }),
    );
  });

  it("switches a formerly shared embedding without organization copy in desktop mode", async () => {
    renderSettings();

    const selects = await screen.findAllByRole("combobox");
    await waitFor(() => expect(selects[1]).toHaveValue(
      "provider-1:group-1:embedding-current",
    ));
    fireEvent.change(selects[1], {
      target: { value: "provider-1:group-1:embedding-next" },
    });

    await waitFor(() => expect(mocks.saveSelectedModels).toHaveBeenCalled());
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("keeps the shared embedding confirmation in cloud mode", async () => {
    mocks.runtimeFeatures.hideUserGroupSurfaces = false;
    renderSettings();

    const selects = await screen.findAllByRole("combobox");
    await waitFor(() => expect(selects[1]).toHaveValue(
      "provider-1:group-1:embedding-current",
    ));
    fireEvent.change(selects[1], {
      target: { value: "provider-1:group-1:embedding-next" },
    });

    expect(mocks.confirm).toHaveBeenCalledTimes(1);
    expect(mocks.saveSelectedModels).not.toHaveBeenCalled();
  });
});
