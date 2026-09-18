import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Modal } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  groups: vi.fn(), patch: vi.fn(), remove: vi.fn(), select: vi.fn(),
  t: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));
const provider = { id: "mineru", name: "MinerU", category: "ocr", is_configured: true, base_url: "https://personal.example/api" };
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: mocks.t, i18n: { language: "en" } }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code, getLocalizedErrorMessage: () => "request failed" }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: async () => ({ data: { providers: [provider] } }),
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.groups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch: mocks.patch,
    apiCoreModelProvidersSelectedProvidersPut: mocks.select,
  },
  modelProvidersDefaultApi: { apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete: mocks.remove },
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));
vi.mock("../components/ToolManagementSection", () => ({ default: () => null }));
vi.mock("../components/DependencyInstallSection", () => ({ default: () => null }));
vi.mock("@/utils/developerMode", () => ({ DEVELOPER_ACTIVE_EVENT: "fixture-developer-mode", isDeveloperModeActive: () => false }));
import ExternalServicesPage from "./ExternalServicesPage";
import ExternalServiceConfigModal from "../components/ExternalServiceConfigModal";

const keys = [{ id: "key-id-one", masked: "********...one1" }, { id: "key-id-two", masked: "********...two2" }];
const group = { id: "group-one", has_api_key: true, is_verified: true, base_url: provider.base_url, keys };
let confirmation: Parameters<typeof Modal.confirm>[0];
beforeEach(() => {
  vi.clearAllMocks();
  mocks.groups.mockResolvedValue({ data: { groups: [group] } });
  mocks.patch.mockResolvedValue({ data: { id: group.id, base_url: "https://new.example/api" } });
  vi.spyOn(Modal, "confirm").mockImplementation((options: Parameters<typeof Modal.confirm>[0]) => {
    confirmation = options;
    return { destroy: vi.fn(), update: vi.fn() };
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

async function openPage() {
  render(<ExternalServicesPage includeMcp={false} includeDependencies={false} includeBuiltinTools={false} />);
  fireEvent.click(await screen.findByRole("button", { name: "modelProvider.external.configModalTitle:MinerU" }));
  await screen.findByText(keys[0].masked);
}

describe("personal external service credentials without Cloud login", () => {
  it("shows stored key metadata and removes only the selected key by ID", async () => {
    render(<ExternalServiceConfigModal open service={{ ...provider, category: "parsing", key: provider.id, description: "", fields: ["apiKey"], logo: null, logoUrl: "", tone: "blue", status: "configured" }} onClose={() => {}} />);
    expect(await screen.findByText(keys[0].masked)).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.external.noKeysConfigured")).toBeNull();
    expect(screen.getByText("modelProvider.external.status.configured")).toBeInTheDocument();
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...group, keys: [keys[1]] }] } });
    fireEvent.click(screen.getAllByRole("button", { name: "common.delete", hidden: true })[0]);
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith({ modelProviderId: provider.id, groupId: group.id }, { data: { key_id: keys[0].id } }));
    await waitFor(() => expect(screen.queryByText(keys[0].masked)).toBeNull());
    expect(screen.getByText(keys[1].masked)).toBeInTheDocument();
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...group, has_api_key: false, keys: [] }] } });
    fireEvent.click(screen.getByRole("button", { name: "common.delete", hidden: true }));
    await screen.findByText("modelProvider.external.noKeysConfigured");
    expect(screen.getByText("modelProvider.external.status.missing")).toBeInTheDocument();
  });

  it.each([false, true])("requires confirmation before changing a URL with saved keys (confirm=%s)", async (accept) => {
    await openPage();
    const input = screen.getByDisplayValue(provider.base_url);
    fireEvent.change(input, { target: { value: "https://new.example/api" } });
    fireEvent.blur(input);
    expect(mocks.patch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("modelProvider.external.saveConfig"));
    await waitFor(() => expect(Modal.confirm).toHaveBeenCalledTimes(1));
    expect(mocks.patch).not.toHaveBeenCalled();
    await act(async () => { if (accept) confirmation.onOk?.(); else confirmation.onCancel?.(); });
    if (accept) {
      await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    } else {
      expect(mocks.patch).not.toHaveBeenCalled();
      expect(screen.getByText(keys[0].masked)).toBeInTheDocument();
    }
  });

  it("does not treat a failed key lookup as an empty group that can be overwritten", async () => {
    mocks.groups.mockRejectedValue(new Error("fixture unavailable"));
    render(<ExternalServicesPage includeMcp={false} includeDependencies={false} includeBuiltinTools={false} />);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.external.configModalTitle:MinerU" }));
    await screen.findByText("2000509");
    expect(screen.getByText("modelProvider.external.saveConfig").closest("button")).toBeDisabled();
    expect(mocks.patch).not.toHaveBeenCalled();
  });
});
