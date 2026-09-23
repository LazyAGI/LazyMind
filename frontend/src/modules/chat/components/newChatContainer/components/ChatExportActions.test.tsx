import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import ChatExportActions from "./ChatExportActions";
import ArtifactCollectorCard from "../../ArtifactCollectorCard";

const api = vi.hoisted(() => ({ create: vi.fn(), error: vi.fn(), download: vi.fn() }));
vi.mock("react-i18next", async (importOriginal) => ({ ...await importOriginal<typeof import("react-i18next")>(), useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/chat/utils/request", () => ({ TaskServiceApi: () => ({ createConversationArtifact: api.create }) }));
vi.mock("@/components/request", async (importOriginal) => ({ ...await importOriginal<typeof import("@/components/request")>(), getLocalizedErrorMessage: () => "failed" }));
vi.mock("antd", async (importOriginal) => ({ ...await importOriginal<typeof import("antd")>(), message: { error: api.error } }));
vi.mock("../../MarkdownViewer", () => ({ default: ({ children }: { children: string }) => <div>{children}</div> }));
vi.mock("@/modules/chat/utils/download", () => ({ downloadStream: api.download }));

const item = { index: 0, title: "报告", filename: "报告.md", content_type: "text/markdown" as const, start: 3, end: 7, export_id: "export-1" };
const props = { content: "前😀报告😀后", exports: [item], conversationId: "conv", historyId: "history" };
const artifact = { artifact_id: "export-1", history_id: "history", filename: "报告.md", content_type: "text", value: { text: "报告😀" } };

beforeEach(() => {
  api.create.mockReset(); api.error.mockReset(); api.download.mockReset();
  useTaskCenterStore.setState({ artifactsByConversation: {}, loadConversationArtifacts: vi.fn(async () => {}) });
});

describe("Chat export actions", () => {
  it("saves the exact slice once, opens the file list with download, and reopens saved artifacts", async () => {
    let resolve: (value: unknown) => void = () => {};
    api.create.mockReturnValue(new Promise((done) => { resolve = done; }));
    const view = render(<ChatExportActions {...props} />);
    expect(screen.getByRole("button")).toBeDisabled();
    act(() => useTaskCenterStore.setState({ artifactsByConversation: { conv: [] } }));
    fireEvent.click(screen.getByRole("button"));
    fireEvent.click(screen.getByRole("button"));
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(api.create).toHaveBeenCalledWith("conv", {
      history_id: "history", export_id: "export-1", filename: "报告.md", content_type: "text/markdown", content: "报告😀",
    });
    await act(async () => resolve({ data: { data: artifact } }));
    expect(screen.getByRole("dialog")).toBeVisible();
    expect(screen.queryByText("报告😀")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "报告.md", exact: true }));
    expect(screen.getByText("报告😀")).toBeVisible();
    fireEvent.click(screen.getByTitle("chat.artifactCollectorDownload 报告.md"));
    await waitFor(() => expect(api.download).toHaveBeenCalledWith(expect.any(Blob), "报告.md"));
    const blob = api.download.mock.calls[0][0] as Blob;
    const downloaded = await new Promise((done) => {
      const reader = new FileReader();
      reader.onload = () => done(reader.result);
      reader.readAsText(blob);
    });
    expect(downloaded).toBe("报告😀");
    view.unmount();
    render(<ChatExportActions {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.exportView · 报告.md" }));
    expect(screen.getByRole("dialog")).toBeVisible();
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("报告😀")).not.toBeInTheDocument();
  });

  it("saves a portable citation snapshot and retains it after sources change", async () => {
    useTaskCenterStore.setState({ artifactsByConversation: { conv: [] } });
    const body = "结论见[1](#source-1.1)。\n\n`[1](#source-1.1)`";
    const exports = [{ ...item, start: 0, end: body.length }];
    const sources = [{ citation_id: "1.1", source_type: "external" as const, title: "原始资料", url: "https://example.com/report", content: "原始证据" }];
    api.create.mockImplementation(async (_id, request) => ({ data: { ...artifact, value: { text: request.content, chat_export: true } } }));
    const view = render(<ChatExportActions {...props} content={body} exports={exports} sources={sources} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.exportSave · 报告.md" }));
    await screen.findByRole("dialog");
    const saved = api.create.mock.calls[0][1].content;
    expect(saved).toContain("[1](https://example.com/report)");
    expect(saved).toContain("原始资料");
    expect(saved).toContain("原始证据");
    expect(saved).toContain("`[1](#source-1.1)`");
    view.rerender(<ChatExportActions {...props} content={body} exports={exports} sources={[{ ...sources[0], url: "https://example.com/new" }]} />);
    fireEvent.click(screen.getByRole("button", { name: "报告.md", exact: true }));
    expect(screen.getByRole("region", { name: "chat.exportPreviewing" })).toHaveTextContent("https://example.com/report");
    fireEvent.click(screen.getByTitle("chat.artifactCollectorDownload 报告.md"));
    await waitFor(() => expect(api.download).toHaveBeenCalled());
    const downloaded = await new Promise((done) => {
      const reader = new FileReader(); reader.onload = () => done(reader.result);
      reader.readAsText(api.download.mock.calls[0][0]);
    });
    expect(downloaded).toBe(saved);
  });

  it("allows retry after failure", async () => {
    useTaskCenterStore.setState({ artifactsByConversation: { conv: [] } });
    api.create.mockRejectedValueOnce(new Error("network"));
    api.create.mockResolvedValueOnce({ data: artifact });
    render(<ChatExportActions {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.exportSave · 报告.md" }));
    await waitFor(() => expect(api.error).toHaveBeenCalled());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.exportSave · 报告.md" }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeVisible());
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "chat.exportView · 报告.md" })).toBeEnabled();
  });
  it("uses one entry for multiple exports and saves only the selected file", async () => {
    useTaskCenterStore.setState({ artifactsByConversation: { conv: [] } });
    const appendix = { ...item, title: "附录", filename: "附录.md", index: 1, start: 7, end: 8, export_id: "export-2" };
    api.create.mockResolvedValue({ data: { ...artifact, artifact_id: "export-2", filename: "附录.md", value: { text: "后" } } });
    render(<ChatExportActions {...props} exports={[item, appendix]} />);
    expect(screen.getAllByRole("button")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "chat.exportSave" }));
    const choice = await screen.findByRole("menuitem", { name: /附录.md/ });
    expect(choice).toHaveTextContent("Markdown (.md)");
    expect(api.create).not.toHaveBeenCalled();
    fireEvent.click(choice);
    await waitFor(() => expect(screen.getByRole("dialog")).toBeVisible());
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(api.create).toHaveBeenCalledWith("conv", {
      history_id: "history", export_id: "export-2", filename: "附录.md", content_type: "text/markdown", content: "后",
    });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "chat.exportSave" }));
    expect(await screen.findByRole("menuitem", { name: /报告.md/ })).toHaveTextContent("chat.exportSave");
    const saved = screen.getByRole("menuitem", { name: /附录.md/ });
    expect(saved).toHaveTextContent("chat.exportView");
    fireEvent.click(saved);
    await waitFor(() => expect(screen.getByRole("dialog")).toBeVisible());
    expect(api.create).toHaveBeenCalledTimes(1);
  });

  it("keeps same-name historical files distinct and shows their saved times", async () => {
    const stored = [
      { ...artifact, created_at: "2026-09-22T10:00:00Z", value: { text: "报告😀", chat_export: true } },
      { ...artifact, artifact_id: "previous", created_at: "2026-09-22T09:00:00Z", value: { text: "旧报告", chat_export: true } },
      { ...artifact, artifact_id: "legacy", filename: "tool.md", created_at: "2026-09-21T09:00:00Z", value: { text: "旧版无来源记录" } },
    ];
    useTaskCenterStore.setState({ artifactsByConversation: { conv: stored as any } });
    render(<ChatExportActions {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.exportView · 报告.md" }));
    const current = screen.getByText("chat.exportCurrentGeneration").closest(".artifact-collector__file-item") as HTMLElement;
    const previous = screen.getByText("chat.exportPreviousGeneration").closest(".artifact-collector__file-item") as HTMLElement;
    expect(current).toHaveTextContent("报告.md");
    expect(previous).toHaveTextContent("报告.md");
    expect(within(current).getByText(/chat.exportSavedAt/)).toHaveAttribute("dateTime", stored[0].created_at);
    expect(within(previous).getByText(/chat.exportSavedAt/)).toHaveAttribute("dateTime", stored[1].created_at);
    const ordinary = screen.getByText("tool.md").closest(".artifact-collector__file-item") as HTMLElement;
    expect(ordinary).not.toBeNull();
    expect(within(ordinary).queryByText(/chat.export(Current|Previous|Unknown)Generation/)).not.toBeInTheDocument();
    expect(screen.queryByText("chat.exportUnknownGeneration")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "chat.exportPreviewing" })).not.toBeInTheDocument();
    fireEvent.click(within(previous).getByRole("button", { name: "报告.md", exact: true }));
    expect(screen.getByRole("region", { name: "chat.exportPreviewing" })).toHaveTextContent("旧报告");
    expect(within(previous).getByText("chat.exportPreviewing")).toBeVisible();
    fireEvent.click(within(current).getByRole("checkbox"));
    expect(screen.getByRole("region", { name: "chat.exportPreviewing" })).toHaveTextContent("旧报告");
    fireEvent.click(within(current).getByRole("button", { name: "报告.md", exact: true }));
    expect(screen.getByRole("region", { name: "chat.exportPreviewing" })).toHaveTextContent("报告😀");
    expect(screen.getByRole("region", { name: "chat.exportPreviewing" })).not.toHaveTextContent("旧报告");
    expect(within(previous).queryByText("chat.exportPreviewing")).not.toBeInTheDocument();
    fireEvent.click(within(previous).getByTitle("chat.artifactCollectorDownload 报告.md"));
    await waitFor(() => expect(api.download).toHaveBeenCalledWith(expect.any(Blob), "报告.md"));
    const downloaded = await new Promise((done) => {
      const reader = new FileReader(); reader.onload = () => done(reader.result);
      reader.readAsText(api.download.mock.calls[0][0]);
    });
    expect(downloaded).toBe("旧报告");
    expect(api.create).not.toHaveBeenCalled();
  });

  it("does not label ordinary artifacts when the message has no exports", () => {
    useTaskCenterStore.setState({ artifactsByConversation: { conv: [artifact as any] } });
    render(<ArtifactCollectorCard sessionId="conv" historyId="history" currentExportIds={[]} />);
    expect(screen.getByText("报告.md")).toBeVisible();
    expect(screen.queryByText(/chat.export(Current|Previous|Unknown)Generation/)).not.toBeInTheDocument();
  });
});
