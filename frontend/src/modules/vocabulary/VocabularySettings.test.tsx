import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConfigProvider } from "antd";
import VocabularySettings from "./VocabularySettings";
import i18n from "@/i18n";

vi.mock("./cloudApi", () => ({ listCloudConnections: vi.fn(async () => []), listCloudOperations: vi.fn(async () => []) }));

const mocks = vi.hoisted(() => ({
  desktop: vi.fn(),
  status: vi.fn(),
  save: vi.fn(async (_value: unknown) => undefined),
  sync: vi.fn(async () => undefined),
}));

vi.mock("@/runtime/desktopBridge", () => ({
  ankiIntegrationStatus: mocks.desktop,
  openAnki: vi.fn(),
}));
vi.mock("./api", () => ({
  getAnkiStatus: mocks.status,
  requestAnkiPermission: vi.fn(async () => "granted"),
  syncAnki: mocks.sync,
  saveVocabularyProvider: mocks.save,
}));

describe("VocabularySettings", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("zh-CN");
    mocks.desktop.mockResolvedValue({ installed: true, executable_path: "/Applications/Anki.app", addon_code: "2055492159" });
    vi.clearAllMocks();
  });

  it("offers Anki, Eudic and MaiMemo configuration together", async () => {
    mocks.status.mockResolvedValue({ available: true, enabled: false, connected: false });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    expect(await screen.findByRole("button", { name: "Anki 配置" })).toBeVisible();
    expect(screen.getByRole("button", { name: "欧路词典 配置" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "墨墨背单词 配置" }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "墨墨背单词" })).toBeVisible());
  });

  it("keeps cloud app configuration available without deployment support for Anki", async () => {
    mocks.status.mockResolvedValue({ available: false, enabled: false, connected: false });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    await waitFor(() => expect(screen.getByText("当前部署未提供 Anki 同步。")).toBeVisible());
    const setupGuide = screen.getByText("连接指南");
    expect(setupGuide).toBeVisible();
    expect(setupGuide.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText(/2055492159/)).toBeInTheDocument();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(mocks.desktop).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "欧路词典 配置" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "墨墨背单词 配置" })).toBeEnabled();
  });

  it("localizes the Anki setup flow in English", async () => {
    await i18n.changeLanguage("en-US");
    mocks.desktop.mockResolvedValue({ installed: false });
    mocks.status.mockResolvedValue({ available: true, enabled: false, connected: false });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki Configure" }));
    await waitFor(() => expect(screen.getByText("Anki setup")).toBeVisible());
    expect(screen.getByText("Sync vocabulary to Anki")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "View installation guide" }));
    await waitFor(() => expect(screen.getByText("Install Anki").closest("[role=dialog]")).toBeVisible());
  });

  it("shows installation state when AnkiConnect is missing", async () => {
    mocks.status.mockResolvedValue({ available: true, enabled: false, connected: false, initialized: false, pending_operations: 0 });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    expect(screen.getByText("Anki 客户端已安装")).toBeTruthy();
    expect(screen.getByText("尚未连接 AnkiConnect")).toBeTruthy();
  });

  it("lets the user opt into Anki sync before the backend probes the connection", async () => {
    mocks.status.mockResolvedValue({ available: true, enabled: false, connected: false, initialized: false, pending_operations: 0 });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    expect(screen.getByText("同步生词到 Anki")).toBeTruthy();
    fireEvent.click(screen.getByRole("switch"));
    await waitFor(() => expect(mocks.save).toHaveBeenCalledWith({ anki_sync_enabled: true }));
  });

  it("runs manual sync for an enabled connection", async () => {
    mocks.status.mockResolvedValue({ available: true, enabled: true, connected: true, initialized: true });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    fireEvent.click(await screen.findByRole("button", { name: "立即同步" }));
    await waitFor(() => expect(mocks.sync).toHaveBeenCalledTimes(1));
  });

  it("asks the user to open Anki when the add-on is already installed", async () => {
    mocks.desktop.mockResolvedValue({ installed: true, connect_installed: true, executable_path: "/Applications/Anki.app", addon_code: "2055492159" });
    mocks.status.mockResolvedValue({ available: true, enabled: false, connected: false, initialized: false, pending_operations: 0 });
    render(<ConfigProvider theme={{ token: { motion: false } }}><VocabularySettings /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Anki 配置" }));
    expect((await screen.findAllByRole("button", { name: "打开 Anki" })).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "安装 AnkiConnect" })).toBeNull();
    expect(screen.getByText(/AnkiConnect 已安装，但 Anki 尚未运行/)).toBeTruthy();
  });
});
