import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicArtifact } from "@/modules/chat/types/ordinaryTask";
import { TaskArtifactList } from "./TaskArtifactList";
import { downloadArtifactFile } from "@/modules/chat/utils/artifactFileActions";

vi.mock("@/modules/knowledge/components/FileViewer", () => ({ default: () => <div>File viewer</div> }));
vi.mock("@/modules/chat/utils/artifactFileActions", () => ({ downloadArtifactFile: vi.fn() }));
vi.mock("react-i18next", async importOriginal => ({ ...(await importOriginal<typeof import("react-i18next")>()), useTranslation: () => ({ t: (key: string) => key }) }));
const file = (overrides: Partial<PublicArtifact> = {}): PublicArtifact => ({
  artifact_id: "artifact-1", revision: 1, producer_display_key: "task-1", name: "Report.md", content_type: "text/markdown",
  size_bytes: null, state: "ready", preview_kind: "text", capabilities: { preview: true, open: false, download: true },
  inline_content: "# Public report\n<script>window.evil = true</script>", created_at: "2026-09-23T00:00:00Z", ...overrides,
});
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("ordinary stage artifacts", () => {
  it("hides an empty collection and deduplicates repeated IDs", () => {
    const { rerender } = render(<TaskArtifactList artifacts={[]} />);
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
    rerender(<TaskArtifactList artifacts={[file(), file()]} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
    expect(screen.getByText(/ordinaryUnknownSize/)).toBeInTheDocument();
  });
  it("previews inline content as text, traps focus and restores the initiating control", () => {
    render(<TaskArtifactList artifacts={[file()]} onReload={vi.fn()} />);
    const trigger = screen.getByRole("button", { name: "Report.md" });
    trigger.focus(); fireEvent.click(trigger);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(screen.getByRole("dialog")).queryByRole("button", { name: "taskCenter.ordinaryReload" })).not.toBeInTheDocument();
    expect(screen.getByText(/<script>window.evil/)).toBeInTheDocument();
    expect(screen.getByRole("dialog").querySelector("script")).toBeNull();
    const close = screen.getByRole("button", { name: "chat.closePreview" });
    expect(close).toHaveFocus();
    fireEvent.keyDown(close, { key: "Tab" }); expect(close).toHaveFocus();
    fireEvent.keyDown(close, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
  it("honors capabilities and refuses unsafe artifact URLs", () => {
    render(<TaskArtifactList artifacts={[file({ inline_content: undefined, preview_url: "javascript:alert(1)", open_url: "data:text/html,x", download_url: "file:///etc/passwd", capabilities: { preview: true, open: true, download: true } })]} />);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
  it("routes signed static files through Core for opening and downloading", async () => {
    const signed = "/static-files/workflow-artifacts/session/report.md?expires=4102444800&sig=test";
    render(<TaskArtifactList artifacts={[file({ inline_content: undefined, open_url: signed, download_url: signed, capabilities: { preview: false, open: true, download: true } })]} />);
    const expected = `/api/core${signed}`;
    expect(screen.getByRole("link")).toHaveAttribute("href", expected);
    fireEvent.click(screen.getByRole("button", { name: "taskCenter.download Report.md" }));
    await waitFor(() => expect(downloadArtifactFile).toHaveBeenCalledWith(expected, "Report.md"));
  });
  it("uses existing download actions and offers read-only reload after failure", async () => {
    vi.mocked(downloadArtifactFile).mockRejectedValueOnce(new Error("expired"));
    const reload = vi.fn().mockResolvedValue(undefined);
    render(<TaskArtifactList artifacts={[file({ inline_content: undefined, download_url: "/api/core/artifacts/a/download" })]} onReload={reload} />);
    fireEvent.click(screen.getByRole("button", { name: "taskCenter.download Report.md" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("ordinaryArtifactActionFailed");
    expect(downloadArtifactFile).toHaveBeenCalledWith("/api/core/artifacts/a/download", "Report.md");
    fireEvent.click(screen.getByRole("button", { name: "taskCenter.ordinaryReload" }));
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  });
  it("records only fixed local performance markers when artifact actions fail", async () => {
    const mark = vi.fn();
    const original = Object.getOwnPropertyDescriptor(performance, "mark");
    Object.defineProperty(performance, "mark", { configurable: true, value: mark });
    try {
      vi.mocked(downloadArtifactFile).mockRejectedValueOnce(new Error("TEST_PRIVATE_FAILURE"));
      const reload = vi.fn().mockRejectedValue(new Error("TEST_PRIVATE_RELOAD_FAILURE"));
      render(<TaskArtifactList artifacts={[file({ download_url: "/api/core/artifacts/test/download?signature=TEST_PRIVATE_SIGNATURE" })]} onReload={reload} />);
      fireEvent.click(screen.getByRole("button", { name: "taskCenter.download Report.md" }));
      await screen.findByRole("alert");
      fireEvent.click(screen.getByRole("button", { name: "taskCenter.ordinaryReload" }));
      await waitFor(() => expect(mark).toHaveBeenCalledTimes(2));
      expect(mark.mock.calls).toEqual([
        ["lazymind.task_display.artifact_action_failed"],
        ["lazymind.task_display.artifact_action_failed"],
      ]);
    } finally {
      if (original) Object.defineProperty(performance, "mark", original);
      else Reflect.deleteProperty(performance, "mark");
    }
  });

});
