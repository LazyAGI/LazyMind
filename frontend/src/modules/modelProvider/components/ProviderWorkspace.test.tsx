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
  createGroup: vi.fn(),
  createProvider: vi.fn(),
  updateProvider: vi.fn(),
  deleteProvider: vi.fn(),
  getModels: vi.fn(),
  logoObservers: [] as Array<(busy: boolean) => void>,
}));
vi.mock("./CustomLogoPicker", () => ({
  CustomLogoPicker: ({ value, onChange, onBusyChange }: { value?: string; onChange: (value: string) => void; onBusyChange: (busy: boolean) => void }) => {
    mocks.logoObservers.push(onBusyChange);
    return <input aria-label="Test supplier logo" value={value || ""} onChange={(event) => onChange(event.target.value)} />;
  },
}));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: Error) => error.message,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
    apiCoreModelProvidersModelProviderIdGroupsPost: mocks.createGroup,
  },
  createCustomProvider: mocks.createProvider,
  updateCustomProviderName: mocks.updateProvider,
  deleteCustomProvider: mocks.deleteProvider,
  unwrapModelProviderData: (data: unknown) => data,
  modelProvidersDefaultApi: {
    apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost: mocks.addKey,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete:
      mocks.deleteKey,
  },
  updateProviderEndpoint: mocks.updateEndpoint,
  setProviderActiveKey: mocks.setActiveKey,
  withModelProviderJsonOptions: (value: unknown) => value,
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
          onLoadModels={vi.fn()}
          modelsLoading={{}}
          onAddModel={vi.fn()}
          onSyncModels={vi.fn()}
          onEditModel={vi.fn()}
          onDeleteModel={callbacks.onDeleteModel}
          onCheck={vi.fn()}
          checking={{}}
          onReload={callbacks.onReload}
          cloudContent={<p>Cloud login</p>}
          onBack={() => setDrawer(true)}
          {...overrides}
        />
        {drawer ? <p>Settings overview open</p> : null}
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
async function openAddKeyDialog() {
  fireEvent.click(
    screen.getByRole("button", { name: "modelProvider.keyManager.manage" }),
  );
  const manager = within(await screen.findByRole("dialog"));
  fireEvent.click(
    manager.getByRole("button", { name: "modelProvider.workspace.addKey" }),
  );
  return within(await screen.findByRole("dialog"));
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.logoObservers = [];
  mocks.addKey.mockResolvedValue({ data: { success: true } });
  mocks.deleteKey.mockResolvedValue({ data: { success: true } });
  mocks.createGroup.mockResolvedValue({
    data: { id: "new-private-record", is_verified: true },
  });
  mocks.createProvider.mockResolvedValue({
    id: "created-supplier",
    group_id: "created-private-record",
  });
  mocks.updateProvider.mockResolvedValue({ id: "custom" });
  mocks.deleteProvider.mockResolvedValue({ id: "custom" });
  mocks.getModels.mockResolvedValue({ data: { models: [] } });
  mocks.setActiveKey.mockResolvedValue({
    group_id: "backup",
    key_id: "opaque-backup",
  });
});

