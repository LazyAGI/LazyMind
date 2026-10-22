import { useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsNavigationGuard } from "@/modules/settings/SettingsNavigationGuard";
import { ProviderWorkspace } from "./ProviderWorkspace";
import type { AddedProvider } from "../pages/ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  addKey: vi.fn(),
  deleteKey: vi.fn(),
  updateEndpoint: vi.fn(),
  setActiveKey: vi.fn(),
}));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) =>
      key === "modelProvider.workspace.activeKeyConnection"
        ? `${key}:${options?.connection}:${options?.masked}`
        : key,
  }),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: Error) => error.message,
}));
vi.mock("../api", () => ({
  modelProvidersDefaultApi: {
    apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost: mocks.addKey,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete:
      mocks.deleteKey,
  },
  updateProviderEndpoint: mocks.updateEndpoint,
  setProviderActiveKey: mocks.setActiveKey,
  withModelProviderJsonOptions: (value: unknown) => value,
}));
vi.mock("./ModelModalityMarks", () => ({
  ModelModalityMarks: () => <span>modalities</span>,
}));

const provider: AddedProvider = {
  id: "openai",
  name: "OpenAI",
  source: "OpenAI",
  brand: "OpenAI",
  headline: "",
  baseUrl: "https://api.example.test/v1",
  capabilities: ["LLM_CHAT"],
  models: [],
  groups: [
    {
      id: "primary",
      name: "Primary",
      source: "OpenAI",
      baseUrl: "https://api.example.test/v1",
      apiKeyConfigured: true,
      verified: true,
      keys: [{ id: "opaque-primary", masked: "********1234", enabled: true }],
      models: [
        {
          id: "model-id",
          name: "fixture-model",
          capability: "LLM_CHAT",
          builtIn: true,
          enabled: true,
          maxInputTokens: "128K",
        },
      ],
    },
    {
      id: "backup",
      name: "Backup",
      source: "OpenAI",
      baseUrl: "https://api.example.test/v1",
      apiKeyConfigured: true,
      verified: false,
      keys: [{ id: "opaque-backup", masked: "********5678", enabled: false }],
      models: [],
    },
  ],
};
function setup(
  overrides: Partial<React.ComponentProps<typeof ProviderWorkspace>> = {},
  guarded = false,
) {
  let updateConfiguredProviders: (
    providers: AddedProvider[],
  ) => void = () => {};
  const callbacks = {
    setProvider: (next: AddedProvider) => updateConfiguredProviders([next]),
    onReload: vi.fn().mockResolvedValue(undefined),
    onDeleteModel: vi.fn(),
    onRetry: vi.fn(),
  };
  function Harness() {
    const [drawer, setDrawer] = useState(false);
    const [configuredProviders, setConfiguredProviders] = useState([provider]);
    updateConfiguredProviders = setConfiguredProviders;
    return (
      <>
        <ProviderWorkspace
          providers={[provider]}
          configuredProviders={configuredProviders}
          loading={false}
          loadError={false}
          onRetry={callbacks.onRetry}
          capabilityLabel={(type) => type}
          onAddConnection={vi.fn()}
          onEditConnection={vi.fn()}
          onDeleteConnection={vi.fn()}
          onLoadModels={vi.fn()}
          modelsLoading={{}}
          onAddModel={vi.fn()}
          onSyncModels={vi.fn()}
          onEditModel={vi.fn()}
          onDeleteModel={callbacks.onDeleteModel}
          onCheck={vi.fn()}
          checking={{}}
          onReload={callbacks.onReload}
          cloudAvailable={false}
          cloudContent={<p>Cloud login</p>}
          onConfigureDefaults={() => setDrawer(true)}
          {...overrides}
        />
        {drawer ? <p>Default drawer open</p> : null}
      </>
    );
  }
  if (guarded) {
    const router = createMemoryRouter(
      [
        {
          path: "/settings",
          element: (
            <SettingsNavigationGuard>
              <Harness />
            </SettingsNavigationGuard>
          ),
        },
      ],
      { initialEntries: ["/settings"] },
    );
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <RouterProvider router={router} />
      </ConfigProvider>,
    );
  } else
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <Harness />
      </ConfigProvider>,
    );
  return callbacks;
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.addKey.mockResolvedValue({ data: { success: true } });
  mocks.deleteKey.mockResolvedValue({ data: { success: true } });
  mocks.setActiveKey.mockResolvedValue({
    group_id: "backup",
    key_id: "opaque-backup",
  });
});

