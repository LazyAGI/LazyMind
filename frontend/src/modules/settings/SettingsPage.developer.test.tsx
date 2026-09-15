import { render, screen } from "@testing-library/react";
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
}));

vi.mock("./api", () => ({
  fetchSettingsOverview: mocks.fetchSettingsOverview,
  runSettingsChecks: vi.fn(),
}));

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: mocks.fetchUserUiPreferences,
  patchUserUiPreferences: vi.fn(),
}));

describe("SettingsPage developer preferences", () => {
  beforeEach(() => {
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
