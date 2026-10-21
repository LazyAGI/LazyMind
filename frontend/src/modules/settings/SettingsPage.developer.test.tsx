import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

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
    fetchSettingsOverview: vi.fn(),
    fetchUserUiPreferences: vi.fn(),
    applySettingsChange: vi.fn(),
    listTasks: vi.fn(),
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
  AgentAppsAuth: { getUserInfo: () => ({ role: "admin" }) },
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

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: mocks.fetchUserUiPreferences,
  patchUserUiPreferences: vi.fn(),
}));

vi.mock("@/modules/taskCenter/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/modules/taskCenter/api")>(),
  listTasks: mocks.listTasks,
}));

describe("SettingsPage developer preferences", () => {
  beforeEach(() => {
    mocks.listTasks.mockReset().mockResolvedValue({ items: [], total: 0 });
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

  it("enables developer mode only after confirmation and persistence", async () => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter></ConfigProvider>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await screen.findByText("settingsPage.confirm.developerEnableContent");
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
    expect(screen.getByRole("switch", { name: "settingsPage.developer.performanceAria" })).toBeDisabled();
    fireEvent.click(screen.getByText("settingsPage.confirmEnable"));
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "true"));
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(mocks.listTasks).not.toHaveBeenCalled();
    expect(screen.getByRole("switch", { name: "settingsPage.developer.performanceAria" })).toBeEnabled();
  });

  it("keeps developer mode and its child controls off when enabling is canceled", async () => {
    render(<MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await screen.findByText("settingsPage.confirm.developerEnableContent");
    fireEvent.click(screen.getByText("settingsPage.cancel"));
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

  it("shows performance stats beside sensitive-word filtering before developer mode is enabled", async () => {
    render(
      <MemoryRouter initialEntries={["/settings?section=developer"]}>
        <SettingsPage />
      </MemoryRouter>,
    );

    const sensitiveSwitch = await screen.findByRole("switch", {
      name: "settingsPage.developer.sensitiveWordFilterAria",
    });
    const performanceSwitch = screen.getByRole("switch", {
      name: "settingsPage.developer.performanceAria",
    });

    expect(sensitiveSwitch).toBeDisabled();
    expect(performanceSwitch).toBeDisabled();
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
