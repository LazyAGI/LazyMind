import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import SettingsPage from "./index";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";

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
    fetchSettingsOverview: vi.fn(),
    fetchUserUiPreferences: vi.fn(),
    applySettingsChange: vi.fn(),
    listTasks: vi.fn(),
    readLocalCapability: vi.fn(),
  };
});

vi.mock("react-i18next", () => ({
  initReactI18next: { type: "3rdParty", init: vi.fn() },
  useTranslation: () => ({
    i18n: { language: "zh-CN" },
    t: (key: string) => key,
  }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "admin", userId: "user-1" }) },
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: {
    hideEvo: true,
    hideUserGroupSurfaces: true,
  },
}));

vi.mock("@/runtime/mode", () => ({
  isDesktopRuntime: () => true,
  isLocalRuntime: () => true,
  isVocabularyEnabled: () => false,
}));

vi.mock("./api", () => ({
  fetchSettingsOverview: mocks.fetchSettingsOverview,
  runSettingsChecks: vi.fn(),
  applySettingsChange: mocks.applySettingsChange,
}));

vi.mock("@/modules/githubStar/api", () => ({
  readLocalGitHubStarCapability: mocks.readLocalCapability,
}));

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: mocks.fetchUserUiPreferences,
  patchUserUiPreferences: vi.fn(),
}));

vi.mock("@/modules/taskCenter/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/modules/taskCenter/api")>(),
  listTasks: mocks.listTasks,
}));

vi.mock("./QuickModelSettings", () => ({ default: () => null }));

