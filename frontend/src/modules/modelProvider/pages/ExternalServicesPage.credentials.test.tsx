import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Modal } from "antd";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { SettingsNavigationGuard } from "@/modules/settings/SettingsNavigationGuard";
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
beforeEach(() => {
  vi.clearAllMocks();
  mocks.groups.mockResolvedValue({ data: { groups: [group] } });
  mocks.patch.mockResolvedValue({ data: { id: group.id, base_url: "https://new.example/api" } });
  vi.spyOn(Modal, "confirm").mockReturnValue({ destroy: vi.fn(), update: vi.fn() });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

async function openPage() {
  render(<ExternalServicesPage includeMcp={false} includeDependencies={false} includeBuiltinTools={false} />);
  fireEvent.click(await screen.findByRole("button", { name: "modelProvider.external.configModalTitle:MinerU" }));
  await screen.findByText(keys[0].masked);
}

describe("personal external service credentials without Cloud login", () => {
  it("restores the service editor from the URL and protects changed values on browser back", async () => {
    const router = createMemoryRouter([{ path: "/settings", element: <SettingsNavigationGuard>
      <ExternalServicesPage includeMcp={false} includeDependencies={false} includeBuiltinTools={false} />
    </SettingsNavigationGuard> }], { initialEntries: ["/settings?section=knowledge", "/settings?section=knowledge&editor=service&item=mineru"], initialIndex: 1 });
    render(<RouterProvider router={router} />);
    await screen.findByText(keys[0].masked);
    const input = screen.getByDisplayValue(provider.base_url);
    fireEvent.change(input, { target: { value: "https://draft.example/api" } });
    await act(async () => { await router.navigate(-1); });
    fireEvent.click(await screen.findByText("settingsPage.unsaved.stay"));
    expect(router.state.location.search).toContain("editor=service");
    expect(input).toHaveValue("https://draft.example/api");
    await act(async () => { await router.navigate(-1); });
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    await waitFor(() => expect(router.state.location.search).toBe("?section=knowledge"));
    expect(mocks.patch).not.toHaveBeenCalled();
    await act(async () => { await router.navigate(1); });
    await waitFor(() => expect(screen.getByDisplayValue(provider.base_url)).toBeInTheDocument());
  });

  it("keeps a failed service configuration open with the entered URL", async () => {
    mocks.patch.mockRejectedValueOnce(new Error("offline"));
    await openPage();
    fireEvent.change(screen.getByDisplayValue(provider.base_url), { target: { value: "https://draft.example/api" } });
    fireEvent.click(screen.getByText("modelProvider.external.saveConfig"));
    await screen.findByText("request failed");
    expect(screen.getByDisplayValue("https://draft.example/api")).toBeInTheDocument();
    expect(screen.getByText(keys[0].masked)).toBeInTheDocument();
    expect(Modal.confirm).not.toHaveBeenCalled();
    expect(screen.getByText("modelProvider.external.saveConfig").closest("button")).toBeEnabled();
  });

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

  it("saves a changed address without clearing saved keys or asking for destructive confirmation", async () => {
    const updated = { ...group, base_url: "https://new.example/api", is_verified: false };
    mocks.patch.mockImplementationOnce(async () => {
      mocks.groups.mockResolvedValue({ data: { groups: [updated] } });
      return { data: { id: group.id, base_url: updated.base_url, is_verified: false } };
    });
    await openPage();
    const input = screen.getByDisplayValue(provider.base_url);
    fireEvent.change(input, { target: { value: updated.base_url } });
    fireEvent.blur(input);
    expect(mocks.patch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("modelProvider.external.saveConfig"));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith({
      modelProviderId: provider.id,
      groupId: group.id,
      updateModelProviderGroupOpenAPIRequest: { name: "MinerU", base_url: updated.base_url, verify: false },
    }));
    expect(Modal.confirm).not.toHaveBeenCalled();
    expect(mocks.remove).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("button", { name: "modelProvider.external.saveConfig" })).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.external.configModalTitle:MinerU" }));
    expect(await screen.findByText(keys[0].masked)).toBeInTheDocument();
    expect(screen.getByText(keys[1].masked)).toBeInTheDocument();
    expect(screen.getByDisplayValue(updated.base_url)).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.external.noKeysConfigured")).not.toBeInTheDocument();
  });

  it("retains the service selection when its address returns to the default and existing keys remain", async () => {
    const customAddress = "https://previous.example/api";
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...group, base_url: customAddress }] } });
    mocks.patch.mockResolvedValueOnce({ data: { id: group.id, base_url: provider.base_url, is_verified: false } });
    await openPage();
    fireEvent.change(screen.getByDisplayValue(customAddress), { target: { value: provider.base_url } });
    fireEvent.click(screen.getByText("modelProvider.external.saveConfig"));
    await waitFor(() => expect(mocks.select).toHaveBeenCalledWith({
      setSelectedProviderOpenAPIRequest: { selections: [{ category: "ocr", group_id: group.id }] },
    }));
    expect(Modal.confirm).not.toHaveBeenCalled();
    expect(mocks.remove).not.toHaveBeenCalled();
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
