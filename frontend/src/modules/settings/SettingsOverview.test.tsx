import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { createInstance } from "i18next";
import type { ReactNode } from "react";
import { I18nextProvider } from "react-i18next";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import zhCN from "@/i18n/locales/zh-CN";
import enUS from "@/i18n/locales/en-US";
import SettingsPage from "./index";

const mocks = vi.hoisted(() => {
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      removeItem: (key: string) => values.delete(key),
      setItem: (key: string, value: string) => values.set(key, String(value)),
    },
  });
  return {
    mode: "local",
    role: "admin",
    fetchSettingsOverview: vi.fn(),
    fetchUserUiPreferences: vi.fn(),
    applySettingsChange: vi.fn(),
  };
});

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: mocks.role }) },
}));
vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideEvo: true, hideUserGroupSurfaces: true },
}));
vi.mock("@/runtime/mode", () => ({
  isDesktopRuntime: () => mocks.mode === "desktop",
  isLocalRuntime: () => mocks.mode === "local",
  isVocabularyEnabled: () => false,
}));
vi.mock("@/runtime/cloud/session", () => ({
  getCloudSession: vi.fn().mockResolvedValue(null),
  isCloudBusinessAvailable: () => false,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "test-cloud-session-changed",
}));
vi.mock("./api", () => ({
  fetchSettingsOverview: mocks.fetchSettingsOverview,
  applySettingsChange: mocks.applySettingsChange,
  runSettingsChecks: vi.fn(),
}));
vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: mocks.fetchUserUiPreferences,
  patchUserUiPreferences: vi.fn(),
}));
vi.mock("./QuickModelSettings", () => ({ default: () => <div>Model configuration</div> }));
vi.mock("./MemoryCapabilitySettings", () => ({ default: () => <h1>Memory configuration</h1> }));
vi.mock("./KnowledgeDataSettings", () => ({ default: () => <h1>Knowledge configuration</h1> }));
vi.mock("@/modules/modelProvider/pages/ExternalServicesPage", () => ({
  default: ({ visibleCategories, parsingControl }: { visibleCategories: string[]; parsingControl?: ReactNode }) => <>
    <output aria-label="Service categories">{visibleCategories.join(",")}</output>
    {parsingControl}
  </>,
}));

function CurrentRoute() {
  const location = useLocation();
  return <output aria-label="Current route">{location.pathname}{location.search}</output>;
}

async function renderOverview(language = "zh-CN") {
  const i18n = createInstance();
  await i18n.init({
    lng: language,
    fallbackLng: "zh-CN",
    resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } },
    interpolation: { escapeValue: false },
  });
  render(
    <I18nextProvider i18n={i18n}>
      <ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}>
        <MemoryRouter initialEntries={["/settings?section=overview&return_to=%2Fagent%2Fchat%2Fhome%2Ftest-conversation"]}>
          <SettingsPage />
          <CurrentRoute />
        </MemoryRouter>
      </ConfigProvider>
    </I18nextProvider>,
  );
  await screen.findByRole("heading", { level: 1, name: i18n.t("settingsPage.overview.title") });
  return i18n.t.bind(i18n);
}

function card(title: string) {
  return within(screen.getByRole("heading", { level: 2, name: title }).closest("section")!);
}

function routeParams() {
  return new URLSearchParams(screen.getByLabelText("Current route").textContent!.split("?")[1]);
}

