import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  providers: [] as Array<Record<string, unknown>>,
  getProviders: vi.fn(),
  groups: vi.fn(),
  create: vi.fn(),
  patch: vi.fn(),
  addKey: vi.fn(),
  selected: vi.fn(),
  select: vi.fn(),
  t: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: mocks.t, i18n: { language: "en" } }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code, getLocalizedErrorMessage: () => "request failed" }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.groups,
    apiCoreModelProvidersModelProviderIdGroupsPost: mocks.create,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch: mocks.patch,
    apiCoreModelProvidersSelectedProvidersGet: mocks.selected,
    apiCoreModelProvidersSelectedProvidersPut: mocks.select,
  },
  modelProvidersDefaultApi: { apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost: mocks.addKey },
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));
vi.mock("../components/ToolManagementSection", () => ({ default: () => null }));
vi.mock("../components/DependencyInstallSection", () => ({ default: () => null }));
vi.mock("@/utils/developerMode", () => ({ DEVELOPER_ACTIVE_EVENT: "fixture-developer-mode", isDeveloperModeActive: () => false }));

import ExternalServicesPage from "./ExternalServicesPage";

const provider = (id: string, name: string, capabilities: string[] = []) => ({
  id, name, category: "translation", capabilities, base_url: `https://${id}.example/`, is_configured: false,
});

beforeEach(() => {
  vi.clearAllMocks();
  mocks.providers = [];
  mocks.getProviders.mockImplementation(async (params: { category?: string }) => ({
    data: { providers: params.category === "datasource" ? [] : mocks.providers },
  }));
  mocks.groups.mockResolvedValue({ data: { groups: [] } });
  mocks.create.mockResolvedValue({ data: { id: "group-new" } });
  mocks.patch.mockResolvedValue({ data: { id: "group-existing" } });
  mocks.selected.mockResolvedValue({ data: { selections: [] } });
  mocks.select.mockResolvedValue({ data: { selections: [] } });
});
afterEach(() => cleanup());

async function openProvider(name: string) {
  render(<ExternalServicesPage includeMcp={false} includeDependencies={false} includeBuiltinTools={false} visibleCategories={["translation"]} />);
  fireEvent.click(await screen.findByRole("button", { name: `modelProvider.external.configModalTitle:${name}` }));
  await waitFor(() => expect(mocks.groups).toHaveBeenCalled());
}

describe("translation service configuration", () => {
  it("enables a keyless translation source without requesting a credential", async () => {
    mocks.providers = [provider("google-web", "Google Translate Web", ["keyless_translation"])];
    await openProvider("Google Translate Web");
    expect(screen.queryByPlaceholderText("modelProvider.external.keyPlaceholder")).toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.external.enableTranslationSource" }));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith(
      expect.objectContaining({ createModelProviderGroupOpenAPIRequest: expect.objectContaining({
        name: "Google Translate Web", api_key: "", verify: false,
      }) }),
      undefined,
    ));
    await waitFor(() => expect(mocks.select).toHaveBeenCalledWith({
      setSelectedProviderOpenAPIRequest: { selections: [{ category: "translation", group_id: "group-new" }] },
    }));
  });

  it("stores Baidu's APP ID and secret together without a model check", async () => {
    mocks.providers = [provider("baidu", "Baidu Translation")];
    await openProvider("Baidu Translation");
    fireEvent.change(screen.getByPlaceholderText("modelProvider.external.baiduAppIdPlaceholder"), { target: { value: "app-123" } });
    fireEvent.change(screen.getByPlaceholderText("modelProvider.external.baiduSecretPlaceholder"), { target: { value: "secret-abc" } });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.external.saveTranslationCredential" }));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith(
      expect.objectContaining({ createModelProviderGroupOpenAPIRequest: expect.objectContaining({
        api_key: "app-123|secret-abc", verify: false,
      }) }),
      expect.anything(),
    ));
    expect(mocks.addKey).not.toHaveBeenCalled();
  });

  it("replaces an existing single-key translation credential without using AddKey", async () => {
    mocks.providers = [provider("deepl", "DeepL")];
    mocks.groups.mockResolvedValue({ data: { groups: [{ id: "group-existing", base_url: "https://deepl.example/", has_api_key: true, keys: [{ id: "old", masked: "********old" }] }] } });
    await openProvider("DeepL");
    fireEvent.change(screen.getByPlaceholderText("modelProvider.external.keyPlaceholder"), { target: { value: "new-deepl-key" } });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.external.saveTranslationCredential" }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith(expect.objectContaining({
      updateModelProviderGroupOpenAPIRequest: expect.objectContaining({ api_key: "new-deepl-key", verify: false }),
    })));
    expect(mocks.addKey).not.toHaveBeenCalled();
    await waitFor(() => expect(mocks.select).toHaveBeenCalled());
  });

  it("reselects a saved credentialed source without replacing its secret and updates the active marker", async () => {
    mocks.providers = [provider("deepl", "DeepL"), provider("google-cloud", "Google Cloud Translation")];
    mocks.groups.mockResolvedValue({ data: { groups: [{
      id: "group-deepl", base_url: "https://deepl.example/", has_api_key: true,
      keys: [{ id: "stored", masked: "********old" }],
    }] } });
    mocks.selected.mockResolvedValue({ data: { selections: [{
      category: "translation", group_id: "group-google", user_model_provider_id: "google-cloud",
      provider_name: "Google Cloud Translation", group_name: "Google Cloud Translation", base_url: "https://google-cloud.example/", share: false,
    }] } });

    await openProvider("DeepL");
    const googleCard = screen.getByRole("button", { name: "modelProvider.external.configModalTitle:Google Cloud Translation" });
    await waitFor(() => expect(within(googleCard).getByText("modelProvider.external.translationSelected")).toBeInTheDocument());
    const selectButton = await screen.findByRole("button", { name: "modelProvider.external.selectTranslationSource" });
    fireEvent.click(selectButton);

    await waitFor(() => expect(mocks.select).toHaveBeenCalledWith({
      setSelectedProviderOpenAPIRequest: { selections: [{ category: "translation", group_id: "group-deepl" }] },
    }));
    expect(mocks.patch).not.toHaveBeenCalled();
    const deepLCard = screen.getByRole("button", { name: "modelProvider.external.configModalTitle:DeepL" });
    await waitFor(() => expect(within(deepLCard).getByText("modelProvider.external.translationSelected")).toBeInTheDocument());
    expect(within(googleCard).queryByText("modelProvider.external.translationSelected")).toBeNull();
  });

  it("keeps the Azure resource region visible while entering credentials", async () => {
    mocks.providers = [provider("microsoft", "Microsoft Translator")];
    await openProvider("Microsoft Translator");
    expect(screen.getByPlaceholderText("modelProvider.external.microsoftRegionPlaceholder")).toHaveAttribute("type", "text");
  });

  it("keeps the Google Custom Search Engine ID visible", async () => {
    mocks.providers = [{ id: "google-search", name: "Google Custom Search", category: "search", base_url: "https://customsearch.googleapis.com/", is_configured: false }];
    render(<ExternalServicesPage includeMcp={false} includeDependencies={false} includeBuiltinTools={false} />);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.external.configModalTitle:Google Custom Search" }));
    await waitFor(() => expect(mocks.groups).toHaveBeenCalled());
    expect(screen.getByPlaceholderText("modelProvider.external.googleSearchEngineIdPlaceholder")).toHaveAttribute("type", "text");
  });
});
