import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConfigProvider } from "antd";
import VocabularySettings from "./VocabularySettings";
import i18n from "@/i18n";

vi.mock("./cloudApi", () => ({ listCloudConnections: vi.fn(async () => []), listCloudOperations: vi.fn(async () => []) }));

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
  beforeEach(async () => {
    await i18n.changeLanguage("zh-CN");
    mocks.desktop.mockResolvedValue({ installed: true, executable_path: "/Applications/Anki.app", addon_code: "2055492159" });
    mocks.setting.mockResolvedValue({ selected_provider: "local", anki_endpoint: "http://127.0.0.1:8765", anki_deck_name: "LazyMind Vocabulary" });
  });

  it("offers Anki, Eudic and MaiMemo configuration together", async () => {
    mocks.status.mockResolvedValue({ connected: false });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    expect(await screen.findByRole("button", { name: "Anki 配置" })).toBeVisible();
    expect(screen.getByRole("button", { name: "欧路词典 配置" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "墨墨背单词 配置" }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "墨墨背单词" })).toBeVisible());
  });

  it("localizes the Anki setup flow in English", async () => {
    await i18n.changeLanguage("en-US");
    mocks.desktop.mockResolvedValue({ installed: false });
    mocks.status.mockResolvedValue({ connected: false });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki Configure" }));
    await waitFor(() => expect(screen.getByText("Anki setup")).toBeVisible());
    expect(screen.getByText("Allow LazyMind to access Anki")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "View installation guide" }));
    await waitFor(() => expect(screen.getByText("Install Anki").closest("[role=dialog]")).toBeVisible());
  });

  it("shows installation state when AnkiConnect is missing", async () => {
    mocks.status.mockResolvedValue({ connected: false, initialized: false, pending_operations: 0 });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    expect(screen.getByText("Anki 客户端已安装")).toBeTruthy();
    expect(screen.getByText("尚未连接 AnkiConnect")).toBeTruthy();
  });

  it("shows the connection switch when installation is complete", async () => {
    mocks.status.mockResolvedValue({ connected: true, initialized: true, version: 6, pending_operations: 0 });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    expect(screen.getByText("允许 LazyMind 操作 Anki")).toBeTruthy();
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  it("asks the user to open Anki when the add-on is already installed", async () => {
    mocks.desktop.mockResolvedValue({ installed: true, connect_installed: true, executable_path: "/Applications/Anki.app", addon_code: "2055492159" });
    mocks.status.mockResolvedValue({ connected: false, initialized: false, pending_operations: 0 });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    expect((await screen.findAllByRole("button", { name: "打开 Anki" })).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "安装 AnkiConnect" })).toBeNull();
    expect(screen.getByText(/AnkiConnect 已安装，但 Anki 尚未运行/)).toBeTruthy();
  });
});
