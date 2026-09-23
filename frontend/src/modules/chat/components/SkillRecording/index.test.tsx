import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Panel from ".";
import Review from "./Review";
import * as api from "./api";
import { collectDesktopEvidence, desktopInputAvailable, prepareDesktopInput } from "./desktopEvidence";
import { captureScreen } from "./capture";
vi.mock("./api");
vi.mock("./capture");
vi.mock("./desktopEvidence");
vi.mock("react-i18next", async (original) => ({ ...(await original<typeof import("react-i18next")>()), useTranslation: () => ({ t: translate }) }));
const translate = (key: string) => key;
const pending: api.SkillRecording = { id: "r", conversation_id: "c", skill_id: "s", name: "Example", description: "Steps", error: "", status: "pending" };
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.listRecordings).mockResolvedValue([]);
  vi.mocked(api.recordingTargets).mockResolvedValue([{ device_id: "device", tab_id: "1", title: "Page", origin: "https://example.com" }]);
  vi.mocked(collectDesktopEvidence).mockResolvedValue({ finish: vi.fn().mockResolvedValue({ events: [], limitations: [] }), cancel: vi.fn().mockResolvedValue(undefined) });
  vi.mocked(api.recordingSetup).mockResolvedValue({ installed: false });
  vi.mocked(desktopInputAvailable).mockReturnValue(true);
  vi.mocked(prepareDesktopInput).mockResolvedValue(undefined);
});
describe("recording flow", () => {
  it("requires installation before screen selection and cancellation never submits", async () => {
    const cancel = vi.fn();
    vi.mocked(captureScreen).mockResolvedValue({ stop: vi.fn(), cancel, startedAt: Date.now() });
    vi.mocked(api.recordingSetup).mockImplementation(async (install) => ({ installed: Boolean(install) }));
    render(<MemoryRouter><Panel open conversationId="c" onClose={vi.fn()} /></MemoryRouter>);
    const install = await screen.findByText("recording.install");
    await waitFor(() => expect(install.closest("button")).not.toHaveClass("ant-btn-loading"));
    expect(captureScreen).not.toHaveBeenCalled(); fireEvent.click(install);
    fireEvent.click(await screen.findByText("recording.start"));
    await screen.findByText("recording.stop"); fireEvent.click(screen.getByText("common.cancel"));
    expect(cancel).toHaveBeenCalledOnce(); expect(api.submitRecording).not.toHaveBeenCalled();
    expect(api.recordingTargets).not.toHaveBeenCalled();
    expect(collectDesktopEvidence).toHaveBeenCalledOnce();
  });
  it("shows permission guidance without submitting", async () => {
    vi.mocked(api.recordingSetup).mockResolvedValue({ installed: true });
    vi.mocked(captureScreen).mockRejectedValue(new DOMException("denied", "NotAllowedError"));
    render(<MemoryRouter><Panel open conversationId="c" onClose={vi.fn()} /></MemoryRouter>);
    fireEvent.click(await screen.findByText("recording.start"));
    expect(await screen.findByText("recording.permission")).toBeVisible();
    expect(api.submitRecording).not.toHaveBeenCalled();
  });
  it("distinguishes capture startup failure from missing browser support", async () => {
    vi.mocked(api.recordingSetup).mockResolvedValue({ installed: true });
    vi.mocked(captureScreen).mockRejectedValue(new DOMException("Error starting capture", "AbortError"));
    render(<MemoryRouter><Panel open conversationId="c" onClose={vi.fn()} /></MemoryRouter>);
    fireEvent.click(await screen.findByText("recording.start"));
    expect(await screen.findByText("recording.captureAborted")).toBeVisible();
    expect(screen.queryByText("recording.unsupported")).not.toBeInTheDocument();
    expect(screen.queryByText("recording.openInputSettings")).not.toBeInTheDocument();
    expect(api.submitRecording).not.toHaveBeenCalled();
  });
  it("requires system permission before screen recording", async () => {
    vi.mocked(api.recordingSetup).mockResolvedValue({ installed: true });
    vi.mocked(prepareDesktopInput).mockRejectedValue(new Error("recording-input-permission"));
    render(<MemoryRouter><Panel open conversationId="c" onClose={vi.fn()} /></MemoryRouter>);
    fireEvent.click(await screen.findByText("recording.start"));
    expect(await screen.findByText("recording.inputPermission")).toBeVisible();
    expect(captureScreen).not.toHaveBeenCalled();
  });
  it("explains the web limitation without requesting DOM or browser tabs", async () => {
    vi.mocked(desktopInputAvailable).mockReturnValue(false);
    render(<MemoryRouter><Panel open conversationId="c" onClose={vi.fn()} /></MemoryRouter>);
    expect(await screen.findByText("recording.webActions")).toBeVisible();
    expect(api.recordingTargets).not.toHaveBeenCalled();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
  it("renders durable pending result and review entry", async () => {
    vi.mocked(api.listRecordings).mockResolvedValue([pending]);
    render(<MemoryRouter><Panel open={false} conversationId="c" onClose={vi.fn()} /></MemoryRouter>);
    expect(await screen.findByText("Example")).toBeVisible();
    expect(screen.getByText("recording.status.pending")).toBeVisible();
    expect(screen.getByText("recording.review")).toBeVisible();
  });
  it.each([true, false])("submits decision keep=%s and updates the management page", async (keep) => {
    vi.mocked(api.listRecordings).mockResolvedValue([pending]);
    const decided = vi.fn();
    render(<Review skillId="s" onDecision={decided} />);
    fireEvent.click(await screen.findByText(keep ? "recording.keep" : "recording.discard"));
    await waitFor(() => expect(api.decideRecording).toHaveBeenCalledWith("r", keep));
    expect(decided).toHaveBeenCalledWith(keep);
  });
});
