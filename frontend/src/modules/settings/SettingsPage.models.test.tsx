import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { useState } from "react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useSettingsDraft } from "./SettingsNavigationGuard";
import SettingsPage from "./index";

const mocks = vi.hoisted(() => ({ overview: vi.fn(), preferences: vi.fn() }));
vi.mock("react-i18next", () => ({
  initReactI18next: { type: "3rdParty", init: vi.fn() },
  useTranslation: () => ({
    i18n: { language: "zh-CN" },
    t: (key: string) => key,
  }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "admin" }) },
}));
vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideEvo: true, hideUserGroupSurfaces: true },
}));
vi.mock("@/runtime/mode", () => ({
  isDesktopRuntime: () => true,
  isLocalRuntime: () => true,
  isVocabularyEnabled: () => false,
}));
vi.mock("./api", () => ({
  fetchSettingsOverview: mocks.overview,
  runSettingsChecks: vi.fn(),
  applySettingsChange: vi.fn(),
}));
vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: mocks.preferences,
  patchUserUiPreferences: vi.fn(),
}));
vi.mock("@/modules/modelProvider/pages/ModelProvidersPage", () => ({
  default: ({ onBack, highlightProviderId }: { onBack?: () => void; highlightProviderId?: string }) => (
    <div>
      <nav aria-label="suppliers" data-provider-id={highlightProviderId}>Existing suppliers</nav>
      <ProviderDraft />
      {onBack ? <button onClick={onBack}>Back to settings</button> : null}
    </div>
  ),
}));
vi.mock("@/modules/modelProvider/pages/DefaultServicesPage", () => ({
  default: ({
    onConfigureProviders,
    onConfigureCloudService,
    focusServiceCategory,
  }: {
    onConfigureProviders: () => void;
    onConfigureCloudService: (service: "cloudParsing" | "searchEngine") => void;
    focusServiceCategory?: string;
  }) => (
    <div data-focus-service={focusServiceCategory}>
      <span>Default model choices</span>
      <button onClick={onConfigureProviders}>Configure providers</button>
      <button onClick={() => onConfigureCloudService("cloudParsing")}>Configure OCR here</button>
      <button onClick={() => onConfigureCloudService("searchEngine")}>Configure search here</button>
    </div>
  ),
}));

vi.mock("./QuickModelSettings", () => ({
  default: () => <span>Quick model defaults</span>,
}));

function ProviderDraft() {
  const [value, setValue] = useState("");
  useSettingsDraft({ dirty: Boolean(value), discard: () => setValue("") });
  return <input aria-label="Provider draft" value={value} onChange={(event) => setValue(event.target.value)} />;
}

function mobileNavigation() {
  // jsdom does not evaluate media queries; expose the mobile control for interaction coverage.
  const selector = screen.getByLabelText("settingsPage.navAria", { selector: "select" });
  selector.parentElement!.style.display = "flex";
  return screen.getByRole("combobox", { name: "settingsPage.navAria" });
}

