import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DesktopUpdateState } from "@/runtime/desktopUpdates";
import { MemoryRouter } from "react-router-dom";

const bridge = vi.hoisted(() => ({ status: vi.fn(), check: vi.fn(), download: vi.fn(), cancel: vi.fn(), reveal: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn() }));
vi.mock("@/runtime/desktopUpdates", () => ({ desktopUpdateStatus: bridge.status, desktopUpdateCheck: bridge.check, desktopUpdateDownload: bridge.download, desktopUpdateCancel: bridge.cancel, desktopUpdateReveal: bridge.reveal, onDesktopUpdateState: bridge.subscribe }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values: Record<string, unknown> = {}) => `${key}${Object.keys(values).length ? ` ${Object.values(values).join(" ")}` : ""}` }) }));
import DesktopUpdateSettings, { DesktopUpdateNotice } from "./DesktopUpdateSettings";

const state = (extra: Partial<DesktopUpdateState> = {}): DesktopUpdateState => ({ revision: 1, phase: "available", currentVersion: "1.0.0", channel: "stable", releaseId: "r2", nextVersion: "1.1.0", targetVersion: "2.0.0", route: ["r2", "r3"], canInstall: false, ...extra });
let listener: (value: DesktopUpdateState) => void;

beforeEach(() => {
  vi.clearAllMocks();
  bridge.status.mockResolvedValue(state()); bridge.check.mockResolvedValue(state());
  bridge.download.mockResolvedValue(state({ phase: "downloading" }));
  bridge.cancel.mockResolvedValue(state());
  bridge.subscribe.mockImplementation((next) => { listener = next; return bridge.unsubscribe; });
});
afterEach(cleanup);

it("shows the next version and bridge steps and binds download to the displayed release", async () => {
  const { unmount } = render(<DesktopUpdateSettings />);
  expect(await screen.findByText(/1\.1\.0/)).toBeInTheDocument();
  expect(screen.getByText(/desktopUpdates.bridge.*2/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "desktopUpdates.download" }));
  await waitFor(() => expect(bridge.download).toHaveBeenCalledWith("r2"));
  unmount(); expect(bridge.unsubscribe).toHaveBeenCalledOnce(); expect(bridge.cancel).not.toHaveBeenCalled();
});

it("exposes cancel during progress and never labels a download as a completed update", async () => {
  bridge.status.mockResolvedValue(state({ phase: "downloading", downloadedBytes: 25, totalBytes: 100 }));
  render(<DesktopUpdateSettings />);
  fireEvent.click(await screen.findByRole("button", { name: "desktopUpdates.cancel" }));
  await waitFor(() => expect(bridge.cancel).toHaveBeenCalledOnce());
  act(() => listener(state({ revision: 2, phase: "downloaded" })));
  expect(await screen.findByRole("button", { name: "desktopUpdates.reveal" })).toBeInTheDocument();
  expect(screen.queryByText("desktopUpdates.installed")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "desktopUpdates.restart" })).not.toBeInTheDocument();
});

it("makes check failure and data incompatibility visible without a download action", async () => {
  bridge.status.mockResolvedValue(state({ phase: "error", reason: "policy_signature", releaseId: undefined }));
  render(<DesktopUpdateSettings />);
  expect(await screen.findByRole("alert")).toHaveTextContent("desktopUpdates.reasons.policy_signature");
  expect(screen.queryByRole("button", { name: "desktopUpdates.download" })).not.toBeInTheDocument();
  act(() => listener(state({ revision: 2, phase: "blocked", reason: "data_dirty" })));
  expect(screen.getByRole("alert")).toHaveTextContent("desktopUpdates.reasons.data_dirty");
  expect(screen.queryByRole("button", { name: "desktopUpdates.download" })).not.toBeInTheDocument();
});

it("does not overwrite a newer event with a delayed initial snapshot", async () => {
  let resolve!: (value: DesktopUpdateState) => void;
  bridge.status.mockReturnValue(new Promise<DesktopUpdateState>((done) => { resolve = done; }));
  render(<DesktopUpdateSettings />);
  expect(bridge.subscribe).toHaveBeenCalledOnce();
  act(() => listener(state({ revision: 5, phase: "downloaded" })));
  await act(async () => resolve(state({ revision: 1, phase: "available" })));
  expect(screen.getByRole("button", { name: "desktopUpdates.reveal" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "desktopUpdates.download" })).not.toBeInTheDocument();
});

it("makes an available update discoverable in the app shell and links to its settings", async () => {
  render(<MemoryRouter><DesktopUpdateNotice /></MemoryRouter>);
  const details = await screen.findByRole("link", { name: "desktopUpdates.details" });
  expect(details).toHaveAttribute("href", "/settings?section=updates");
  fireEvent.click(screen.getByRole("button", { name: "desktopUpdates.download" }));
  await waitFor(() => expect(bridge.download).toHaveBeenCalledWith("r2"));
});