describe("SettingsPage developer preferences", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.spyOn(dataSourceCloudOauthApi, "listConnectionsApiAuthserviceV1CloudConnectionsGet").mockResolvedValue({ data: { items: [{ provider: "github", status: "ACTIVE" }] } } as never);
    mocks.listTasks.mockReset().mockResolvedValue({ items: [], total: 0 });
    mocks.readLocalCapability.mockReset().mockResolvedValue({ repository: "LazyAGI/LazyMind", eligible: false });
    mocks.applySettingsChange.mockReset().mockResolvedValue({ key: "developer_mode_active", enabled: true, preferences: { developer_mode_active: true } });
    mocks.fetchSettingsOverview.mockReset().mockResolvedValue({
      controls: {},
      sections: [],
      issues: [],
      updated_at: "2026-09-04T00:00:00Z",
    });
    mocks.fetchUserUiPreferences.mockReset().mockResolvedValue({
      developer_mode_active: false,
      performance_stats_enabled: false,
      sensitive_word_filter_enabled: false,
    });
  });

  it("checks and saves developer mode directly without a confirmation dialog", async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({
      developer_mode_active: false,
      performance_stats_enabled: true,
      sensitive_word_filter_enabled: true,
    });
    let finish!: (value: unknown) => void;
    mocks.applySettingsChange.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter></ConfigProvider>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    const group = screen.getByRole("group", { name: "settingsPage.developer.optionsGroupAria" });
    expect(group).toHaveAttribute("aria-disabled", "true");
    expect(within(group).queryByRole("status")).not.toBeInTheDocument();
    expect(within(group).queryByText("settingsPage.developer.inactiveNotice")).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1));
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).toHaveBeenCalledTimes(1);
    expect(mocks.readLocalCapability).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "settingsPage.developer.performanceAria" })).toBeDisabled();
    await act(async () => finish({ key: "developer_mode_active", enabled: true,
      preferences: { developer_mode_active: true } }));
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "true"));
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(mocks.listTasks).not.toHaveBeenCalled();
    expect(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("settingsPage.developer.githubGuideTitle")).not.toBeInTheDocument();
    expect(group).toHaveAttribute("aria-disabled", "false");
    expect(group).not.toHaveClass("is-master-paused");
    expect(within(group).queryByRole("status")).not.toBeInTheDocument();
    for (const name of ["settingsPage.developer.performanceAria", "settingsPage.developer.sensitiveWordFilterAria"]) {
      const childSwitch = screen.getByRole("switch", { name });
      expect(childSwitch).toBeEnabled();
      expect(childSwitch).toBeChecked();
      expect(childSwitch).not.toHaveAttribute("aria-describedby");
      expect(childSwitch.closest(".settings-detail-row")).not.toHaveClass("is-master-paused");
      expect(childSwitch.closest(".settings-detail-row")).toHaveAttribute("aria-disabled", "false");
    }
  });

  it("checks GitHub before enabling from the settings overview", async () => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/settings?section=overview"]}><SettingsPage /></MemoryRouter></ConfigProvider>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.overview.enableDeveloper" });
    fireEvent.click(toggle);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(toggle).toBeChecked());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).toHaveBeenCalledTimes(1);
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("settingsPage.developer.githubGuideTitle")).not.toBeInTheDocument();
  });

  it.each([
    ["developer", "settingsPage.developer.modeAria"],
    ["overview", "settingsPage.overview.enableDeveloper"],
  ])("keeps %s mode off without a GitHub connection or local capability, including an old skip record", async (section, name) => {
    vi.mocked(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).mockResolvedValue({ data: { items: [] } } as never);
    localStorage.setItem("lazymind:developer-github-guide-dismissed:user-1", "1");
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[`/settings?section=${section}`]}><SettingsPage /></MemoryRouter></ConfigProvider>);
    const toggle = await screen.findByRole("switch", { name });
    fireEvent.click(toggle);
    await screen.findByText("settingsPage.developer.githubGuideDescription");
    expect(screen.queryByText("settingsPage.confirm.developerEnableContent")).not.toBeInTheDocument();
    expect(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).toHaveBeenCalledTimes(1);
    expect(mocks.readLocalCapability).toHaveBeenCalledTimes(1);
    expect(toggle).not.toBeChecked();
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
    expect(screen.queryByText("settingsPage.developer.githubGuideSkip")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("settingsPage.developer.githubGuideCancel"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(toggle).not.toBeChecked();
  });

  it.each([
    ["developer", "settingsPage.developer.modeAria"],
    ["overview", "settingsPage.overview.enableDeveloper"],
  ])("enables %s mode with local capability and no GitHub connection", async (section, name) => {
    vi.mocked(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet)
      .mockResolvedValue({ data: { items: [] } } as never);
    mocks.readLocalCapability.mockResolvedValue({ repository: "LazyAGI/LazyMind", eligible: true });
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[`/settings?section=${section}`]}><SettingsPage /></MemoryRouter></ConfigProvider>);
    const toggle = await screen.findByRole("switch", { name });
    fireEvent.click(toggle);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(toggle).toBeChecked());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.readLocalCapability).toHaveBeenCalledTimes(1);
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("settingsPage.developer.githubGuideDescription")).not.toBeInTheDocument();
  });

  it("keeps mode off after an incomplete GitHub round trip and enables only after a fresh valid check", async () => {
    vi.mocked(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).mockResolvedValue({ data: { items: [] } } as never);
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/settings?section=developer"]}>
      <Routes>
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/cloud-documents/github" element={<Link to="/settings?section=developer">Return from GitHub</Link>} />
      </Routes>
    </MemoryRouter></ConfigProvider>);
    fireEvent.click(await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" }));
    fireEvent.click(await screen.findByText("settingsPage.developer.githubGuideConnect"));
    fireEvent.click(await screen.findByText("Return from GitHub"));
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    expect(toggle).not.toBeChecked();
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
    vi.mocked(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).mockResolvedValue({ data: { items: [{ provider: "github", status: "ACTIVE" }] } } as never);
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).toHaveBeenCalledTimes(2);
  });

  it("keeps existing enabled users unchanged without requesting GitHub association", async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({ developer_mode_active: true });
    render(<MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter>);
    expect(await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" })).toBeChecked();
    expect(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet).not.toHaveBeenCalled();
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
    expect(screen.queryByText("settingsPage.developer.githubGuideDescription")).not.toBeInTheDocument();
  });

  it("keeps mode and its child controls off when the direct GitHub guide is canceled", async () => {
    vi.mocked(dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet)
      .mockResolvedValue({ data: { items: [] } } as never);
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter></ConfigProvider>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await screen.findByText("settingsPage.developer.githubGuideDescription");
    expect(screen.queryByText("settingsPage.confirm.developerEnableContent")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("settingsPage.developer.githubGuideCancel"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
    expect(screen.getByRole("switch", { name: "settingsPage.developer.performanceAria" })).toBeDisabled();
  });

  it("retains the enabled switch when the disable confirmation is canceled", async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({ developer_mode_active: true });
    mocks.listTasks.mockResolvedValue({ items: [{ id: "background-task" }], total: 1 });
    render(<MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await screen.findByText("settingsPage.change.developerConsequence");
    fireEvent.click(screen.getByText("settingsPage.cancel"));
    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
  });

  it("disables developer mode without a confirmation when no tasks are running", async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({ developer_mode_active: true });
    mocks.applySettingsChange.mockResolvedValue({ key: "developer_mode_active", enabled: false, preferences: { developer_mode_active: false } });
    render(<MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.listTasks).toHaveBeenCalledWith({ status: "running", page_size: 1 });
    expect(screen.getByRole("switch", { name: "settingsPage.developer.performanceAria" })).toBeDisabled();
  });

  it("grays and disables saved developer options without showing an explanatory notice", async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({
      developer_mode_active: false,
      performance_stats_enabled: true,
      sensitive_word_filter_enabled: true,
    });
    render(
      <MemoryRouter initialEntries={["/settings?section=developer"]}>
        <SettingsPage />
      </MemoryRouter>,
    );

    const performanceSwitch = await screen.findByRole("switch", {
      name: "settingsPage.developer.performanceAria",
    });
    expect(screen.getByText("settingsPage.developer.description")).toBeInTheDocument();
    expect(screen.queryByText("settingsPage.fallbackDetail")).not.toBeInTheDocument();
    const group = screen.getByRole("group", { name: "settingsPage.developer.optionsGroupAria" });
    expect(group).toHaveAttribute("aria-disabled", "true");
    expect(group).toHaveClass("is-master-paused");
    expect(within(group).queryByRole("status")).not.toBeInTheDocument();
    expect(within(group).queryByText("settingsPage.developer.inactiveNotice")).not.toBeInTheDocument();
    for (const childSwitch of [performanceSwitch, screen.getByRole("switch", { name: "settingsPage.developer.sensitiveWordFilterAria" })]) {
      expect(childSwitch).toBeChecked();
      expect(childSwitch).toBeDisabled();
      expect(childSwitch).not.toHaveAttribute("aria-describedby");
      expect(childSwitch.closest(".settings-detail-row")).not.toHaveClass("is-master-paused");
      expect(childSwitch.closest(".settings-detail-row")).toHaveAttribute("aria-disabled", "true");
    }
  });

  it("places environment variables immediately after MCP in Capabilities", async () => {
    render(
      <MemoryRouter initialEntries={["/settings?section=developer"]}>
        <SettingsPage />
      </MemoryRouter>,
    );
    const management = (await screen.findByText("settingsPage.navGroups.management")).closest(".settings-reference-nav-group") as HTMLElement;
    const chat = screen.getByText("settingsPage.navGroups.chatKnowledge").closest(".settings-reference-nav-group") as HTMLElement;
    const capabilities = screen.getByText("settingsPage.navGroups.capabilities").closest(".settings-reference-nav-group") as HTMLElement;
    const env = within(capabilities).getByRole("button", { name: /settingsPage.sections.envVars/ });
    const mcp = within(capabilities).getByRole("button", { name: /settingsPage.sections.mcp/ });
    const assistants = within(capabilities).getByRole("button", { name: /settingsPage.sections.assistants/ });
    expect(mcp.nextElementSibling).toBe(env);
    expect(env.nextElementSibling).toBe(assistants);
    expect(within(management).queryByRole("button", { name: /settingsPage.sections.envVars/ })).not.toBeInTheDocument();
    expect(within(chat).queryByRole("button", { name: /settingsPage.sections.envVars/ })).not.toBeInTheDocument();
  });

  it("distinguishes MCP connections from system tools in navigation", async () => {
    render(
      <MemoryRouter initialEntries={["/settings?section=developer"]}>
        <SettingsPage />
      </MemoryRouter>,
    );
    const nav = await screen.findByRole("navigation", { name: "settingsPage.navAria" });
    const systemTools = within(nav).getByRole("button", { name: /settingsPage.sections.systemTools/ });
    const mcp = within(nav).getByRole("button", { name: /settingsPage.sections.mcp/ });
    expect(within(systemTools).getByRole("img", { name: "tool" })).toBeInTheDocument();
    expect(within(mcp).getByRole("img", { name: "apartment" })).toBeInTheDocument();
    expect(within(mcp).queryByRole("img", { name: "tool" })).not.toBeInTheDocument();
  });
});