describe("separate supplier and default model settings", () => {
  beforeEach(() => {
    mocks.overview.mockResolvedValue({
      controls: {},
      sections: [],
      issues: [],
      updated_at: "2026-10-22T00:00:00Z",
    });
    mocks.preferences.mockResolvedValue({});
  });
  const renderAt = (url: string) => {
    const router = createMemoryRouter([{ path: "/settings", element: <SettingsPage /> }], { initialEntries: [url] });
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <RouterProvider router={router} />
      </ConfigProvider>,
    );
    return router;
  };
  it.each(["models", "model_defaults"])("offers all settings in a labeled page selector from %s", async (section) => {
    renderAt(`/settings?section=${section}`);
    await screen.findByText(section === "models" ? "Existing suppliers" : "Default model choices");
    const selector = mobileNavigation();
    expect(selector).toHaveValue(section);
    expect(within(selector).getByRole("group", { name: "settingsPage.navGroups.management" })).toBeInTheDocument();
    expect(within(selector).getByRole("option", { name: "settingsPage.sections.recovery" })).toBeInTheDocument();
    selector.focus();
    expect(selector).toHaveFocus();
    fireEvent.change(selector, { target: { value: section === "models" ? "model_defaults" : "models" } });
    expect(await screen.findByText(section === "models" ? "Default model choices" : "Existing suppliers")).toBeVisible();
  });
  it("keeps the current page and draft when leaving through the page selector is cancelled", async () => {
    const router = renderAt("/settings?section=models&view=providers&return_to=%2Fagent%2Fchat%2Fexample");
    fireEvent.change(await screen.findByLabelText("Provider draft"), { target: { value: "unsaved endpoint" } });
    const selector = mobileNavigation();
    fireEvent.change(selector, { target: { value: "model_defaults" } });
    fireEvent.click(await screen.findByRole("button", { name: "settingsPage.unsaved.stay" }));
    expect(selector).toHaveValue("models");
    expect(screen.getByLabelText("Provider draft")).toHaveValue("unsaved endpoint");
    expect(new URLSearchParams(router.state.location.search).get("section")).toBe("models");

    fireEvent.change(selector, { target: { value: "model_defaults" } });
    fireEvent.click(await screen.findByRole("button", { name: "settingsPage.unsaved.discard" }));
    expect(await screen.findByText("Default model choices")).toBeVisible();
    expect(selector).toHaveValue("model_defaults");
    expect(new URLSearchParams(router.state.location.search).get("return_to")).toBe("/agent/chat/example");
  });
  it("embeds model services beside the persistent settings navigation", async () => {
    renderAt("/settings?section=models");
    expect(
      await screen.findByRole("navigation", { name: "suppliers" }),
    ).toBeVisible();
    expect(screen.queryByText("Default model choices")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Default models" }),
    ).not.toBeInTheDocument();
    const nav = within(
      screen.getByRole("navigation", { name: "settingsPage.navAria" }),
    );
    expect(
      nav.getByRole("button", { name: /settingsPage.sections.models/ }),
    ).toHaveAttribute("aria-current", "page");
    expect(screen.queryByRole("button", {name: "Back to settings"})).not.toBeInTheDocument();
    fireEvent.click(
      nav.getByRole("button", { name: /settingsPage.sections.defaultModels/ }),
    );
    expect(await screen.findByText("Default model choices")).toBeVisible();
    expect(
      screen.queryByRole("navigation", { name: "suppliers" }),
    ).not.toBeInTheDocument();
  });
  it("opens standalone default models and can navigate to model services for setup", async () => {
    renderAt("/settings?section=model_defaults");
    expect(await screen.findByText("Default model choices")).toBeVisible();
    expect(
      screen.getByRole("heading", {
        name: /settingsPage.sections.defaultModels/,
        level: 1,
      }),
    ).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("main", { name: "settingsPage.title" }))
      .toHaveClass("is-default-model-workspace");
    expect(
      screen.queryByRole("navigation", { name: "suppliers" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Configure providers" }),
    );
    expect(
      await screen.findByRole("navigation", { name: "suppliers" }),
    ).toBeVisible();
    expect(screen.getByRole("main", { name: "settingsPage.title" }))
      .not.toHaveClass("is-default-model-workspace");
    await waitFor(() =>
      expect(
        screen.queryByText("Default model choices"),
      ).not.toBeInTheDocument(),
    );
  });
  it.each([
    { url: "/settings?section=models&view=providers", providerId: null },
    { url: "/settings?section=models&view=providers&target=vlm&provider_id=openai", providerId: "openai" },
  ])("opens the supplier page for legacy provider deep links: $url", async ({ url, providerId }) => {
    renderAt(url);
    const suppliers = await screen.findByRole("navigation", { name: "suppliers" });
    expect(suppliers).toBeVisible();
    expect(suppliers.getAttribute("data-provider-id")).toBe(providerId);
    expect(screen.queryByText("Default model choices")).not.toBeInTheDocument();
    const navigation = within(screen.getByRole("navigation", { name: "settingsPage.navAria" }));
    expect(navigation.getByRole("button", { name: /settingsPage.sections.models/ }))
      .toHaveAttribute("aria-current", "page");
  });
  it.each([
    "/settings?section=models&view=defaults",
    "/settings?section=models&target=vlm",
  ])(
    "resolves legacy defaults deep link to the independent page: %s",
    async (url) => {
      renderAt(url);
      expect(await screen.findByText("Default model choices")).toBeVisible();
      expect(
        screen.getByRole("heading", {
          name: /settingsPage.sections.defaultModels/,
          level: 1,
        }),
      ).toBeVisible();
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(
        screen.queryByRole("navigation", { name: "suppliers" }),
      ).not.toBeInTheDocument();
    },
  );
  it.each([
    { tool: "document-parsing", category: "parsing" },
    { tool: "web-search", category: "search" },
    { tool: "academic-search", category: "academic" },
    { tool: "translation", category: "translation" },
  ])("moves the old $tool service link into Default Models", async ({ tool, category }) => {
    const router = renderAt(`/settings?section=knowledge&tool=${tool}`);
    const defaultModels = await screen.findByText("Default model choices");
    expect(defaultModels.parentElement).toHaveAttribute("data-focus-service", category);
    await waitFor(() => {
      const params = new URLSearchParams(router.state.location.search);
      expect(params.get("section")).toBe("model_defaults");
      expect(params.get("service")).toBe(category);
      expect(params.has("tool")).toBe(false);
    });
  });
  it("keeps OCR and search setup in the Default Models page", async () => {
    const router = renderAt("/settings?section=model_defaults");
    await screen.findByText("Default model choices");

    fireEvent.click(screen.getByRole("button", { name: "Configure OCR here" }));
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get("service")).toBe("parsing"));
    expect(screen.getByText("Default model choices")).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: "Configure search here" }));
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get("service")).toBe("search"));
    expect(screen.getByText("Default model choices")).toBeVisible();
  });
});
