import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsPage from "./index";

const mocks = vi.hoisted(() => ({ overview: vi.fn(), preferences: vi.fn() }));
vi.mock("react-i18next", () => ({
  initReactI18next: { type: "3rdParty", init: vi.fn() },
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: (key: string) => key }),
}));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({ role: "admin" }) } }));
vi.mock("@/runtime/features", () => ({ runtimeFeatures: { hideEvo: true, hideUserGroupSurfaces: true } }));
vi.mock("@/runtime/mode", () => ({ isDesktopRuntime: () => true, isLocalRuntime: () => true, isVocabularyEnabled: () => false }));
vi.mock("./api", () => ({ fetchSettingsOverview: mocks.overview, runSettingsChecks: vi.fn(), applySettingsChange: vi.fn() }));
vi.mock("@/modules/user/uiPreferencesApi", () => ({ fetchUserUiPreferences: mocks.preferences, patchUserUiPreferences: vi.fn() }));
vi.mock("@/modules/modelProvider/pages/ModelProvidersPage", () => ({
  default: ({ onConfigureDefaults, onBack }: { onConfigureDefaults: () => void; onBack: () => void }) => <div>
    <nav aria-label="suppliers">Existing suppliers</nav>
    <button onClick={onConfigureDefaults}>Default models</button>
    <button onClick={onBack}>Back to settings</button>
  </div>,
}));
vi.mock("@/modules/modelProvider/pages/DefaultServicesPage", () => ({
  default: ({ onConfigureProviders }: { onConfigureProviders: () => void }) => <div>
    <span>Default model choices</span>
    <button onClick={onConfigureProviders}>Configure providers</button>
  </div>,
}));

describe("supplier workspace navigation", () => {
  beforeEach(() => {
    mocks.overview.mockResolvedValue({ controls: {}, sections: [], issues: [], updated_at: "2026-10-22T00:00:00Z" });
    mocks.preferences.mockResolvedValue({});
  });
  const renderAt = (url: string) => render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[url]}><SettingsPage /></MemoryRouter></ConfigProvider>);

  it("opens suppliers directly and preserves the workspace while changing defaults", async () => {
    renderAt("/settings?section=models");
    expect(await screen.findByRole("navigation", { name: "suppliers" })).toBeVisible();
    expect(screen.queryByRole("navigation", { name: "settingsPage.navAria" })).not.toBeInTheDocument();
    expect(screen.queryByText("Default model choices")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Default models" }));
    expect(await screen.findByText("Default model choices")).toBeVisible();
    expect(screen.getByRole("navigation", { name: "suppliers" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Configure providers" }));
    await waitFor(() => expect(screen.queryByText("Default model choices")).not.toBeInTheDocument());
  });

  it("opens the default model drawer for an existing capability deep link", async () => {
    renderAt("/settings?section=models&target=vlm");
    expect(await screen.findByText("Default model choices")).toBeVisible();
    expect(screen.getByRole("navigation", { name: "suppliers" })).toBeInTheDocument();
  });
});