describe("compact provider workspace", () => {
  it("keeps uploaded supplier artwork in a failed create draft and retries it", async () => {
    mocks.createProvider.mockRejectedValueOnce(new Error("Create failed"));
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
    const dialog = within(await screen.findByRole("dialog"));
    fireEvent.change(dialog.getByLabelText("modelProvider.workspace.supplierName"), { target: { value: "My gateway" } });
    fireEvent.change(dialog.getByRole("textbox", { name: "Test supplier logo" }), { target: { value: "supplier-logo" } });
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(mocks.createProvider).toHaveBeenCalledTimes(1));
    expect(mocks.createProvider).toHaveBeenLastCalledWith({ name: "My gateway", source_provider_id: "openai", base_url: provider.baseUrl, logo_data_url: "supplier-logo" });
    expect(dialog.getByRole("textbox", { name: "Test supplier logo" })).toHaveValue("supplier-logo");
    await waitFor(() => expect(dialog.getByRole("button", { name: "common.save" })).toBeEnabled());
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(mocks.createProvider).toHaveBeenCalledTimes(2));
    expect(mocks.createProvider.mock.calls[1][0].logo_data_url).toBe("supplier-logo");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it.each(["preserve", "reset", "replace"])("%s supplier artwork when saving its edit", async (mode) => {
    const custom = { ...provider, id: "custom", name: "Private proxy", isCustom: true, logoDataUrl: "saved-logo" };
    setup({ providers: [custom], configuredProviders: [custom] });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.editSupplier" }));
    const dialog = within(await screen.findByRole("dialog"));
    expect(dialog.getByRole("textbox", { name: "Test supplier logo" })).toHaveValue("saved-logo");
    if (mode !== "preserve") fireEvent.change(dialog.getByRole("textbox", { name: "Test supplier logo" }), { target: { value: mode === "reset" ? "" : "new-logo" } });
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(mocks.updateProvider).toHaveBeenCalledTimes(1));
    expect(mocks.updateProvider.mock.calls[0]).toEqual(mode === "preserve" ? ["custom", "Private proxy"] : ["custom", "Private proxy", mode === "reset" ? "" : "new-logo"]);
  });

  it("locks supplier submission and dismissal during image reads and ignores an older session observer", async () => {
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
    const first = await screen.findByRole("dialog");
    const oldObserver = mocks.logoObservers[mocks.logoObservers.length - 1]!;
    act(() => oldObserver(true));
    expect(within(first).getByRole("button", { name: "common.save" })).toBeDisabled();
    expect(within(first).getByRole("button", { name: "Cancel" })).toBeDisabled();
    fireEvent.submit(first.querySelector("form")!);
    expect(mocks.createProvider).not.toHaveBeenCalled();
    act(() => oldObserver(false));
    fireEvent.click(within(first).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
    const second = within(await screen.findByRole("dialog"));
    const current = mocks.logoObservers[mocks.logoObservers.length - 1]!;
    act(() => current(true));
    act(() => oldObserver(false));
    expect(second.getByRole("button", { name: "common.save" })).toBeDisabled();
    act(() => current(false));
    expect(second.getByRole("button", { name: "common.save" })).toBeEnabled();
  });

  it("inherits supplier artwork in its sidebar, heading and saved models", () => {
    const logoDataUrl = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==";
    const custom = { ...provider, id: "custom", name: "Private proxy", isCustom: true, logoDataUrl };
    setup({ providers: [custom], configuredProviders: [custom] });
    for (const selector of [".provider-workspace-provider img", ".provider-workspace-supplier-title img", ".saved-provider-model-row img"]) {
      expect(document.querySelector(selector)).toHaveAttribute("src", logoDataUrl);
    }
  });

  it("allows only one supplier save while its request is pending", async () => {
    let finish!: (value: { id: string; group_id: string }) => void;
    mocks.createProvider.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
    const dialogElement = await screen.findByRole("dialog");
    const dialog = within(dialogElement);
    fireEvent.change(dialog.getByLabelText("modelProvider.workspace.supplierName"), { target: { value: "My gateway" } });
    fireEvent.change(dialog.getByRole("textbox", { name: "Test supplier logo" }), { target: { value: "supplier-logo" } });
    fireEvent.submit(dialogElement.querySelector("form")!);
    fireEvent.submit(dialogElement.querySelector("form")!);
    await waitFor(() => expect(mocks.createProvider).toHaveBeenCalledTimes(1));
    expect(dialog.getByRole("button", { name: /common.save/ })).toBeDisabled();
    expect(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();
    await act(async () => finish({ id: "created-supplier", group_id: "created-group" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(mocks.createProvider).toHaveBeenCalledTimes(1);
  });

  it("keeps an edit bound to its original supplier when selection changes", async () => {
    const original = { ...provider, id: "custom-a", name: "Gateway A", isCustom: true, logoDataUrl: "logo-a" };
    const other = { ...provider, id: "custom-b", name: "Gateway B", isCustom: true, logoDataUrl: "logo-b" };
    setup({ providers: [original, other], configuredProviders: [original, other] });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.editSupplier" }));
    const dialog = within(await screen.findByRole("dialog"));
    fireEvent.change(dialog.getByRole("textbox", { name: "Test supplier logo" }), { target: { value: "replacement-a" } });
    fireEvent.click(screen.getByRole("button", { name: "Gateway B" }));
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(mocks.updateProvider).toHaveBeenCalledWith("custom-a", "Gateway A", "replacement-a"));
  });

  it("expands a brand without selecting it and configures only an independent service leaf", () => {
    const standard = { ...provider, id: "ark-standard", name: "Doubao", brandKey: "Doubao", serviceKey: "standard", groups: [] };
    const coding = { ...provider, id: "ark-coding", name: "DoubaoCodingPlan", brandKey: "Doubao", serviceKey: "coding", groups: [] };
    const onLoadModels = vi.fn();
    setup({ providers: [provider, standard, coding], configuredProviders: [provider, standard, coding], onLoadModels, highlightProviderId: "openai" });
    const nav = screen.getByRole("navigation", { name: "modelProvider.workspace.suppliers" });
    const brand = within(nav).getByRole("button", { name: "Volcengine" });
    expect(brand).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(brand);
    expect(brand).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("heading", { name: "OpenAI" })).toBeInTheDocument();
    fireEvent.click(within(nav).getByRole("button", { name: "Coding Plan" }));
    expect(screen.getByRole("heading", { name: "Volcengine · Coding Plan" })).toBeInTheDocument();
    expect(within(nav).getByRole("button", { name: "Coding Plan" })).toHaveAttribute("aria-current", "page");
  });

  it("searches services under their parent and never groups custom protocol-compatible suppliers", () => {
    const standard = { ...provider, id: "ark-standard", name: "Doubao", brandKey: "Doubao", serviceKey: "standard", groups: [] };
    const coding = { ...provider, id: "ark-coding", name: "DoubaoCodingPlan", brandKey: "Doubao", serviceKey: "coding", groups: [] };
    const custom = { ...provider, id: "aihubmix", name: "AIhubmix", isCustom: true, protocolName: "Doubao", source: "Doubao", groups: [] };
    setup({ providers: [standard, coding, custom], configuredProviders: [standard, coding, custom] });
    const nav = screen.getByRole("navigation", { name: "modelProvider.workspace.suppliers" });
    expect(within(nav).getByRole("button", { name: "AIhubmix" })).not.toHaveAttribute("aria-expanded");
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.searchAria" }), { target: { value: "Coding Plan" } });
    expect(within(nav).getByRole("button", { name: "Volcengine" })).toHaveAttribute("aria-expanded", "true");
    expect(within(nav).getByRole("button", { name: "Coding Plan" })).toBeInTheDocument();
    expect(within(nav).queryByRole("button", { name: "Standard API" })).not.toBeInTheDocument();
    expect(within(nav).queryByRole("button", { name: "AIhubmix" })).not.toBeInTheDocument();
  });

  it("opens the selected service ancestor and filters configured services at the leaf", () => {
    const standard = { ...provider, id: "ark-standard", name: "Doubao", brandKey: "Doubao", serviceKey: "standard", groups: [] };
    const coding = { ...provider, id: "ark-coding", name: "DoubaoCodingPlan", brandKey: "Doubao", serviceKey: "coding" };
    setup({ providers: [standard, coding], configuredProviders: [standard, coding], highlightProviderId: "ark-coding" });
    const nav = screen.getByRole("navigation", { name: "modelProvider.workspace.suppliers" });
    expect(within(nav).getByRole("button", { name: "Volcengine" })).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(screen.getByRole("button", { name: /modelProvider.workspace.configured/ }));
    expect(within(nav).getByRole("button", { name: "Coding Plan" })).toBeInTheDocument();
    expect(within(nav).queryByRole("button", { name: "Standard API" })).not.toBeInTheDocument();
  });

  it("keeps mobile navigation on the brand list until a service is chosen", () => {
    const standard = { ...provider, id: "ark-standard", name: "Doubao", brandKey: "Doubao", serviceKey: "standard", groups: [] };
    const coding = { ...provider, id: "ark-coding", name: "DoubaoCodingPlan", brandKey: "Doubao", serviceKey: "coding", groups: [] };
    setup({ providers: [provider, standard, coding], configuredProviders: [provider, standard, coding] });
    const workspace = document.querySelector(".provider-workspace");
    const nav = screen.getByRole("navigation", { name: "modelProvider.workspace.suppliers" });
    fireEvent.click(within(nav).getByRole("button", { name: "Volcengine" }));
    expect(workspace).not.toHaveClass("is-mobile-detail");
    fireEvent.click(within(nav).getByRole("button", { name: "Coding Plan" }));
    expect(workspace).toHaveClass("is-mobile-detail");
  });

  it("shows a plan's official usage scope without blocking an empty model inventory", () => {
    const coding = { ...provider, id: "qwen-coding", name: "QwenCodingPlan", brandKey: "Qwen", serviceKey: "coding", groups: [] };
    setup({ providers: [coding], configuredProviders: [coding] });
    const notice = screen.getByRole("alert");
    expect(notice).toHaveTextContent("modelProvider.workspace.serviceScopeQwen");
    expect(within(notice).getByRole("link")).toHaveAttribute("href", "https://help.aliyun.com/zh/model-studio/more-tools");
    expect(screen.getByRole("button", { name: "modelProvider.workspace.getModelList" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "modelProvider.customModel" })).toBeEnabled();
  });

  it("renders an empty supplier list before loading without treating it as a saved endpoint", () => {
    setup({ providers: [], configuredProviders: [] });
    expect(screen.getAllByText("modelProvider.noMatchedProviders").length).toBeGreaterThan(0);
    expect(screen.queryByRole("textbox", { name: "modelProvider.apiAddressLabel" })).not.toBeInTheDocument();
  });

  it.each([
    ["https://api.example.test/", provider.baseUrl],
    ["https://new.example.test", "https://new.example.test/v1/"],
  ])("accepts the server's normalized endpoint after saving %s even before a provider refresh", async (entered, canonical) => {
    mocks.updateEndpoint.mockResolvedValueOnce({ id: provider.id, base_url: canonical });
    const callbacks = setup({}, true);
    const endpoint = screen.getByRole("textbox", { name: "modelProvider.apiAddressLabel" });
    fireEvent.change(endpoint, { target: { value: entered } });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.saveEndpoint" }));
    await waitFor(() => expect(callbacks.onReload).toHaveBeenCalledOnce());
    await waitFor(() => expect(endpoint).toHaveValue(canonical));
    expect(screen.queryByRole("button", { name: "modelProvider.workspace.saveEndpoint" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.workspace.getModelList" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.back" }));
    expect(await screen.findByText("Settings overview open")).toBeInTheDocument();
    expect(screen.queryByText("settingsPage.unsaved.title")).not.toBeInTheDocument();
  });

  it("shows a verified keyless custom supplier as connected without adding a sidebar status dot", () => {
    const custom = { ...provider, id: "custom", name: "Local models", isCustom: true, groups: [{ ...provider.groups[0], keys: [], apiKeyConfigured: false, verified: true }] };
    setup({ providers: [custom], configuredProviders: [custom] });
    expect(document.querySelector(".provider-workspace-status")).toHaveTextContent("modelProvider.workspace.connected");
    expect(screen.getByRole("button", { name: /Local models/ }).querySelector("i")).not.toBeInTheDocument();
  });

  it("separates pointer focus from keyboard navigation without removing focused controls", () => {
    setup();
    const workspace = document.querySelector(".provider-workspace");
    const endpoint = screen.getByRole("textbox", { name: "modelProvider.apiAddressLabel" });
    fireEvent.keyDown(document, { key: "Tab" });
    expect(workspace).toHaveAttribute("data-focus-modality", "keyboard");
    endpoint.focus();
    expect(endpoint).toHaveFocus();
    fireEvent.pointerDown(endpoint);
    expect(workspace).toHaveAttribute("data-focus-modality", "pointer");
    expect(endpoint).toHaveFocus();
  });

  it("keeps all task filters visible and combines image tasks while searching a flat list", () => {
    const imageModels = [
      { ...provider.groups[0].models[0], id: "paint", name: "paint-model", capability: "TEXT_TO_IMAGE" as const },
      { ...provider.groups[0].models[0], id: "edit", name: "edit-model", capability: "IMAGE_EDITING" as const },
    ];
    const mixed = { ...provider, groups: [{ ...provider.groups[0], models: [...provider.groups[0].models, ...imageModels] }] };
    setup({ providers: [mixed], configuredProviders: [mixed] });
    const filters = within(screen.getByRole("group", { name: "modelProvider.workspace.modelType" }));
    const images = filters.getByRole("button", { name: /modelProvider.taskTab.image/ });
    expect(filters.getAllByRole("button")).toHaveLength(9);
    expect(images).toHaveTextContent("2");
    fireEvent.click(images);
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.workspace.searchModel" }), { target: { value: "paint" } });
    expect(images).toHaveTextContent("1");
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
    expect(filters.getByRole("button", { name: /modelProvider.taskTab.video/ })).toHaveTextContent("0");
  });
  it("puts search, the labeled model fetch action, custom model and working overflow actions together", async () => {
    const onSyncModels = vi.fn();
    const onAddModel = vi.fn();
    const onLoadModels = vi.fn();
    setup({ onSyncModels, onAddModel, onLoadModels });
    const toolbar = document.querySelector(".provider-workspace-model-toolbar")!;
    const sync = screen.getByRole("button", { name: "modelProvider.workspace.getModelList" });
    const add = screen.getByRole("button", { name: "modelProvider.customModel" });
    expect(toolbar).toContainElement(screen.getByRole("textbox", { name: "modelProvider.workspace.searchModel" }));
    expect(toolbar).toContainElement(sync);
    expect(toolbar).toContainElement(add);
    expect(sync).toHaveTextContent("modelProvider.workspace.getModelList");
    expect(add).toHaveTextContent(/^$/);
    fireEvent.click(sync);
    expect(onSyncModels).toHaveBeenCalledTimes(1);
    expect(onSyncModels).toHaveBeenCalledWith(provider, provider.groups[0]);
    fireEvent.click(add);
    expect(onAddModel).toHaveBeenCalledTimes(1);
    expect(onAddModel).toHaveBeenCalledWith(provider, provider.groups[0]);
    onLoadModels.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.moreModelActions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "modelProvider.workspace.refreshSavedModels" }));
    expect(onLoadModels).toHaveBeenCalledWith(provider, provider.groups[0]);
  });
  it("keeps a supplier logo when its request protocol changes", () => {
    const supplier = { ...provider, name: "DeepSeek", protocolName: "Claude", source: "Claude" };
    setup({ providers: [supplier], configuredProviders: [supplier] });
    expect(document.querySelector(".provider-workspace-supplier-title img")).toHaveAttribute("src", "/provider-icons/deepseek.svg");
  });
  it("shows a retryable model-load failure while retaining the last saved models", () => {
    const onLoadModels = vi.fn();
    setup({
      onLoadModels,
      modelsError: { "openai:primary": "Temporary model service error" },
    });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("modelProvider.workspace.modelsLoadFailed");
    expect(alert).toHaveTextContent("Temporary model service error");
    expect(screen.getByRole("listitem", { name: "fixture-model" })).toBeInTheDocument();
    onLoadModels.mockClear();
    fireEvent.click(within(alert).getByRole("button", { name: "modelProvider.workspace.retry" }));
    expect(onLoadModels).toHaveBeenCalledWith(provider, provider.groups[0]);
  });

  it("keeps load errors actionable and explains how to fetch an empty all-model list", () => {
    const empty = { ...provider, groups: [{ ...provider.groups[0], models: [] }] };
    setup({
      providers: [empty],
      configuredProviders: [empty],
      modelsError: { "openai:primary": "Service unavailable" },
    });
    expect(screen.getByRole("alert")).toHaveTextContent("modelProvider.workspace.modelsLoadFailed");
    expect(screen.queryByText("modelProvider.noModels")).not.toBeInTheDocument();
    expect(screen.getByText("modelProvider.workspace.syncModelsEmpty")).toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.workspace.searchModel" }), { target: { value: "missing" } });
    expect(screen.queryByText("modelProvider.workspace.syncModelsEmpty")).not.toBeInTheDocument();
    expect(screen.getByText("modelProvider.workspace.noMatchingModels")).toBeInTheDocument();
  });

  it("protects endpoint edits when returning to the mobile supplier list", async () => {
    setup({}, true);
    fireEvent.click(document.querySelector(".provider-workspace-provider")!);
    const workspace = document.querySelector(".provider-workspace");
    expect(workspace).toHaveClass("is-mobile-detail");
    const endpoint = screen.getByRole("textbox", { name: "modelProvider.apiAddressLabel" });
    fireEvent.change(endpoint, { target: { value: "https://unsaved.example.test/v1" } });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.suppliers" }));
    expect(await screen.findByText("settingsPage.unsaved.title")).toBeInTheDocument();
    expect(workspace).toHaveClass("is-mobile-detail");
    fireEvent.click(screen.getByText("settingsPage.unsaved.stay"));
    expect(endpoint).toHaveValue("https://unsaved.example.test/v1");
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.suppliers" }));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    await waitFor(() => expect(workspace).not.toHaveClass("is-mobile-detail"));
    expect(endpoint).toHaveValue(provider.baseUrl);
  });

  it("counts keys, models or configured credentials rather than empty compatibility groups", () => {
    const variants = [
      { id: "empty", name: "Empty supplier", keys: [], models: [], apiKeyConfigured: false },
      { id: "keys", name: "Keys supplier", keys: provider.groups[0].keys, models: [], apiKeyConfigured: false },
      { id: "models", name: "Models supplier", keys: [], models: provider.groups[0].models, apiKeyConfigured: false },
      { id: "stored-models", name: "Stored-only supplier", keys: [], models: [], modelCount: 2, apiKeyConfigured: false },
      { id: "credentials", name: "Credentials supplier", keys: [], models: [], apiKeyConfigured: true },
    ].map(({ id, name, ...state }) => ({
      ...provider,
      id,
      name,
      groups: [{ ...provider.groups[0], ...state, id: `${id}-group`, verified: false }],
    }));
    setup({ providers: variants, configuredProviders: variants });
    const emptyButton = screen.getByRole("button", { name: /Empty supplier/ });
    expect(emptyButton.querySelector("i")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.configured" }));
    expect(screen.queryByRole("button", { name: /Empty supplier/ })).not.toBeInTheDocument();
    for (const name of ["Keys supplier", "Models supplier", "Stored-only supplier", "Credentials supplier"]) {
      expect(screen.getByRole("button", { name: new RegExp(name) })).toBeInTheDocument();
    }
  });

  it("validates custom supplier names against the backend UTF-8 byte limit", async () => {
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
    const dialog = within(await screen.findByRole("dialog"));
    const name = dialog.getByLabelText("modelProvider.workspace.supplierName");
    fireEvent.change(name, { target: { value: "中".repeat(86) } });
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    expect(await screen.findByText("modelProvider.workspace.supplierNameBytes")).toBeInTheDocument();
    expect(mocks.createProvider).not.toHaveBeenCalled();
    fireEvent.change(name, { target: { value: ` ${"中".repeat(85)} ` } });
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(mocks.createProvider).toHaveBeenCalledWith({
      name: "中".repeat(85),
      source_provider_id: "openai",
      base_url: provider.baseUrl,
    }));
  });

  it.each(["custom", "default", "empty"] as const)(
    "keeps or updates a %s API address when changing supplier templates",
    async (mode) => {
      const deepseek = { ...provider, id: "deepseek", name: "DeepSeek", source: "DeepSeek", baseUrl: "https://api.deepseek.example/v1", groups: [] };
      setup({ providers: [provider, deepseek], configuredProviders: [provider] });
      fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
      const dialog = within(await screen.findByRole("dialog"));
      const endpoint = dialog.getByRole("textbox", { name: "modelProvider.apiAddressLabel" });
      if (mode !== "default") {
        fireEvent.change(endpoint, { target: { value: mode === "custom" ? "https://private.example.test/v1" : "" } });
      }
      fireEvent.mouseDown(dialog.getByRole("combobox"));
      fireEvent.click(await screen.findByText("DeepSeek", { selector: ".ant-select-item-option-content" }));
      expect(endpoint).toHaveValue(mode === "custom" ? "https://private.example.test/v1" : deepseek.baseUrl);
    },
  );

  it("filters task types without splitting dialogue or vector models by modality", async () => {
    const capabilities = [
      "LLM_CHAT",
      "VLM",
      "EMBEDDING",
      "MULTIMODAL_EMBEDDING",
      "RERANK",
    ] as const;
    const models = capabilities.map((capability) => ({
      ...provider.groups[0].models[0],
      id: capability,
      name: `fixture-${capability}`,
      capability,
      builtIn: false,
    }));
    const mixed = {
      ...provider,
      groups: [{ ...provider.groups[0], models }],
    };
    setup({ providers: [mixed], configuredProviders: [mixed] });
    const filters = within(screen.getByRole("group", { name: "modelProvider.workspace.modelType" }));
    const vectors = filters.getByRole("button", { name: /modelProvider.taskTab.embedding/ });
    expect(vectors).toHaveTextContent("2");
    expect(filters.getByRole("button", { name: /modelProvider.taskTab.image/ })).toHaveTextContent("0");
    fireEvent.click(vectors);
    expect(vectors).toHaveAttribute("aria-pressed", "true");
    await waitFor(() =>
      expect(
        document.querySelectorAll(".saved-provider-model-row"),
      ).toHaveLength(2),
    );
    expect(
      screen.getByRole("listitem", { name: "fixture-EMBEDDING" }),
    ).toBeVisible();
    expect(
      screen.getByRole("listitem", { name: "fixture-MULTIMODAL_EMBEDDING" }),
    ).toBeVisible();
    expect(
      screen.queryByRole("listitem", { name: "fixture-RERANK" }),
    ).not.toBeInTheDocument();
  });
  it("places key controls before the API address and only shows save for an address draft", () => {
    setup();
    const keys = document.querySelector(".provider-workspace-api-keys")!;
    const endpoint = document.querySelector(".provider-workspace-endpoint")!;
    expect(
      keys.compareDocumentPosition(endpoint) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      screen.queryByRole("button", {
        name: "modelProvider.workspace.saveEndpoint",
      }),
    ).not.toBeInTheDocument();
    fireEvent.change(
      screen.getByRole("textbox", { name: "modelProvider.apiAddressLabel" }),
      {
        target: { value: "https://api.example.test/v2" },
      },
    );
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.saveEndpoint",
      }),
    ).toBeEnabled();
  });
  it("pins custom suppliers and their manually added models before catalog entries", () => {
    const custom = {
      ...provider,
      id: "custom-supplier",
      name: "Zulu Custom",
      isCustom: true,
      groups: [
        {
          ...provider.groups[0],
          models: [
            ...provider.groups[0].models,
            {
              id: "custom-model",
              name: "zulu-custom-model",
              capability: "LLM_CHAT" as const,
              builtIn: false,
              enabled: true,
            },
          ],
        },
      ],
    };
    setup({
      providers: [provider, custom],
      configuredProviders: [provider, custom],
    });
    const suppliers = document.querySelectorAll(".provider-workspace-provider");
    expect(suppliers[0]).toHaveTextContent("Zulu Custom");
    const rows = document.querySelectorAll(".saved-provider-model-row");
    expect(rows[0]).toHaveTextContent("zulu-custom-model");
  });
  it("shows supplier settings directly without instance controls or explanatory clutter", () => {
    setup();
    expect(screen.getByText("****1234")).toBeInTheDocument();
    for (const key of [
      "selectConnection",
      "addConnection",
      "connections",
      "supplierNote",
      "endpointShared",
      "activeKeyConnection",
      "saved",
    ]) {
      expect(
        screen.queryByText(`modelProvider.workspace.${key}`),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("button", {
          name: `modelProvider.workspace.${key}`,
        }),
      ).not.toBeInTheDocument();
    }
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.addSupplier",
      }),
    ).toBeInTheDocument();
  });
  it("switches the single enabled key while retaining other saved keys", async () => {
    const callbacks = setup();
    const next = {
      ...provider,
      groups: [
        {
          ...provider.groups[0],
          keys: [
            ...provider.groups[0].keys,
            { id: "opaque-backup", masked: "********5678", enabled: false },
          ],
        },
      ],
    };
    await act(async () => callbacks.setProvider(next));
    const switches = screen.getAllByRole("switch");
    expect(switches[0]).toBeChecked();
    expect(switches[1]).not.toBeChecked();
    fireEvent.click(switches[1]);
    await waitFor(() =>
      expect(mocks.setActiveKey).toHaveBeenCalledWith(
        "openai",
        "primary",
        "opaque-backup",
      ),
    );
    await waitFor(() => expect(callbacks.onReload).toHaveBeenCalled());
    await act(async () =>
      callbacks.setProvider({
        ...next,
        groups: [
          {
            ...next.groups[0],
            keys: next.groups[0].keys.map((key) => ({
              ...key,
              enabled: key.id === "opaque-backup",
            })),
          },
        ],
      }),
    );
    expect(screen.getAllByRole("switch")[0]).not.toBeChecked();
    expect(screen.getAllByRole("switch")[1]).toBeChecked();
    expect(screen.getByText("****1234")).toBeInTheDocument();
    expect(screen.getByText("****5678")).toBeInTheDocument();
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
    expect(screen.getByText("****1234")).toBeInTheDocument();
    expect(mocks.deleteKey).not.toHaveBeenCalled();
  });

  it("retains the previous selection when a new key fails verification", async () => {
    const callbacks = setup();
    await act(async () =>
      callbacks.setProvider({
        ...provider,
        groups: [
          {
            ...provider.groups[0],
            keys: [
              ...provider.groups[0].keys,
              { id: "opaque-backup", masked: "********5678", enabled: false },
            ],
          },
        ],
      }),
    );
    mocks.setActiveKey.mockRejectedValueOnce(
      new Error("key verification failed"),
    );
    fireEvent.click(screen.getAllByRole("switch")[1]);
    expect(
      await screen.findByText("key verification failed"),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("switch")[0]).toBeChecked();
    expect(screen.getAllByRole("switch")[1]).not.toBeChecked();
    expect(callbacks.onReload).not.toHaveBeenCalled();
  });
  it("adds a supplier key and clears the secret after success", async () => {
    const onKeySaved = vi.fn().mockResolvedValue(undefined);
    const callbacks = setup({ onKeySaved });
    const dialog = await openAddKeyDialog();
    fireEvent.change(dialog.getByLabelText("modelProvider.apiKeyLabel"), {
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
    await waitFor(() => expect(onKeySaved).toHaveBeenCalledWith(provider, "primary", expect.any(Function)));
    expect(onKeySaved.mock.calls[0][2]()).toBe(true);
    expect(mocks.setActiveKey).not.toHaveBeenCalled();
    expect(
      screen.queryByDisplayValue("secret-new-5678"),
    ).not.toBeInTheDocument();
  });
  it("creates the first private credential storage directly from the empty key field", async () => {
    const onKeySaved = vi.fn().mockResolvedValue(undefined);
    const callbacks = setup({ onKeySaved });
    await act(async () => callbacks.setProvider({ ...provider, groups: [] }));
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.change(input, { target: { value: "  fixture-first-key  " } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    fireEvent.blur(input);
    await waitFor(() => expect(mocks.createGroup).toHaveBeenCalledTimes(1));
    expect(mocks.createGroup).toHaveBeenCalledWith({
      modelProviderId: "openai",
      createModelProviderGroupOpenAPIRequest: { name: "OpenAI", api_key: "fixture-first-key", verify: false },
    }, { timeout: 180000 });
    await waitFor(() => expect(input).toHaveValue(""));
    expect(mocks.addKey).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(onKeySaved).toHaveBeenCalledWith(expect.objectContaining({ id: "openai" }), "new-private-record", expect.any(Function)));
  });
  it("releases key editing after saving while a separate connection check is still running", async () => {
    let finish!: () => void;
    const onKeySaved = vi.fn().mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
    const callbacks = setup({ onKeySaved });
    await act(async () => callbacks.setProvider({ ...provider, groups: [] }));
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.change(input, { target: { value: "saved-before-check" } });
    fireEvent.blur(input);
    await waitFor(() => expect(onKeySaved).toHaveBeenCalledOnce());
    await waitFor(() => expect(input).toBeEnabled());
    expect(input).toHaveValue("");
    await act(async () => finish());
  });
  it("clears a persisted inline key when refreshing fails instead of allowing a duplicate retry", async () => {
    const onReload = vi.fn().mockRejectedValue(new Error("Refresh unavailable"));
    const onKeySaved = vi.fn().mockResolvedValue(undefined);
    const callbacks = setup({ onReload, onKeySaved });
    await act(async () => callbacks.setProvider({ ...provider, groups: [{ ...provider.groups[0], keys: [], apiKeyConfigured: false }] }));
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.change(input, { target: { value: "fixture-first-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(input).toHaveValue(""));
    expect(await screen.findByText("modelProvider.workspace.savedRefreshFailed")).toBeVisible();
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    fireEvent.blur(input);
    expect(mocks.addKey).toHaveBeenCalledTimes(1);
    expect(onKeySaved).not.toHaveBeenCalled();
  });
  it("keeps an in-flight inline key attached to its original supplier after selection changes", async () => {
    let finish!: (value: unknown) => void;
    mocks.addKey.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    const other = { ...provider, id: "other", name: "Other supplier", isCustom: true, groups: [] };
    const onKeySaved = vi.fn().mockResolvedValue(undefined);
    const callbacks = setup({ providers: [provider, other], onKeySaved });
    await act(async () => callbacks.setProvider({ ...provider, groups: [{ ...provider.groups[0], keys: [], apiKeyConfigured: false }] }));
    const navigation = within(screen.getByRole("navigation", { name: "modelProvider.workspace.suppliers" }));
    fireEvent.click(navigation.getByRole("button", { name: /^OpenAI/ }));
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.change(input, { target: { value: "fixture-original-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(mocks.addKey).toHaveBeenCalledTimes(1));
    fireEvent.click(navigation.getByRole("button", { name: "Other supplier" }));
    await act(async () => finish({ data: { success: true } }));
    expect(mocks.addKey).toHaveBeenCalledWith({ modelProviderId: "openai", groupId: "primary" }, { data: { api_key: "fixture-original-key" }, timeout: 180000 });
    expect(screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" })).toHaveValue("");
    expect(mocks.createGroup).not.toHaveBeenCalled();
    expect(onKeySaved).not.toHaveBeenCalled();
  });
  it("reports a failed inline save for the original supplier after switching away", async () => {
    let fail!: (error: Error) => void;
    mocks.addKey.mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject; }));
    const other = { ...provider, id: "other", name: "Other supplier", isCustom: true, groups: [] };
    const callbacks = setup({ providers: [provider, other] });
    await act(async () => callbacks.setProvider({ ...provider, groups: [{ ...provider.groups[0], keys: [], apiKeyConfigured: false }] }));
    const navigation = within(screen.getByRole("navigation", { name: "modelProvider.workspace.suppliers" }));
    fireEvent.click(navigation.getByRole("button", { name: /^OpenAI/ }));
    fireEvent.change(screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" }), { target: { value: "fixture-original-key" } });
    fireEvent.blur(screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" }));
    await waitFor(() => expect(mocks.addKey).toHaveBeenCalledTimes(1));
    fireEvent.click(navigation.getByRole("button", { name: "Other supplier" }));
    await act(async () => fail(new Error("Fixture key rejected")));
    expect(await screen.findByText("OpenAI: Fixture key rejected")).toBeVisible();
    expect(screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" })).toHaveValue("");
    expect(screen.queryByDisplayValue("fixture-original-key")).not.toBeInTheDocument();
    expect(callbacks.onReload).not.toHaveBeenCalled();
  });
  it.each([
    ["", "apiKeyRequired"],
    ["   ", "apiKeyRequired"],
    ["secret with spaces", "apiKeyNoSpaces"],
    ["x".repeat(513), "apiKeyMax"],
  ])("blocks an invalid supplier key before submitting: %s", async (apiKey, error) => {
    setup();
    const dialog = await openAddKeyDialog();
    fireEvent.change(dialog.getByLabelText("modelProvider.apiKeyLabel"), {
      target: { value: apiKey },
    });
    fireEvent.click(
      dialog.getByRole("button", {
        name: "modelProvider.workspace.addAndCheckKey",
      }),
    );
    expect(
      (await dialog.findAllByText(`modelProvider.validation.${error}`)).length,
    ).toBeGreaterThan(0);
    expect(mocks.addKey).not.toHaveBeenCalled();
    expect(mocks.createGroup).not.toHaveBeenCalled();
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
  it("resets a discarded endpoint before leaving supplier settings", async () => {
    setup({}, true);
    const endpoint = screen.getByRole("textbox", {
      name: "modelProvider.apiAddressLabel",
    });
    fireEvent.change(endpoint, {
      target: { value: "https://unsaved.example.test/v1" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.back" }),
    );
    fireEvent.click(await screen.findByText("settingsPage.unsaved.stay"));
    expect(endpoint).toHaveValue("https://unsaved.example.test/v1");
    expect(
      screen.queryByText("Settings overview open"),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.back" }),
    );
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    expect(
      await screen.findByText("Settings overview open"),
    ).toBeInTheDocument();
    expect(endpoint).toHaveValue(provider.baseUrl);
    fireEvent.change(endpoint, {
      target: { value: "https://second-unsaved.example.test" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.workspace.back" }),
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
  it("removes a catalog model directly without confirmation", () => {
    const callbacks = setup();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.deleteModelAria" }),
    );
    expect(screen.queryByRole("button", { name: "common.delete" })).not.toBeInTheDocument();
    expect(callbacks.onDeleteModel).toHaveBeenCalledWith(
      provider,
      provider.groups[0],
      provider.groups[0].models[0],
    );
  });
  it("defaults to the OpenAI supplier when a plan shares its runtime protocol", async () => {
    setup({
      providers: [
        {
          ...provider,
          id: "sense-token",
          name: "SenseNovaTokenPlan",
          brandKey: "SenseNova",
          serviceKey: "token",
          protocolName: "OpenAI",
          baseUrl: "https://token.sensenova.cn/v1/",
          groups: [],
        },
        provider,
      ],
    });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.addSupplier" }));
    const dialog = within(await screen.findByRole("dialog"));
    expect(dialog.getByRole("textbox", { name: "modelProvider.apiAddressLabel" })).toHaveValue(provider.baseUrl);
    fireEvent.change(dialog.getByLabelText("modelProvider.workspace.supplierName"), { target: { value: "My gateway" } });
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(mocks.createProvider).toHaveBeenCalledWith({
      name: "My gateway",
      source_provider_id: "openai",
      base_url: provider.baseUrl,
    }));
  });

  it("creates a custom supplier using a built-in API protocol", async () => {
    setup({
      providers: [
        provider,
        { ...provider, id: "custom", name: "Private proxy", isCustom: true },
      ],
      configuredProviders: [provider],
    });
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.addSupplier",
      }),
    );
    const dialog = within(await screen.findByRole("dialog"));
    fireEvent.change(
      dialog.getByLabelText("modelProvider.workspace.supplierName"),
      { target: { value: "My gateway" } },
    );
    fireEvent.change(
      dialog.getByRole("textbox", { name: "modelProvider.apiAddressLabel" }),
      {
        target: { value: "https://custom.example.test/v1" },
      },
    );
    fireEvent.mouseDown(dialog.getByRole("combobox"));
    expect(
      screen.queryByText("Private proxy", {
        selector: ".ant-select-item-option-content",
      }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      await screen.findByText("OpenAI", {
        selector: ".ant-select-item-option-content",
      }),
    );
    fireEvent.change(
      dialog.getByRole("textbox", { name: "modelProvider.apiAddressLabel" }),
      {
        target: { value: "https://custom.example.test/v1" },
      },
    );
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() =>
      expect(mocks.createProvider).toHaveBeenCalledWith({
        name: "My gateway",
        source_provider_id: "openai",
        base_url: "https://custom.example.test/v1",
      }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });
  it("edits a custom supplier's name without changing its protocol", async () => {
    const custom = {
      ...provider,
      id: "custom",
      name: "Private proxy",
      isCustom: true,
      protocolName: "OpenAI",
    };
    setup({ providers: [custom], configuredProviders: [custom] });
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.editSupplier",
      }),
    );
    const dialog = within(await screen.findByRole("dialog"));
    expect(dialog.queryByRole("combobox")).not.toBeInTheDocument();
    fireEvent.change(
      dialog.getByLabelText("modelProvider.workspace.supplierName"),
      { target: { value: "Renamed gateway" } },
    );
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() =>
      expect(mocks.updateProvider).toHaveBeenCalledWith(
        "custom",
        "Renamed gateway",
      ),
    );
  });
  it("checks fresh embedding impact before confirming custom supplier deletion", async () => {
    const custom = {
      ...provider,
      id: "custom",
      name: "Private proxy",
      isCustom: true,
    };
    setup({ providers: [custom], configuredProviders: [custom] });
    mocks.getModels.mockResolvedValue({
      data: { models: [{ model_type: "embed" }] },
    });
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.deleteSupplier",
      }),
    );
    expect(
      await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc"),
    ).toBeInTheDocument();
    expect(mocks.deleteProvider).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() =>
      expect(mocks.deleteProvider).toHaveBeenCalledWith("custom", true),
    );
  });
  it("configures the first key through management for an unconfigured supplier", async () => {
    const callbacks = setup({ configuredProviders: [] });
    const dialog = await openAddKeyDialog();
    expect(
      dialog.queryByLabelText("modelProvider.groupName"),
    ).not.toBeInTheDocument();
    fireEvent.change(dialog.getByLabelText("modelProvider.apiKeyLabel"), {
      target: { value: "first-key" },
    });
    fireEvent.click(
      dialog.getByRole("button", {
        name: "modelProvider.workspace.addAndCheckKey",
      }),
    );
    await waitFor(() =>
      expect(mocks.createGroup).toHaveBeenCalledWith(
        {
          modelProviderId: "openai",
          createModelProviderGroupOpenAPIRequest: {
            name: "OpenAI",
            api_key: "first-key",
            verify: false,
          },
        },
        { timeout: 180000 },
      ),
    );
    await waitFor(() => expect(callbacks.onReload).toHaveBeenCalled());
    expect(mocks.addKey).not.toHaveBeenCalled();
  });
  it("starts the separate check workflow after saving an unverified first key", async () => {
    mocks.createGroup.mockResolvedValueOnce({ data: { id: "new-private-record", is_verified: false } });
    const onKeySaved = vi.fn().mockResolvedValue(undefined);
    const callbacks = setup({ onKeySaved });
    await act(async () => callbacks.setProvider({ ...provider, groups: [] }));
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.change(input, { target: { value: "fixture-first-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(callbacks.onReload).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(input).toHaveValue(""));
    await waitFor(() => expect(onKeySaved).toHaveBeenCalledWith(expect.objectContaining({ id: "openai" }), "new-private-record", expect.any(Function)));
  });
});
