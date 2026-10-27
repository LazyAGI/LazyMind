import { useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getProvidersWithGroups: vi.fn(),
  getGroups: vi.fn(),
  getModels: vi.fn(),
  translate: (key: string, options?: { name?: string }) =>
    options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(),
  getCredentialRestoreDiscovery: vi.fn(),
  getCredentialRestoreOperation: vi.fn(),
  setCredentialBackupEnabled: vi.fn(),
  startCredentialRestore: vi.fn(),
  cancelCredentialRestore: vi.fn(),
}));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: vi
    .fn()
    .mockResolvedValue({ configured: false, state: "signed_out" }),
  isCloudBusinessAvailable: () => false,
  beginCloudLogin: vi.fn(),
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(),
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
  default: () => null,
}));

vi.mock("../components/ProviderWorkspace", () => ({
  ProviderWorkspace: (props: {
    configuredProviders: import("./ModelProvidersPage").AddedProvider[];
    onLoadModels: (
      provider: import("./ModelProvidersPage").AddedProvider,
      group: import("./ModelProvidersPage").ProviderConnectionGroup,
    ) => void;
    onReload: () => Promise<void>;
  }) => {
    const [reloaded, setReloaded] = useState(0);
    const provider = props.configuredProviders[0];
    const group = provider?.groups[0];
    return (
      <>
        <button
          disabled={!group}
          onClick={() => props.onLoadModels(provider, group)}
        >
          Load models
        </button>
        <button
          disabled={!group}
          onClick={async () => {
            await props.onReload();
            setReloaded((n) => n + 1);
          }}
        >
          Reload settings
        </button>
        <output data-testid="reload-count">{reloaded}</output>
        <output data-testid="group">{JSON.stringify(group)}</output>
      </>
    );
  },
}));
const supplier = {
  id: "supplier",
  name: "OpenAI",
  base_url: "https://example.test/v1",
};
const initialGroup = {
  id: "group",
  name: "Supplier",
  base_url: supplier.base_url,
  has_api_key: true,
  is_verified: true,
  keys: [{ id: "old", masked: "********old", enabled: true }],
};
const apiModels = [
  {
    id: "vector",
    name: "text-vector",
    model_type: "embed",
    is_default: true,
    max_input_tokens: "8192",
  },
  {
    id: "chat",
    name: "dialogue",
    model_type: "llm",
    is_default: false,
    max_input_tokens: "1M",
  },
  {
    id: "visual",
    name: "vision-dialogue",
    model_type: "vlm",
    is_default: true,
    vision: true,
    max_input_tokens: "200000",
  },
];
const state = () =>
  JSON.parse(screen.getByTestId("group").textContent || "null");
beforeEach(() => {
  vi.clearAllMocks();
  mocks.getProviders.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.getProvidersWithGroups.mockResolvedValue({
    data: { providers: [supplier] },
  });
  mocks.getGroups.mockResolvedValue({ data: { groups: [initialGroup] } });
  mocks.getModels.mockResolvedValue({ data: { models: apiModels } });
});
function draw() {
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <ModelProviderPage />
    </ConfigProvider>,
  );
}
describe("provider metadata reloads", () => {
  it("preserves model types, built-in identity and context when settings reload after a key action", async () => {
    draw();
    const load = await screen.findByRole("button", { name: "Load models" });
    await waitFor(() => expect(load).toBeEnabled());
    fireEvent.click(load);
    await waitFor(() => expect(state().models).toHaveLength(3));
    const before = state().models;
    fireEvent.click(screen.getByRole("button", { name: "Reload settings" }));
    await waitFor(() =>
      expect(screen.getByTestId("reload-count")).toHaveTextContent("1"),
    );
    expect(state().models).toEqual(before);
    expect(
      state().models.map((model: { capability: string }) => model.capability),
    ).toEqual(["LLM_CHAT", "EMBEDDING", "VLM"]);
  });
  it("keeps newly loaded key metadata when an older model request finishes afterward", async () => {
    let finish!: (result: { data: { models: typeof apiModels } }) => void;
    mocks.getModels.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    draw();
    const load = await screen.findByRole("button", { name: "Load models" });
    await waitFor(() => expect(load).toBeEnabled());
    fireEvent.click(load);
    await waitFor(() => expect(mocks.getModels).toHaveBeenCalledOnce());
    const nextGroup = {
      ...initialGroup,
      is_verified: false,
      keys: [{ id: "new", masked: "********new", enabled: true }],
    };
    mocks.getGroups.mockResolvedValue({ data: { groups: [nextGroup] } });
    fireEvent.click(screen.getByRole("button", { name: "Reload settings" }));
    await waitFor(() =>
      expect(screen.getByTestId("reload-count")).toHaveTextContent("1"),
    );
    expect(state().keys[0].id).toBe("new");
    await act(async () => finish({ data: { models: apiModels } }));
    await waitFor(() => expect(state().models).toHaveLength(3));
    expect(state().keys).toEqual(nextGroup.keys);
    expect(state().verified).toBe(false);
  });
});
