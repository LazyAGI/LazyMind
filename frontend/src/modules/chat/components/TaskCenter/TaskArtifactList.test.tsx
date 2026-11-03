import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicArtifact } from "@/modules/chat/types/ordinaryTask";
import { MemoryRouter } from "react-router-dom";
import * as sourceAPI from "../SourceImports/api";
import { TaskArtifactList } from "./TaskArtifactList";
import { downloadArtifactFile } from "@/modules/chat/utils/artifactFileActions";
import { ArtifactPreviewContext } from "../ArtifactPreview/context";
import { useState, type ReactNode } from 'react';
import ArtifactPreview from '../ArtifactPreview';
import type { ArtifactPreviewTarget } from '../ArtifactPreview/target';

vi.mock("../SourceImports/api", () => ({ inspectSources: vi.fn(), listSourceImports: vi.fn(), sourceTargets: vi.fn(), submitSourceImport: vi.fn() }));
vi.mock("@/modules/knowledge/components/FileViewer", () => ({ default: ({ data, renderMarkdown }: { data?: ArrayBuffer; renderMarkdown?: (value: string) => ReactNode }) =>
  data && renderMarkdown ? renderMarkdown(new TextDecoder().decode(data)) : <div>File viewer</div> }));
vi.mock("@/modules/chat/utils/artifactFileActions", () => ({ downloadArtifactFile: vi.fn() }));
vi.mock("react-i18next", async importOriginal => ({ ...(await importOriginal<typeof import("react-i18next")>()), useTranslation: () => ({ t: (key: string) => key }) }));
const file = (overrides: Partial<PublicArtifact> = {}): PublicArtifact => ({
  artifact_id: "artifact-1", revision: 1, producer_display_key: "task-1", name: "Report.md", content_type: "text/markdown",
  size_bytes: null, state: "ready", preview_kind: "text", capabilities: { preview: true, open: false, download: true },
  inline_content: "# Public report\n<script>window.evil = true</script>", created_at: "2026-09-23T00:00:00Z", ...overrides,
});
afterEach(() => { cleanup(); vi.clearAllMocks(); });

function PreviewHarness({ children, shared }: { children: ReactNode; shared: boolean }) {
  const [target, setTarget] = useState<ArtifactPreviewTarget | null>(null);
  return shared ? <ArtifactPreviewContext.Provider value={{ target, open: setTarget, close: () => setTarget(null), onVersions: undefined }}>
    {children}{target && <ArtifactPreview target={target} onClose={() => setTarget(null)} />}
  </ArtifactPreviewContext.Provider> : children;
}

describe("ordinary stage artifacts", () => {
  it.each([false, true])("uses the producing Work task for actual citation import in a result preview (shared: %s)", async shared => {
    const source = { source_type: "external" as const, citation_id: "1.1", title: "ReAct", url: "https://arxiv.org/abs/2210.03629" };
    vi.mocked(sourceAPI.inspectSources).mockResolvedValue([{ source_id: "1.1", title: "ReAct", url: source.url, eligible: true, paper_key: "arxiv:2210.03629" }]);
    vi.mocked(sourceAPI.listSourceImports).mockResolvedValue([]);
    render(<MemoryRouter><PreviewHarness shared={shared}><TaskArtifactList artifacts={[file({ inline_content: "ReAct [1](#source-1.1)" })]} sourceContexts={{ "task-1": { context: { task_id: "task-1" }, sources: [source] } }} /></PreviewHarness></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", { name: "Report.md" }));
    expect(sourceAPI.inspectSources).not.toHaveBeenCalled();
    const citation = await waitFor(() => { const element = document.querySelector("a.md-source-chip"); expect(element).toBeTruthy(); return element!; });
    expect(citation).toBeTruthy();
    fireEvent.focus(citation);
    await waitFor(() => expect(sourceAPI.inspectSources).toHaveBeenCalledWith({ task_id: "task-1" }));
    expect(await screen.findByRole("button", { name: /加入(?:知识库|资料库)/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "打开原文" })).toBeInTheDocument();
  });
  it.each([false, true])("passes durable operation receipts into the Work preview (shared: %s)", async shared => {
    render(<MemoryRouter><PreviewHarness shared={shared}><TaskArtifactList artifacts={[file()]} sourceContexts={{ "task-1": {
      context: { task_id: "task-1" }, sources: [], operations: [{ call_id: "operation", action: "knowledge.create", status: "success", items: [
        { name: "Created library", resource_ref: { kind: "knowledge_base", knowledge_base_id: "created" }, resource_status: "created" },
      ] }],
    } }} /></PreviewHarness></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", { name: "Report.md" }));
    fireEvent.click(await screen.findByRole("button", { name: "Agent 执行结果" }));
    expect(await screen.findByRole("link", { name: "Created library" })).toHaveAttribute("href", "/lib/knowledge/detail/created");
    expect(sourceAPI.inspectSources).not.toHaveBeenCalled();
  });
  it("passes the producing workflow step with the exact artifact revision", () => {
    const preview = { target: null, open: vi.fn(), close: vi.fn(), onVersions: undefined };
    render(<ArtifactPreviewContext.Provider value={preview}>
      <TaskArtifactList artifacts={[file({ revision: 2 })]} workflow={{ sessionId: 'session-1', stepId: 'prepare' }} />
    </ArtifactPreviewContext.Provider>);
    fireEvent.click(screen.getByRole('button', { name: 'taskCenter.ordinaryPreview Report.md' }));
    expect(preview.open).toHaveBeenCalledWith(expect.objectContaining({
      key: 'artifact-1:2', workflow: { sessionId: 'session-1', stepId: 'prepare' },
    }));
  });

  it("keeps an explicit preview action when using the shared preview panel", async () => {
    const preview = { target: null, open: vi.fn(), close: vi.fn(), onVersions: undefined };
    render(<ArtifactPreviewContext.Provider value={preview}>
      <TaskArtifactList artifacts={[file({ open_url: "/api/core/artifacts/report/open", download_url: "/api/core/artifacts/report/download", capabilities: { preview: true, open: true, download: true } })]} />
    </ArtifactPreviewContext.Provider>);
    fireEvent.click(screen.getByRole("button", { name: "taskCenter.ordinaryPreview Report.md" }));
    expect(preview.open).toHaveBeenCalledWith(expect.objectContaining({
      key: "artifact-1:1", filename: "Report.md", content: file().inline_content,
    }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(downloadArtifactFile).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Report.md" }));
    expect(preview.open).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole("button", { name: "taskCenter.download Report.md" }));
    await waitFor(() => expect(downloadArtifactFile).toHaveBeenCalledWith("/api/core/artifacts/report/download", "Report.md"));
    expect(preview.open).toHaveBeenCalledTimes(2);
  });

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
  it("routes signed static files through Core for downloading without an open action", async () => {
    const signed = "/static-files/workflow-artifacts/session/report.md?expires=4102444800&sig=test";
    render(<TaskArtifactList artifacts={[file({ inline_content: undefined, open_url: signed, download_url: signed, capabilities: { preview: false, open: true, download: true } })]} />);
    const expected = `/api/core${signed}`;
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
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