describe("compact provider workspace", () => {
  it("keeps each connection's masked keys separate", async () => {
    setup();
    expect(screen.getByText("********1234")).toBeInTheDocument();
    fireEvent.mouseDown(
      screen.getByRole("combobox", {
        name: "modelProvider.workspace.selectConnection",
      }),
    );
    fireEvent.click(
      await screen.findByText("Backup", {
        selector: ".ant-select-item-option-content",
      }),
    );
    expect(screen.getByText("********5678")).toBeInTheDocument();
    expect(screen.queryByText("********1234")).not.toBeInTheDocument();
  });
  it("switches the supplier's active key across instances and retains the other saved key", async () => {
    const callbacks = setup();
    expect(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    ).toBeChecked();
    fireEvent.mouseDown(
      screen.getByRole("combobox", {
        name: "modelProvider.workspace.selectConnection",
      }),
    );
    fireEvent.click(
      await screen.findByText("Backup", {
        selector: ".ant-select-item-option-content",
      }),
    );
    expect(
      screen.getByText(
        "modelProvider.workspace.activeKeyConnection:Primary:********1234",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    ).not.toBeChecked();
    fireEvent.click(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    );
    await waitFor(() =>
      expect(mocks.setActiveKey).toHaveBeenCalledWith(
        "openai",
        "backup",
        "opaque-backup",
      ),
    );
    await waitFor(() => expect(callbacks.onReload).toHaveBeenCalled());
    const next = {
      ...provider,
      groups: provider.groups.map((connection) => ({
        ...connection,
        keys: connection.keys.map((key) => ({
          ...key,
          enabled: connection.id === "backup",
        })),
      })),
    };
    await act(async () => callbacks.setProvider(next));
    expect(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    ).toBeChecked();
    fireEvent.mouseDown(
      screen.getByRole("combobox", {
        name: "modelProvider.workspace.selectConnection",
      }),
    );
    fireEvent.click(
      await screen.findByText("Primary", {
        selector: ".ant-select-item-option-content",
      }),
    );
    expect(screen.getByText("********1234")).toBeInTheDocument();
    expect(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    ).not.toBeChecked();
    expect(
      screen.getByText(
        "modelProvider.workspace.activeKeyConnection:Backup:********5678",
      ),
    ).toBeInTheDocument();
    expect(mocks.deleteKey).not.toHaveBeenCalled();
  });

  it("allows disabling the only active key while retaining all saved keys", async () => {
    const callbacks = setup();
    fireEvent.click(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    );
    await waitFor(() =>
      expect(mocks.setActiveKey).toHaveBeenCalledWith("openai", "", ""),
    );
    await waitFor(() => expect(callbacks.onReload).toHaveBeenCalled());
    await act(async () =>
      callbacks.setProvider({
        ...provider,
        groups: provider.groups.map((connection) => ({
          ...connection,
          keys: connection.keys.map((key) => ({ ...key, enabled: false })),
        })),
      }),
    );
    expect(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    ).not.toBeChecked();
    expect(
      screen.getByText("modelProvider.workspace.noActiveKey"),
    ).toBeInTheDocument();
    expect(screen.getByText("********1234")).toBeInTheDocument();
    expect(mocks.deleteKey).not.toHaveBeenCalled();
  });

  it("retains the previous selection when enabling another key fails verification", async () => {
    const callbacks = setup();
    mocks.setActiveKey.mockRejectedValueOnce(
      new Error("key verification failed"),
    );
    fireEvent.mouseDown(
      screen.getByRole("combobox", {
        name: "modelProvider.workspace.selectConnection",
      }),
    );
    fireEvent.click(
      await screen.findByText("Backup", {
        selector: ".ant-select-item-option-content",
      }),
    );
    fireEvent.click(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    );
    expect(
      await screen.findByText("key verification failed"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "modelProvider.workspace.activeKeyConnection:Primary:********1234",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("switch", {
        name: "modelProvider.workspace.useKeyNumber",
      }),
    ).not.toBeChecked();
    expect(callbacks.onReload).not.toHaveBeenCalled();
  });

  it("adds a key only to the selected instance and clears the secret after success", async () => {
    const callbacks = setup();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.addKey" }),
    );
    const dialog = within(await screen.findByRole("dialog"));
    fireEvent.change(dialog.getByLabelText("API Key"), {
      target: { value: "secret-new-5678" },
    });
    fireEvent.click(
      dialog.getByRole("button", {
        name: "modelProvider.workspace.addAndCheckKey",
      }),
    );
    await waitFor(() =>
      expect(mocks.addKey).toHaveBeenCalledWith(
        { modelProviderId: "openai", groupId: "primary" },
        { data: { api_key: "secret-new-5678" }, timeout: 180000 },
      ),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(callbacks.onReload).toHaveBeenCalled();
    expect(mocks.setActiveKey).not.toHaveBeenCalled();
    expect(
      screen.queryByDisplayValue("secret-new-5678"),
    ).not.toBeInTheDocument();
  });
  it("deletes a saved key by opaque ID without submitting masked text", async () => {
    setup();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.deleteKeyNumber",
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "common.delete" }),
    );
    await waitFor(() =>
      expect(mocks.deleteKey).toHaveBeenCalledWith(
        { modelProviderId: "openai", groupId: "primary" },
        { data: { key_id: "opaque-primary" } },
      ),
    );
  });
  it("resets a discarded endpoint even when the defaults drawer leaves this page mounted", async () => {
    setup({}, true);
    const endpoint = screen.getByRole("textbox", { name: "API Host" });
    fireEvent.change(endpoint, {
      target: { value: "https://unsaved.example.test/v1" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.defaults" }),
    );
    fireEvent.click(await screen.findByText("settingsPage.unsaved.stay"));
    expect(endpoint).toHaveValue("https://unsaved.example.test/v1");
    expect(screen.queryByText("Default drawer open")).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.defaults" }),
    );
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    expect(await screen.findByText("Default drawer open")).toBeInTheDocument();
    expect(endpoint).toHaveValue(provider.baseUrl);
    fireEvent.change(endpoint, {
      target: { value: "https://second-unsaved.example.test" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.defaults" }),
    );
    expect(
      await screen.findByText("settingsPage.unsaved.title"),
    ).toBeInTheDocument();
  });
  it("offers loading retry and preserves access to signed-out cloud login", async () => {
    const callbacks = setup({ loadError: true });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "modelProvider.workspace.loadFailed",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.retry" }),
    );
    expect(callbacks.onRetry).toHaveBeenCalledOnce();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.cloud" }),
    );
    expect(await screen.findByText("Cloud login")).toBeInTheDocument();
  });
  it("allows removing a catalog model through the same confirmation flow", async () => {
    const callbacks = setup();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.deleteModelAria" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "common.delete" }),
    );
    expect(callbacks.onDeleteModel).toHaveBeenCalledWith(
      provider,
      provider.groups[0],
      provider.groups[0].models[0],
    );
  });
});
