import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useSettingsChange } from "./useSettingsChange";

const api = vi.hoisted(() => ({ checkSettingsChange: vi.fn(), applySettingsChange: vi.fn() }));
vi.mock("./api", () => api);
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

const impact = (ids: string[] = []) => ({ key: "skills_enabled", enabled: false, tasks: ids.map((id) => ({ id, title: id, status: "running" })) });
function Fixture({ saved = vi.fn() }: { saved?: ReturnType<typeof vi.fn> }) {
  const change = useSettingsChange(saved);
  return <><button onClick={() => change.requestChange("skills_enabled", false)}>disable</button>
    <button onClick={() => change.requestChange("skills_enabled", true)}>enable</button>{change.dialog}</>;
}

describe("settings impact confirmation", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.checkSettingsChange.mockResolvedValue(impact());
    api.applySettingsChange.mockResolvedValue({ applied: true, impact: impact() });
  });
  it.each(["enable", "disable"])("saves %s immediately when no affected tasks exist", async (action) => {
    const saved = vi.fn(); render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText(action));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(api.applySettingsChange).toHaveBeenCalledWith({ key: "skills_enabled", enabled: action === "enable", confirmed_task_ids: undefined });
  });
  it("cancel leaves preferences and running tasks unchanged", async () => {
    api.checkSettingsChange.mockResolvedValue(impact(["task-a"]));
    render(<Fixture />); fireEvent.click(screen.getByText("disable"));
    await screen.findByText("task-a"); fireEvent.click(screen.getByText("settingsPage.cancel"));
    expect(api.applySettingsChange).not.toHaveBeenCalled();
  });
  it("requires a new confirmation when another task starts before save", async () => {
    api.checkSettingsChange.mockResolvedValue(impact(["task-a"]));
    api.applySettingsChange.mockResolvedValueOnce({ applied: false, impact: impact(["task-a", "task-b"]) }).mockResolvedValueOnce({ applied: true, impact: impact() });
    const saved = vi.fn(); render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText("disable"));
    fireEvent.click(await screen.findByText("settingsPage.confirmDisable"));
    await screen.findByText("task-b"); expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.confirmDisable"));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(api.applySettingsChange).toHaveBeenLastCalledWith({ key: "skills_enabled", enabled: false, confirmed_task_ids: ["task-a", "task-b"] });
  });
  it.each(["check", "save"])("keeps the effective value after %s failure and supports retry", async (stage) => {
    api[stage === "check" ? "checkSettingsChange" : "applySettingsChange"].mockRejectedValueOnce(new Error("offline"));
    const saved = vi.fn(); render(<Fixture saved={saved} />);
    fireEvent.click(screen.getByText("disable"));
    await screen.findByText(`settingsPage.change.${stage}Failed`);
    expect(saved).not.toHaveBeenCalled();
    if (stage === "check") expect(api.applySettingsChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("settingsPage.retry"));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(api.checkSettingsChange).toHaveBeenCalledTimes(2);
  });
  it("deduplicates rapid clicks while checking", async () => {
    let finish!: (value: ReturnType<typeof impact>) => void;
    api.checkSettingsChange.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    render(<Fixture />); fireEvent.click(screen.getByText("disable")); fireEvent.click(screen.getByText("disable"));
    expect(api.checkSettingsChange).toHaveBeenCalledTimes(1);
    await act(async () => finish(impact()));
    expect(api.applySettingsChange).toHaveBeenCalledTimes(1);
  });
});
