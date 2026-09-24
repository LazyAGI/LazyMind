import { ConfigProvider } from "antd";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SettingsChangeKey } from "./api";
import { useSettingsChange } from "./useSettingsChange";

const api = vi.hoisted(() => ({ applySettingsChange: vi.fn() }));
vi.mock("./api", () => api);
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

function Fixture({ saved = vi.fn(), setting = "skills_enabled" }: {
  saved?: ReturnType<typeof vi.fn>;
  setting?: SettingsChangeKey;
}) {
  const change = useSettingsChange(saved);
  return <ConfigProvider theme={{ token: { motion: false } }}><button onClick={() => change.requestChange(setting, false)}>disable</button>
    <button onClick={() => change.requestChange(setting, true)}>enable</button>{change.dialog}</ConfigProvider>;
}

describe("settings disable confirmation", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.applySettingsChange.mockImplementation(async (change) => change);
  });

  it("enables directly and notifies the page after persistence", async () => {
    const saved = vi.fn();
    render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText("enable"));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: "skills_enabled", enabled: true }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(api.applySettingsChange).toHaveBeenCalledWith({ key: "skills_enabled", enabled: true });
  });

  it.each<SettingsChangeKey>([
    "developer_mode_active", "task_center_enabled", "schedules_enabled", "skills_enabled",
    "workflows_enabled", "mcp_enabled", "document_parsing_enabled",
  ])("confirms %s once before saving, without fetching running tasks", async (setting) => {
    const saved = vi.fn();
    render(<Fixture saved={saved} setting={setting} />);
    fireEvent.click(screen.getByText("disable"));
    expect(await screen.findByText("settingsPage.change.consequence")).toBeInTheDocument();
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.confirmDisable"));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: setting, enabled: false }));
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(api.applySettingsChange).toHaveBeenCalledWith({ key: setting, enabled: false });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("cancel does not save or change the effective setting", async () => {
    const saved = vi.fn();
    render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText("disable"));
    fireEvent.click(await screen.findByText("settingsPage.cancel"));
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    expect(saved).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it.each([true, false])("keeps the displayed setting after save failure and retries enabled=%s", async (enabled) => {
    api.applySettingsChange.mockRejectedValueOnce(new Error("offline"));
    const saved = vi.fn();
    render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText(enabled ? "enable" : "disable"));
    if (!enabled) fireEvent.click(await screen.findByText("settingsPage.confirmDisable"));
    await screen.findByText("settingsPage.change.saveFailed");
    expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.retry"));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(api.applySettingsChange.mock.calls).toEqual([
      [{ key: "skills_enabled", enabled }], [{ key: "skills_enabled", enabled }],
    ]);
  });

  it("prevents duplicate saves and canceling while saving", async () => {
    let finish!: (value: { key: SettingsChangeKey; enabled: boolean }) => void;
    api.applySettingsChange.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const saved = vi.fn();
    render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText("disable"));
    const confirm = await screen.findByText("settingsPage.confirmDisable");
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    fireEvent.click(screen.getByText("enable"));
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "settingsPage.cancel" })).toBeDisabled();
    expect(saved).not.toHaveBeenCalled();
    await act(async () => finish({ key: "skills_enabled", enabled: false }));
    expect(saved).toHaveBeenCalledTimes(1);
  });
});
