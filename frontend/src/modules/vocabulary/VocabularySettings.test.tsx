import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import VocabularySettings from "./VocabularySettings";

const mocks = vi.hoisted(() => ({
  desktop: vi.fn(),
  status: vi.fn(),
  setting: vi.fn(),
}));

vi.mock("@/runtime/desktopBridge", () => ({
  ankiIntegrationStatus: mocks.desktop,
  openAnki: vi.fn(),
}));
vi.mock("./api", () => ({
  getAnkiStatus: mocks.status,
  getVocabularyProvider: mocks.setting,
  requestAnkiPermission: vi.fn(),
  saveVocabularyProvider: vi.fn(async (value) => value),
}));

describe("VocabularySettings", () => {
  beforeEach(() => {
    mocks.desktop.mockResolvedValue({ installed: true, executable_path: "/Applications/Anki.app", addon_code: "2055492159" });
    mocks.setting.mockResolvedValue({ selected_provider: "local", anki_endpoint: "http://127.0.0.1:8765", anki_deck_name: "LazyMind Vocabulary" });
  });

  it("shows installation state when AnkiConnect is missing", async () => {
    mocks.status.mockResolvedValue({ connected: false, initialized: false, pending_operations: 0 });
    render(<VocabularySettings />);
    fireEvent.click(await screen.findByRole("button", { name: "收起" }));
    expect(screen.getByText("Anki 客户端已安装")).toBeTruthy();
    expect(screen.getByText("AnkiConnect 未就绪")).toBeTruthy();
  });

  it("shows the connection switch when installation is complete", async () => {
    mocks.status.mockResolvedValue({ connected: true, initialized: true, version: 6, pending_operations: 0 });
    render(<VocabularySettings />);
    fireEvent.click(await screen.findByRole("button", { name: "收起" }));
    expect(screen.getByText("允许 LazyMind 操作 Anki")).toBeTruthy();
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  it("asks the user to open Anki when the add-on is already installed", async () => {
    mocks.desktop.mockResolvedValue({ installed: true, connect_installed: true, executable_path: "/Applications/Anki.app", addon_code: "2055492159" });
    mocks.status.mockResolvedValue({ connected: false, initialized: false, pending_operations: 0 });
    render(<VocabularySettings />);
    expect((await screen.findAllByRole("button", { name: "打开 Anki" })).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "安装 AnkiConnect" })).toBeNull();
    expect(screen.getByText(/AnkiConnect 已安装，但 Anki 尚未运行/)).toBeTruthy();
  });
});
