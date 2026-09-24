import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(), getProvidersWithGroups: vi.fn(), getGroups: vi.fn(), createGroup: vi.fn(),
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
    apiCoreModelProvidersModelProviderIdGroupsPost: mocks.createGroup,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(), getCredentialRestoreDiscovery: vi.fn(),
  getCredentialRestoreOperation: vi.fn(), setCredentialBackupEnabled: vi.fn(),
  startCredentialRestore: vi.fn(), cancelCredentialRestore: vi.fn(),
}));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: vi.fn().mockResolvedValue({ configured: false, state: "signed_out" }),
  isCloudBusinessAvailable: () => false, beginCloudLogin: vi.fn(),
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(), closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(), openCloudTokenPlan: vi.fn(),
}));
vi.mock("../components/CredentialBackupPanel", () => ({ CredentialBackupPanel: () => null }));
vi.mock("../components/CredentialRestorePanel", () => ({ CredentialRestorePanel: () => null }));
vi.mock("../components/CloudSystemProviderCard", () => ({ default: () => null }));

const openAI = { id: "fixture-provider", name: "OpenAI", base_url: "https://api.example.test/v1/" };
const customUrl = "https://custom.example.test/v1/";
const classicLabel = "modelProvider.sensenovaClassicMode";
const customLabel = "modelProvider.baseUrlCustomOption";

async function openConfig(provider = openAI, existingBaseUrl?: string) {
  mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: existingBaseUrl ? [provider] : [] } });
  mocks.getGroups.mockResolvedValue({ data: { groups: [{ id: "fixture-group", name: "Fixture", base_url: existingBaseUrl }] } });
  render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
  fireEvent.click(await screen.findByRole("button", {
    name: existingBaseUrl ? /common.edit/ : /modelProvider.configureAndAdd/,
  }));
  return within(await screen.findByRole("dialog"));
}

function selectMode(label: string) {
  fireEvent.mouseDown(screen.getByRole("combobox"));
  fireEvent.click(screen.getByText(label, { selector: ".ant-select-item-option-content" }));
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.createGroup.mockResolvedValue({ data: { id: "saved-fixture", name: "OpenAI", base_url: customUrl, is_verified: false } });
});

describe("provider Base URL modes", () => {
  it("offers OpenAI classic and custom modes with the existing validation and save contract", async () => {
    const dialog = await openConfig();
    expect(dialog.getByText(classicLabel)).toBeInTheDocument();
    expect(dialog.getByRole("textbox", { name: "Base URL" })).toHaveValue(openAI.base_url);
    fireEvent.click(dialog.getByRole("button", { name: "modelProvider.saveConfig" }));
    expect(await dialog.findByText("modelProvider.validation.apiKeyRequired")).toBeInTheDocument();
    expect(mocks.createGroup).not.toHaveBeenCalled();

    selectMode(customLabel);
    expect(dialog.getByText(customLabel)).toBeInTheDocument();
    const baseUrlInput = dialog.getByRole("textbox", { name: "Base URL" });
    expect(baseUrlInput).toHaveValue("");
    expect(dialog.getByText("modelProvider.apiKeyCustomExtra")).toBeInTheDocument();
    fireEvent.click(dialog.getByRole("button", { name: "modelProvider.saveConfig" }));
    expect(await dialog.findByText("modelProvider.validation.baseUrlRequired")).toBeInTheDocument();
    fireEvent.change(baseUrlInput, { target: { value: "invalid-url" } });
    fireEvent.click(dialog.getByRole("button", { name: "modelProvider.saveConfig" }));
    expect(await dialog.findByText("modelProvider.validation.baseUrlInvalid")).toBeInTheDocument();
    expect(mocks.createGroup).not.toHaveBeenCalled();

    fireEvent.change(baseUrlInput, { target: { value: customUrl } });
    fireEvent.click(dialog.getByRole("button", { name: "modelProvider.saveConfig" }));
    await waitFor(() => expect(mocks.createGroup).toHaveBeenCalledWith({
      modelProviderId: openAI.id,
      createModelProviderGroupOpenAPIRequest: { name: "OpenAI", base_url: customUrl, verify: false },
    }, undefined));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the mode in sync with direct URL edits and restores the classic URL", async () => {
    const dialog = await openConfig();
    const baseUrlInput = dialog.getByRole("textbox", { name: "Base URL" });
    fireEvent.change(baseUrlInput, { target: { value: customUrl } });
    expect(dialog.getByText(customLabel)).toBeInTheDocument();
    selectMode(classicLabel);
    expect(baseUrlInput).toHaveValue(openAI.base_url);
    expect(dialog.getByText("modelProvider.apiKeyDefaultExtra")).toBeInTheDocument();
    expect(mocks.createGroup).not.toHaveBeenCalled();
  });

  it.each([
    [customUrl, customLabel],
    [openAI.base_url.replace(/\/$/, ""), classicLabel],
  ])("selects the matching mode when editing %s", async (baseUrl, label) => {
    const dialog = await openConfig(openAI, baseUrl);
    expect(dialog.getByText(label)).toBeInTheDocument();
    expect(dialog.getByRole("textbox", { name: "Base URL" })).toHaveValue(baseUrl);
  });

  it("retains SenseNova's classic, Token Plan and custom presets", async () => {
    const provider = { ...openAI, name: "SenseNova", base_url: "https://api.sensenova.cn/compatible-mode/v1/" };
    const dialog = await openConfig(provider);
    expect(dialog.getByText(classicLabel)).toBeInTheDocument();
    selectMode("modelProvider.sensenovaTokenPlanMode");
    expect(dialog.getByRole("textbox", { name: "Base URL" })).toHaveValue("https://token.sensenova.cn/v1/chat/completions/");
    selectMode(customLabel);
    expect(dialog.getByRole("textbox", { name: "Base URL" })).toHaveValue("");
    selectMode(classicLabel);
    expect(dialog.getByRole("textbox", { name: "Base URL" })).toHaveValue(provider.base_url);
  });
});
