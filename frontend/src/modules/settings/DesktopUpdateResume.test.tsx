import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import type { DesktopUpdateState } from "@/runtime/desktopUpdates";

const bridge = vi.hoisted(() => ({ status: vi.fn(), resume: vi.fn(), cancel: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn(), check: vi.fn(), download: vi.fn() }));
vi.mock("@/runtime/desktopUpdates", () => ({ desktopUpdateStatus: bridge.status, desktopUpdateResume: bridge.resume,
  desktopUpdateCheck: bridge.check, desktopUpdateDownload: bridge.download, desktopUpdateCancel: bridge.cancel,
  desktopUpdateReveal: vi.fn(), onDesktopUpdateState: bridge.subscribe }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import DesktopUpdateSettings, { DesktopUpdateNotice } from "./DesktopUpdateSettings";
const state: DesktopUpdateState = { revision: 1, phase: "interrupted", pendingDownload: true, currentVersion: "1.0.0", nextVersion: "1.1.0", releaseId: "fixture-r2", canInstall: false };
let listener: (value: DesktopUpdateState) => void;
beforeEach(() => {
  vi.clearAllMocks();
  bridge.status.mockResolvedValue(state); bridge.resume.mockResolvedValue({ ...state, revision: 2, phase: "downloading", pendingDownload: false });
  bridge.cancel.mockResolvedValue({ revision: 2, phase: "idle", currentVersion: "1.0.0", canInstall: false });
  bridge.subscribe.mockImplementation(next => { listener = next; return bridge.unsubscribe; });
});
afterEach(cleanup);

it("shows the unfinished update with explicit continue and cancel, without starting it on mount", async () => {
  render(<DesktopUpdateSettings />);
  expect(await screen.findByText("desktopUpdates.interrupted")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "desktopUpdates.check" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "desktopUpdates.cancelUpdate" })).toBeEnabled();
  expect(bridge.resume).not.toHaveBeenCalled(); expect(bridge.download).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "desktopUpdates.resumeDownload" }));
  await waitFor(() => expect(bridge.resume).toHaveBeenCalledOnce());
  expect(bridge.resume).toHaveBeenCalledWith();
  expect(await screen.findByRole("button", { name: "desktopUpdates.cancel" })).toBeInTheDocument();
});

it("keeps continuation errors visible and allows retry even without release fields in the error snapshot", async () => {
  render(<DesktopUpdateSettings />); await screen.findByText("desktopUpdates.interrupted");
  act(() => listener({ revision: 3, phase: "error", pendingDownload: true, currentVersion: "1.0.0", canInstall: false, reason: "network_unavailable" }));
  expect(screen.getByRole("alert")).toHaveTextContent("desktopUpdates.reasons.network_unavailable");
  expect(screen.getByRole("button", { name: "desktopUpdates.resumeDownload" })).toBeEnabled();
  expect(screen.queryByRole("button", { name: "desktopUpdates.download" })).not.toBeInTheDocument();
});

it("the global notice continues or cancels the pending download and is removed after cancellation", async () => {
  render(<MemoryRouter><DesktopUpdateNotice /></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: "desktopUpdates.cancelUpdate" }));
  await waitFor(() => expect(bridge.cancel).toHaveBeenCalledOnce());
  await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  expect(bridge.resume).not.toHaveBeenCalled();
});

it("closing settings unsubscribes but leaves the unfinished update intact", async () => {
  const { unmount } = render(<DesktopUpdateSettings />); await screen.findByText("desktopUpdates.interrupted"); unmount();
  expect(bridge.unsubscribe).toHaveBeenCalledOnce(); expect(bridge.cancel).not.toHaveBeenCalled();
});

it("the global notice reports a failed continuation and keeps its retry available", async () => {
  bridge.status.mockResolvedValue({ ...state, phase: "error", reason: "policy_signature" });
  render(<MemoryRouter><DesktopUpdateNotice /></MemoryRouter>);
  expect(await screen.findByText("desktopUpdates.reasons.policy_signature")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "desktopUpdates.resumeDownload" })).toBeEnabled();
});

it("the global notice keeps an ordinary download record failure visible with a retry action", async () => {
  bridge.status.mockResolvedValue({ ...state, phase: "error", pendingDownload: false, reason: "update_record_write" });
  render(<MemoryRouter><DesktopUpdateNotice /></MemoryRouter>);
  expect(await screen.findByText("desktopUpdates.reasons.update_record_write")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "desktopUpdates.download" })).toBeEnabled();
});

it("the global notice exposes a record-read error with an explicit check action", async () => {
  bridge.status.mockResolvedValue({ revision: 1, phase: "error", reason: "update_record_read", currentVersion: "1.0.0", canInstall: false });
  render(<MemoryRouter><DesktopUpdateNotice /></MemoryRouter>);
  expect(await screen.findByText("desktopUpdates.reasons.update_record_read")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "desktopUpdates.check" })).toBeEnabled();
});
