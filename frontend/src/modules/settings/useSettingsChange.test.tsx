import { ConfigProvider } from "antd";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SettingsChangeKey } from "./api";
import { useSettingsChange } from "./useSettingsChange";

const api = vi.hoisted(() => ({ applySettingsChange: vi.fn() }));
const tasks = vi.hoisted(() => ({ listTasks: vi.fn() }));
const github = vi.hoisted(() => ({ listConnections: vi.fn() }));
vi.mock("./api", () => api);
vi.mock("@/modules/taskCenter/api", () => tasks);
vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: { listConnectionsApiAuthserviceV1CloudConnectionsGet: github.listConnections },
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

function Fixture({ saved = vi.fn(), setting = "skills_enabled", connect = vi.fn() }: {
  saved?: ReturnType<typeof vi.fn>;
  setting?: SettingsChangeKey;
  connect?: ReturnType<typeof vi.fn>;
}) {
  const change = useSettingsChange(saved, connect);
  return <ConfigProvider theme={{ token: { motion: false } }}><button onClick={() => change.requestChange(setting, false)}>disable</button>
    <button onClick={() => change.requestChange(setting, true)}>enable</button>{change.dialog}</ConfigProvider>;
}

describe("settings disable confirmation", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.applySettingsChange.mockImplementation(async (change) => change);
    tasks.listTasks.mockResolvedValue({ items: [], total: 0 });
    github.listConnections.mockResolvedValue({ data: { items: [{ provider: "github", status: "ACTIVE" }] } });
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

  it.each([null, "EXPIRED", "REVOKED"])("requires a valid GitHub association before enabling: %s", async status => {
    github.listConnections.mockResolvedValue({ data: { items: status ? [{ provider: "github", status }] : [] } });
    localStorage.setItem("lazymind:developer-github-guide-dismissed:user-1", "1");
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("enable"));
    fireEvent.click(await screen.findByText("settingsPage.confirmEnable"));
    await screen.findByText("settingsPage.developer.githubGuideDescription");
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("settingsPage.developer.githubGuideSkip")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("settingsPage.developer.githubGuideCancel"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.applySettingsChange).not.toHaveBeenCalled();
  });

  it("keeps mode off when GitHub lookup fails and checks again before retrying", async () => {
    github.listConnections.mockRejectedValueOnce(new Error("offline"));
    const saved = vi.fn();
    render(<Fixture saved={saved} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("enable"));
    fireEvent.click(await screen.findByText("settingsPage.confirmEnable"));
    await screen.findByText("settingsPage.developer.githubCheckFailed");
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.retry"));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(github.listConnections).toHaveBeenCalledTimes(2);
  });

  it("does not enable on leaving for GitHub or returning without completing association", async () => {
    github.listConnections.mockResolvedValue({ data: { items: [] } });
    const saved = vi.fn();
    const connect = vi.fn();
    render(<Fixture saved={saved} connect={connect} setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("enable"));
    fireEvent.click(await screen.findByText("settingsPage.confirmEnable"));
    fireEvent.click(await screen.findByText("settingsPage.developer.githubGuideConnect"));
    expect(connect).toHaveBeenCalledTimes(1);
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("enable"));
    fireEvent.click(await screen.findByText("settingsPage.confirmEnable"));
    await screen.findByText("settingsPage.developer.githubGuideDescription");
    expect(saved).not.toHaveBeenCalled();
    github.listConnections.mockResolvedValue({ data: { items: [{ provider: "github", status: "ACTIVE" }] } });
    fireEvent.click(screen.getByText("settingsPage.developer.githubGuideCancel"));
    fireEvent.click(screen.getByText("enable"));
    fireEvent.click(await screen.findByText("settingsPage.confirmEnable"));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(github.listConnections).toHaveBeenCalledTimes(3);
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
  });

  it("deduplicates GitHub checks and does not save after leaving during the lookup", async () => {
    let finish!: (response: unknown) => void;
    github.listConnections.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const view = render(<Fixture setting="developer_mode_active" />);
    fireEvent.click(screen.getByText("enable"));
    const confirm = await screen.findByText("settingsPage.confirmEnable");
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    expect(github.listConnections).toHaveBeenCalledTimes(1);
    expect(api.applySettingsChange).not.toHaveBeenCalled();
    view.unmount();
    await act(async () => finish({ data: { items: [{ provider: "github", status: "ACTIVE" }] } }));
    expect(api.applySettingsChange).not.toHaveBeenCalled();
  });

  it.each<SettingsChangeKey>([
    "task_center_enabled", "schedules_enabled", "skills_enabled",
    "workflows_enabled", "mcp_enabled", "document_parsing_enabled", "document_directories_enabled",
  ])("confirms %s once before saving, without fetching running tasks", async (setting) => {
    const saved = vi.fn();
    render(<Fixture saved={saved} setting={setting} />);
    fireEvent.click(screen.getByText("disable"));
    expect(await screen.findByText(setting === "document_directories_enabled" ? "settingsPage.controls.documentDirectories.summary" : "settingsPage.change.consequence")).toBeInTheDocument();
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