describe("Settings overview", () => {
  beforeEach(() => {
    mocks.mode = "local";
    mocks.role = "admin";
    mocks.fetchSettingsOverview.mockReset().mockResolvedValue({
      controls: { document_parsing_enabled: true },
      sections: [],
      issues: [],
      updated_at: "2026-10-19T00:00:00Z",
    });
    mocks.fetchUserUiPreferences.mockReset().mockResolvedValue({
      developer_mode_active: false,
      performance_stats_enabled: false,
      sensitive_word_filter_enabled: false,
    });
    mocks.applySettingsChange.mockReset().mockResolvedValue({ key: "document_parsing_enabled", enabled: false });
  });

  it.each([
    ["local", "admin", "zh-CN"],
    ["desktop", "user", "en-US"],
    ["cloud", "user", "zh-CN"],
  ])("keeps independent model entries and three translated memory capabilities in %s mode", async (mode, role, language) => {
    mocks.mode = mode;
    mocks.role = role;
    const t = await renderOverview(language);
    expect(screen.getAllByRole("heading", { level: 2 }).slice(0, 7).map((heading) => heading.textContent)).toEqual(
      ["models", "defaultModels", "knowledge", "memory", "systemTools", "mcp", "assistants"].map((key) => t(`settingsPage.sections.${key}`)),
    );
    const memory = card(t("settingsPage.sections.memory"));
    for (const capability of ["vocabulary", "read", "edit"]) {
      const title = t(`settingsPage.memory.${capability}.title`);
      expect(memory.getByText(title)).toBeInTheDocument();
      expect(memory.getByText(t(`settingsPage.memory.${capability}.description`))).toBeInTheDocument();
      expect(memory.getByRole("button", { name: t("settingsPage.goToDetailAria", { title }) })).toBeEnabled();
    }
    expect(memory.queryByRole("switch")).not.toBeInTheDocument();
    const defaultModels = card(t("settingsPage.sections.defaultModels"));
    expect(defaultModels.getByText(t("settingsPage.controls.documentParsing.title"))).toBeInTheDocument();
    expect(defaultModels.getByRole("button", { name: t("settingsPage.quickConfig") })).toBeEnabled();
    expect(defaultModels.queryByRole("switch")).not.toBeInTheDocument();
    expect(card(t("settingsPage.sections.knowledge")).getAllByText(t("settingsPage.quickConfig"))).toHaveLength(1);
  });

  it.each(["vocabulary", "read", "edit"])("opens the existing memory settings from %s without writing preferences", async (capability) => {
    const t = await renderOverview();
    const title = t(`settingsPage.memory.${capability}.title`);
    fireEvent.click(card(t("settingsPage.sections.memory")).getByRole("button", {
      name: t("settingsPage.goToDetailAria", { title }),
    }));
    await screen.findByRole("heading", { name: "Memory configuration" });
    expect(routeParams().get("section")).toBe("memory");
    expect(routeParams().get("return_to")).toBe("/agent/chat/home/test-conversation");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
  });

  it("opens document parsing directly, preserving the return route and the switch value", async () => {
    const t = await renderOverview();
    fireEvent.click(card(t("settingsPage.sections.defaultModels")).getByRole("button", {
      name: t("settingsPage.quickConfig"),
    }));
    await screen.findByLabelText("Service categories");
    expect(screen.getByLabelText("Service categories")).toHaveTextContent("parsing");
    expect(screen.getByRole("switch", { name: t("settingsPage.knowledge.documentParsingAria") })).toBeChecked();
    expect(routeParams().get("section")).toBe("model_defaults");
    expect(routeParams().get("service")).toBe("parsing");
    expect(routeParams().get("return_to")).toBe("/agent/chat/home/test-conversation");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
  });

  it("keeps retrieval and data connections on the existing knowledge settings page", async () => {
    const t = await renderOverview();
    fireEvent.click(card(t("settingsPage.sections.knowledge")).getByRole("button", {
      name: t("settingsPage.goToDetailAria", { title: t("settingsPage.overview.knowledgeRetrieval") }),
    }));
    await screen.findByRole("heading", { name: "Knowledge configuration" });
    expect(routeParams().get("section")).toBe("knowledge");
    expect(routeParams().has("tool")).toBe(false);
    expect(routeParams().get("return_to")).toBe("/agent/chat/home/test-conversation");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
  });

  it("retains confirmation and persistence when disabling document parsing", async () => {
    const t = await renderOverview();
    fireEvent.click(card(t("settingsPage.sections.defaultModels")).getByRole("button", { name: t("settingsPage.quickConfig") }));
    const toggle = await screen.findByRole("switch", { name: t("settingsPage.knowledge.documentParsingAria") });
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
    mocks.fetchSettingsOverview.mockResolvedValue({ controls: { document_parsing_enabled: false }, sections: [], issues: [] });
    const confirmation = (await screen.findAllByRole("dialog")).find((dialog) =>
      within(dialog).queryByRole("button", { name: t("settingsPage.confirmDisable") }),
    );
    expect(confirmation).toBeDefined();
    fireEvent.click(within(confirmation!).getByRole("button", { name: t("settingsPage.confirmDisable") }));
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(mocks.applySettingsChange).toHaveBeenCalledWith({ key: "document_parsing_enabled", enabled: false });
    expect(routeParams().get("section")).toBe("model_defaults");
  });

  it("retains the current parsing value after a failed save and allows retry", async () => {
    mocks.applySettingsChange.mockRejectedValueOnce(new Error("Test save failure"));
    const t = await renderOverview();
    fireEvent.click(card(t("settingsPage.sections.defaultModels")).getByRole("button", { name: t("settingsPage.quickConfig") }));
    const toggle = await screen.findByRole("switch", { name: t("settingsPage.knowledge.documentParsingAria") });
    fireEvent.click(toggle);
    const confirmation = (await screen.findAllByRole("dialog")).find((dialog) =>
      within(dialog).queryByRole("button", { name: t("settingsPage.confirmDisable") }),
    );
    expect(confirmation).toBeDefined();
    fireEvent.click(within(confirmation!).getByRole("button", { name: t("settingsPage.confirmDisable") }));
    const failureMessage = await screen.findByText(t("settingsPage.change.saveFailed"));
    const dialog = failureMessage.closest('[role="dialog"]')!;
    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(within(dialog).getByRole("button", { name: t("settingsPage.retry") })).toBeEnabled();
    mocks.fetchSettingsOverview.mockResolvedValue({ controls: { document_parsing_enabled: false }, sections: [], issues: [] });
    fireEvent.click(within(dialog).getByRole("button", { name: t("settingsPage.retry") }));
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(2);
    expect(routeParams().get("section")).toBe("model_defaults");
  });
});
