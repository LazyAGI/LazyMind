import { ConfigProvider } from "antd";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SettingsChangeKey } from "./api";
import { useSettingsChange } from "./useSettingsChange";

const api = vi.hoisted(() => ({ applySettingsChange: vi.fn() }));
const tasks = vi.hoisted(() => ({ listTasks: vi.fn() }));
vi.mock("./api", () => api);
vi.mock("@/modules/taskCenter/api", () => tasks);
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
    tasks.listTasks.mockResolvedValue({ items: [], total: 0 });
  });

  it("enables other features directly and notifies the page after persistence", async () => {
    const setting = "skills_enabled";
    const saved = vi.fn();
    render(<Fixture saved={saved} setting={setting} />);
    fireEvent.click(screen.getByText("enable"));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: setting, enabled: true }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
    expect(api.applySettingsChange).toHaveBeenCalledWith({ key: setting, enabled: true });
    expect(tasks.listTasks).not.toHaveBeenCalled();
  });

  it.each(["cancel", "confirm"])("requires confirmation before enabling developer mode: %s", async (action) => {
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("enable"));
    await screen.findByText("settingsPage.confirm.developerEnableContent");
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    expect(saved).not.toHaveBeenCalled();
    expect(tasks.listTasks).not.toHaveBeenCalled();
    if (action === "cancel") {
      fireEvent.click(screen.getByText("settingsPage.cancel"));
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      expect(api.applySettingsChange).not.toHaveBeenCalled();
    } else {
      const confirm = screen.getByText("settingsPage.confirmEnable");
      fireEvent.click(confirm);
      fireEvent.click(confirm);
      await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: "developer_mode_active", enabled: true }));
      expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
    }
  });

  it.each<SettingsChangeKey>([
    "task_center_enabled", "schedules_enabled", "skills_enabled",
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
    expect(tasks.listTasks).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("disables developer mode directly when there are no running tasks", async () => {
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("disable"));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: "developer_mode_active", enabled: false }));
    expect(tasks.listTasks).toHaveBeenCalledWith({ status: "running", page_size: 1 });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("requires confirmation for running tasks, including tasks beyond the returned page", async () => {
    tasks.listTasks.mockResolvedValue({ items: [{ id: "background-task" }], total: 3 });
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("disable"));
    await screen.findByText("settingsPage.change.developerConsequence");
    expect(tasks.listTasks).toHaveBeenCalledWith({ status: "running", page_size: 1 });
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.confirmDisable"));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: "developer_mode_active", enabled: false }));
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
  });

  it.each([0, 1])("keeps developer mode on after a status-check failure and checks again before retrying with %s running tasks", async (total) => {
    tasks.listTasks.mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ items: [], total });
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("disable"));
    await screen.findByText("settingsPage.change.taskCheckFailed");
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.retry"));
    await waitFor(() => expect(tasks.listTasks).toHaveBeenCalledTimes(2));
    if (total > 0) {
      await screen.findByText("settingsPage.change.developerConsequence");
      expect(api.applySettingsChange).not.toHaveBeenCalled();
      fireEvent.click(screen.getByText("settingsPage.confirmDisable"));
    }
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ key: "developer_mode_active", enabled: false }));
  });

  it("prevents duplicate status checks and changes while checking running tasks", async () => {
    let finish!: (value: { items: never[]; total: number }) => void;
    tasks.listTasks.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("disable"));
    fireEvent.click(screen.getByText("disable"));
    fireEvent.click(screen.getByText("enable"));
    expect(tasks.listTasks).toHaveBeenCalledTimes(1);
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    await act(async () => finish({ items: [], total: 0 }));
    expect(saved).toHaveBeenCalledTimes(1);
  });

  it("closes the status-check error before saving after an idle retry", async () => {
    tasks.listTasks.mockRejectedValueOnce(new Error("offline"));
    let finish!: (value: { key: SettingsChangeKey; enabled: boolean }) => void;
    api.applySettingsChange.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("disable"));
    await screen.findByText("settingsPage.change.taskCheckFailed");
    fireEvent.click(screen.getByText("settingsPage.retry"));
    await waitFor(() => expect(api.applySettingsChange).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(saved).not.toHaveBeenCalled();
    await act(async () => finish({ key: "developer_mode_active", enabled: false }));
    expect(saved).toHaveBeenCalledTimes(1);
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
