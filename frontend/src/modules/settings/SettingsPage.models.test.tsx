import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  default: ({ onBack }: { onBack: () => void }) => (
    <div>
      <nav aria-label="suppliers">Existing suppliers</nav>
      <button onClick={onBack}>Back to settings</button>
    </div>
  ),
}));
vi.mock("@/modules/modelProvider/pages/DefaultServicesPage", () => ({
  default: ({ onConfigureProviders }: { onConfigureProviders: () => void }) => (
    <div>
      <span>Default model choices</span>
      <button onClick={onConfigureProviders}>Configure providers</button>
    </div>
  ),
}));

vi.mock("./QuickModelSettings", () => ({
  default: () => <span>Quick model defaults</span>,
}));

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
  const renderAt = (url: string) =>
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <MemoryRouter initialEntries={[url]}>
          <SettingsPage />
        </MemoryRouter>
      </ConfigProvider>,
    );
  it("keeps model services independent and returns to parallel settings entries", async () => {
    renderAt("/settings?section=models");
    expect(
      await screen.findByRole("navigation", { name: "suppliers" }),
    ).toBeVisible();
    expect(screen.queryByText("Default model choices")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Default models" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back to settings" }));
    const nav = within(
      await screen.findByRole("navigation", { name: "settingsPage.navAria" }),
    );
    expect(
      nav.getByRole("button", { name: /settingsPage.sections.models/ }),
    ).toBeInTheDocument();
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
    expect(
      screen.queryByRole("navigation", { name: "suppliers" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Configure providers" }),
    );
    expect(
      await screen.findByRole("navigation", { name: "suppliers" }),
    ).toBeVisible();
    await waitFor(() =>
      expect(
        screen.queryByText("Default model choices"),
      ).not.toBeInTheDocument(),
    );
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
});
