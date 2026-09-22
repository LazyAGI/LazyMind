import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    checkSettingsChange: vi.fn(),
    applySettingsChange: vi.fn(),
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
  checkSettingsChange: mocks.checkSettingsChange,
  applySettingsChange: mocks.applySettingsChange,
}));

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: mocks.fetchUserUiPreferences,
  patchUserUiPreferences: vi.fn(),
}));

describe("SettingsPage developer preferences", () => {
  beforeEach(() => {
    mocks.checkSettingsChange.mockReset().mockResolvedValue({ key: "developer_mode_active", enabled: true, tasks: [] });
    mocks.applySettingsChange.mockReset().mockResolvedValue({ applied: true, impact: { key: "developer_mode_active", enabled: true, tasks: [] }, preferences: { developer_mode_active: true } });
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

  it("enables developer mode with one click and updates the switch after persistence", async () => {
    render(<MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "true"));
    expect(mocks.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "settingsPage.developer.performanceAria" })).toBeEnabled();
  });

  it("retains the enabled switch when an affected-task confirmation is canceled", async () => {
    mocks.fetchUserUiPreferences.mockResolvedValue({ developer_mode_active: true });
    mocks.checkSettingsChange.mockResolvedValue({ key: "developer_mode_active", enabled: false, tasks: [{ id: "chat:c", title: "Active task", status: "running" }] });
    render(<MemoryRouter initialEntries={["/settings?section=developer"]}><SettingsPage /></MemoryRouter>);
    const toggle = await screen.findByRole("switch", { name: "settingsPage.developer.modeAria" });
    fireEvent.click(toggle);
    await screen.findByText("Active task");
    fireEvent.click(screen.getByText("settingsPage.cancel"));
    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(mocks.applySettingsChange).not.toHaveBeenCalled();
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
});
