import { fireEvent, render, screen, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getProvidersWithGroups: vi.fn(),
  getGroups: vi.fn(),
  translate: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
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
  getCloudSession: vi.fn().mockResolvedValue({ configured: false, state: "signed_out" }),
  isCloudBusinessAvailable: () => false,
  beginCloudLogin: vi.fn(),
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(),
  closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(),
  openCloudTokenPlan: vi.fn(),
}));
vi.mock("../components/CredentialBackupPanel", () => ({ CredentialBackupPanel: () => null }));
vi.mock("../components/CredentialRestorePanel", () => ({ CredentialRestorePanel: () => null }));
vi.mock("../components/CloudSystemProviderCard", () => ({ default: () => null }));

const provider = {
  id: "fixture-provider",
  name: "OpenAI",
  base_url: "https://api.example.test/v1",
  capabilities: ["LLM_CHAT"],
};
const group = {
  id: "fixture-group",
  name: "Fixture",
  base_url: provider.base_url,
  is_verified: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getGroups.mockResolvedValue({ data: { groups: [group] } });
});

describe("custom model name controls", () => {
  it("keeps the clear control inside the input and the remote search as a separate button", async () => {
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ModelProviderPage />
      </ConfigProvider>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.customModel" }));
    const dialogElement = await screen.findByRole("dialog");
    const dialog = within(dialogElement);
    const modelName = dialog.getByPlaceholderText("modelProvider.modelNamePlaceholder");
    fireEvent.change(modelName, { target: { value: "fixture-model" } });

    expect(dialog.getByRole("button", { name: "modelProvider.fetchAvailableModels" })).toBeInTheDocument();
    expect(dialogElement.querySelector(".ant-input-clear-icon")).toBeInTheDocument();
    expect(dialogElement.querySelector(".ant-select-clear")).not.toBeInTheDocument();
  });
});
