import { fireEvent, render, screen, waitFor, cleanup, within, act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { SourceImportProvider, ExecutionResults, uniquePapers } from "./index";
import { ChatSourcePanel, ReferenceSourcesButton } from "./ReferenceSources";
import * as api from "./api";
import type { ChatSource } from "../../utils/sourceAdapter";

vi.mock("./api", () => ({ inspectSources: vi.fn(), lookupSourcePDFs: vi.fn(), listSourceImports: vi.fn(), sourceTargets: vi.fn(), submitSourceImport: vi.fn(), retrySourceImport: vi.fn() }));
vi.mock("@/modules/knowledge/utils/request", async original => ({ ...await original<typeof import("@/modules/knowledge/utils/request")>(), getKnowledgeOptions: vi.fn().mockResolvedValue({ scenes: [{ key: "academic_papers", name_i18n_key: "论文" }], processing_levels: ["stored", "indexed"], default_processing_level: "stored", default_algo_id: "test" }) }));
const sources: ChatSource[] = [
  { source_type: "external", citation_id: "a", title: "Paper abstract", url: "https://arxiv.org/abs/2210.03629", content: "Abstract evidence" },
  { source_type: "external", citation_id: "b", title: "Paper PDF", url: "https://arxiv.org/pdf/2210.03629v2", content: "Version two evidence" },
  { source_type: "external", citation_id: "c", title: "Website", url: "https://example.com" },
];
const inspected = sources.map((source, index) => ({ source_id: source.citation_id!, title: source.title!, url: source.url!, eligible: index < 2, paper_key: index < 2 ? "arxiv:2210.03629" : undefined }));
function view(children: React.ReactNode, rows = sources) { return render(<MemoryRouter><SourceImportProvider context={{ conversation_id: "chat", history_id: "answer" }} sources={rows}>{children}</SourceImportProvider></MemoryRouter>); }
beforeEach(() => {
  localStorage.setItem("lazymind:developer-active", "1");
  vi.clearAllMocks(); vi.mocked(api.inspectSources).mockResolvedValue(inspected); vi.mocked(api.listSourceImports).mockResolvedValue([]); vi.mocked(api.sourceTargets).mockResolvedValue([{ dataset_id: "kb", display_name: "My papers" }]);
});
afterEach(cleanup);
describe("source imports", () => {
  it("groups a confirmed paper and preserves each source when expanded", async () => {
    view(<ChatSourcePanel sources={sources} onClose={() => {}} embedded />);
    const expand = await screen.findByRole("button", { name: /展开 2 个来源/ });
    expect(screen.getByText("2 项 · 3 个来源")).toBeInTheDocument();
    expect(expand).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Paper PDF")).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    fireEvent.click(expand);
    const links = within(screen.getByRole("list", { name: "Paper abstract 的来源" }));
    for (const source of sources.slice(0, 2)) {
      expect(links.getByRole("link", { name: source.url })).toHaveAttribute("href", source.url);
      expect(links.queryByText(source.content!)).not.toBeInTheDocument();
    }
    fireEvent.click(links.getByRole("button", { name: "Paper PDF" }));
    expect(screen.getByRole("heading", { name: "Paper PDF" })).toBeInTheDocument();
    expect(screen.getByText("Version two evidence")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "返回来源列表" }));
    fireEvent.click(screen.getByRole("button", { name: /收起 2 个来源/ }));
    expect(screen.queryByText("Paper PDF")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "加入知识库" }));
    expect(await screen.findByRole("checkbox", { name: "Paper abstract" })).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: /^取\s*消$/ }));
    expect(api.submitSourceImport).not.toHaveBeenCalled();
  });
  it("does not merge matching titles with different or unconfirmed identities", async () => {
    const rows = Array.from({ length: 4 }, (_, i): ChatSource => ({ source_type: "external", citation_id: String(i), title: "Same title", url: `https://example.com/${i}` }));
    vi.mocked(api.inspectSources).mockResolvedValue(rows.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: i < 2, paper_key: i < 2 ? `doi:10.1234/${i}` : undefined })));
    view(<ChatSourcePanel sources={rows} onClose={() => {}} embedded />, rows);
    await screen.findByRole("button", { name: "加入知识库" });
    expect(screen.getAllByRole("button", { name: /Same title/ })).toHaveLength(4);
    expect(screen.queryByText(/展开 .* 个来源/)).not.toBeInTheDocument();
  });
  it("merges confirmed cross-provider identities and updates the reference count", async () => {
    const rows = [sources[0], { ...sources[1], url: "https://publisher.example/paper" }, sources[2]];
    vi.mocked(api.inspectSources).mockResolvedValue(rows.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: i < 2, paper_key: i < 2 ? "doi:10.1234/react" : undefined })));
    view(<ReferenceSourcesButton sources={rows} />, rows);
    expect(api.inspectSources).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole("button", { name: "参考来源 (3)" }));
    await screen.findByRole("button", { name: "参考来源 (2)" });
    fireEvent.click(await screen.findByRole("button", { name: /展开 2 个来源/ }));
    expect(screen.getByRole("link", { name: rows[1].url })).toHaveAttribute("href", rows[1].url);
  });
  it("keeps sources separate when inspection fails", async () => {
    vi.mocked(api.inspectSources).mockRejectedValue(new Error("unavailable"));
    view(<ChatSourcePanel sources={sources} onClose={() => {}} embedded />);
    await screen.findByText("暂时无法检查文献是否可加入，请重新打开来源");
    expect(screen.getByRole("button", { name: /Paper abstract/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Paper PDF/ })).toBeInTheDocument();
    expect(screen.queryByText(/展开 .* 个来源/)).not.toBeInTheDocument();
  });
  it("groups before pagination and clamps the page after identities arrive", async () => {
    const rows: ChatSource[] = Array.from({ length: 21 }, (_, i) => ({ source_type: "external", citation_id: String(i), title: `Source-${i}`, url: `https://example.com/${i}` }));
    let resolveInspection!: (rows: api.InspectedSource[]) => void;
    vi.mocked(api.inspectSources).mockReturnValue(new Promise(resolve => { resolveInspection = resolve; }));
    view(<ChatSourcePanel sources={rows} onClose={() => {}} embedded />, rows);
    fireEvent.click(screen.getByTitle("2"));
    expect(screen.getByRole("button", { name: /Source-20/ })).toBeInTheDocument();
    await act(async () => resolveInspection(rows.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: true, paper_key: i === 0 || i === 20 ? "arxiv:2210.03629" : `doi:10.1234/${i}` }))));
    expect(screen.queryByTitle("2")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /展开 2 个来源/ }));
    expect(screen.getByRole("link", { name: rows[20].url })).toHaveAttribute("href", rows[20].url);
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
  it("keeps draft creation and cancellation free of writes", async () => {
    view(<ChatSourcePanel sources={[sources[0]]} onClose={() => {}} embedded />);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    await screen.findByText("已有论文集");
    fireEvent.click(screen.getByRole("radio", { name: "新建论文集" }));
    fireEvent.change(screen.getByLabelText("名称"), { target: { value: "Draft_papers" } });
    fireEvent.click(screen.getByRole("button", { name: /^取\s*消$/ }));
    expect(api.submitSourceImport).not.toHaveBeenCalled();
  });
  it("keeps the source popover open after canceling the import dialog", async () => {
    view(<ReferenceSourcesButton sources={sources} />);
    fireEvent.click(screen.getByRole("button", { name: "参考来源 (3)" }));
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = (await screen.findByText("已有论文集")).closest('[role="dialog"]') as HTMLElement;
    expect(screen.getByRole("button", { name: "参考来源 (2)" })).toHaveAttribute("aria-expanded", "true");
    // rc-trigger handles outside pointer events before the modal button's click.
    fireEvent.mouseDown(within(dialog).getByRole("radio", { name: "新建论文集" }));
    fireEvent.click(within(dialog).getByRole("radio", { name: "新建论文集" }));
    expect(screen.getByRole("button", { name: "参考来源 (2)" })).toHaveAttribute("aria-expanded", "true");
    fireEvent.mouseDown(within(dialog).getByRole("button", { name: /^取\s*消$/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: /^取\s*消$/ }));
    await waitFor(() => expect(screen.queryByText("已有论文集")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "参考来源 (2)" })).toHaveAttribute("aria-expanded", "true");
    expect(api.submitSourceImport).not.toHaveBeenCalled();
  });
  it("submits the shared creation defaults and enforces the description limit", async () => {
    vi.mocked(api.submitSourceImport).mockResolvedValue({ batch_id: "created", target_dataset_id: "new-library" });
    view(<ChatSourcePanel sources={[sources[0]]} onClose={() => {}} embedded />);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    fireEvent.click(await screen.findByRole("radio", { name: "新建论文集" }));
    expect(screen.getByText("论文集", { selector: "span" })).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "使用场景" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("名称"), { target: { value: "Shared_papers" } });
    expect(screen.getByLabelText("描述")).toHaveAttribute("maxlength", "300");
    fireEvent.click(screen.getByRole("button", { name: "加入 1 篇" }));
    await waitFor(() => expect(api.submitSourceImport).toHaveBeenCalledWith(expect.objectContaining({
      new_knowledge_base: { name: "Shared_papers", description: "", tags: ["论文"], scene: "academic_papers", processing_level: "stored", algo_id: "test" },
    })));
  });
  it("does not offer an import action for a normal webpage", async () => {
    view(<ChatSourcePanel sources={[sources[2]]} onClose={() => {}} embedded />);
    await waitFor(() => expect(api.inspectSources).toHaveBeenCalled());
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "加入知识库" })).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: /Website/ }));
    await waitFor(() => expect(api.inspectSources).toHaveBeenCalled());
    expect(screen.getByRole("button", { name: "打开原文" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "加入知识库" })).not.toBeInTheDocument();
  });
  it("shows only agent tool receipts in the execution result panel", async () => {
    vi.mocked(api.listSourceImports).mockResolvedValue([{ item_id: "manual", batch_id: "batch", paper_key: "manual", title: "Manual import", target_dataset_id: "manual-kb", target_name: "Manual library", created_library: true, status: "added", stage: "done", retryable: false }]);
    view(<ExecutionResults operations={[{ call_id: "a", action: "knowledge.create", status: "succeeded", items: [{ name: "Library", resource_ref: { kind: "knowledge_base", knowledge_base_id: "kb" }, resource_status: "created" }] }]} />);
    expect(api.listSourceImports).not.toHaveBeenCalled();
    expect(screen.getAllByRole("button", { name: "Agent 执行结果" })).toHaveLength(1);
    expect(screen.queryByText("Library")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Agent 执行结果" }));
    const panel = within(screen.getByRole("dialog", { name: "Agent 执行结果" }));
    expect(panel.getByText("Library")).toBeInTheDocument();
    expect(panel.queryByText("Manual import")).not.toBeInTheDocument();
    expect(panel.queryByText("Manual library")).not.toBeInTheDocument();
  });
  it("deduplicates versions without accepting unqualified PDFs", () => { expect(uniquePapers(inspected)).toHaveLength(1); });
  it("adds PDF lookup results and preserves deselection across pages before submitting", async () => {
    const many: ChatSource[] = Array.from({ length: 6 }, (_, i) => ({ source_type: "external", citation_id: String(i), title: `Paper-${i}`, url: `https://doi.org/10.1234/${i}` }));
    const rows = many.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: true, paper_key: row.citation_id!, needs_pdf_lookup: i >= 4 }));
    vi.mocked(api.inspectSources).mockResolvedValue(rows);
    vi.mocked(api.lookupSourcePDFs).mockResolvedValue([{ ...rows[4], needs_pdf_lookup: false }, rows[5]]);
    vi.mocked(api.submitSourceImport).mockResolvedValue({ batch_id: "batch", target_dataset_id: "kb" });
    view(<ChatSourcePanel sources={many} onClose={() => {}} embedded />, many);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = within(screen.getByRole("dialog", { name: "加入知识库" }));
    fireEvent.click(dialog.getByRole("checkbox", { name: "Paper-0" }));
    fireEvent.click(dialog.getByRole("button", { name: "查找更多 PDF" }));
    expect(await dialog.findByText(/累计补充 1 篇全文。.*1 篇未找到可用全文。/)).toBeInTheDocument();
    expect(api.lookupSourcePDFs).toHaveBeenCalledWith({ conversation_id: "chat", history_id: "answer" }, ["4", "5"], expect.any(AbortSignal));
    expect(dialog.getByRole("checkbox", { name: "Paper-4" })).toBeChecked();
    expect(dialog.getByRole("checkbox", { name: "Paper-0" })).not.toBeChecked();
    fireEvent.click(dialog.getByTitle("2"));
    expect(dialog.getByRole("checkbox", { name: "Paper-3" })).toBeChecked();
    fireEvent.click(dialog.getByRole("checkbox", { name: "Paper-3" }));
    fireEvent.click(dialog.getByTitle("1"));
    expect(dialog.getByRole("checkbox", { name: "Paper-0" })).not.toBeChecked();
    fireEvent.click(dialog.getByRole("radio", { name: "新建论文集" }));
    fireEvent.change(dialog.getByLabelText("名称"), { target: { value: "Shared_papers" } });
    fireEvent.click(dialog.getByRole("button", { name: "加入 3 篇" }));
    await waitFor(() => expect(api.submitSourceImport).toHaveBeenCalledWith(expect.objectContaining({ source_ids: ["4", "1", "2"] })));
  });
  it("retries only failed items and restores persisted results", async () => {
    vi.mocked(api.listSourceImports).mockResolvedValue([
      { item_id: "good", batch_id: "batch", paper_key: "good", title: "Added", target_dataset_id: "kb", target_name: "Papers", created_library: false, status: "added", stage: "done", retryable: false },
      { item_id: "bad", batch_id: "batch", paper_key: "bad", title: "Failed", target_dataset_id: "kb", target_name: "Papers", created_library: false, status: "failed", stage: "download", retryable: true, error_message: "HTTP 404" },
    ]);
    vi.mocked(api.retrySourceImport).mockResolvedValue();
    view(<><ExecutionResults /><ChatSourcePanel sources={sources} onClose={() => {}} /></>);
    fireEvent.click(await screen.findByRole("button", { name: "入库记录" }));
    expect(screen.queryByRole("button", { name: "Agent 执行结果" })).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "文献入库记录" })).toBeInTheDocument();
    expect(screen.getByText("HTTP 404")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "仅重试失败项" }));
    await waitFor(() => expect(api.retrySourceImport).toHaveBeenCalledWith("batch", ["bad"], expect.any(String)));
  });

  it("looks up bounded batches and separates failed queries from papers without full text", async () => {
    const many: ChatSource[] = Array.from({ length: 6 }, (_, i) => ({ source_type: "external", citation_id: String(i), title: `Lookup-${i}`, url: `https://doi.org/10.1234/${i}` }));
    const rows = many.map(row => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: true, paper_key: row.citation_id!, needs_pdf_lookup: true }));
    vi.mocked(api.inspectSources).mockResolvedValue(rows);
    vi.mocked(api.lookupSourcePDFs).mockImplementation(async (_context, ids) => ids.map(id => ({
      ...rows[Number(id)], needs_pdf_lookup: Number(id) >= 4, pdf_lookup_failed: id === "5",
    })));
    view(<ChatSourcePanel sources={many} onClose={() => {}} embedded />, many);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = within(await screen.findByRole("dialog", { name: "加入知识库" }));
    fireEvent.click(dialog.getByRole("button", { name: "查找更多 PDF" }));
    expect(await dialog.findByText(/累计补充 4 篇全文。.*1 篇未找到可用全文。.*1 篇查询失败/)).toBeInTheDocument();
    expect(vi.mocked(api.lookupSourcePDFs).mock.calls.map(call => call[1])).toEqual([["0", "1", "2", "3"], ["4", "5"]]);
    expect(dialog.getByRole("checkbox", { name: "Lookup-0" })).toBeChecked();
    fireEvent.click(dialog.getByRole("button", { name: "稍后重试失败项" }));
    await waitFor(() => expect(api.lookupSourcePDFs).toHaveBeenLastCalledWith(expect.anything(), ["5"], expect.any(AbortSignal)));
  });

});


it("does not fetch during history mounting and shares the first panel load", async () => {
  view(<><ReferenceSourcesButton sources={sources} /><span>Historical answer</span></>);
  expect(api.inspectSources).not.toHaveBeenCalled();
  expect(api.listSourceImports).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "参考来源 (3)" }));
  await waitFor(() => expect(api.inspectSources).toHaveBeenCalledTimes(1));
  expect(api.listSourceImports).toHaveBeenCalledTimes(1);
});

it("shares concurrent loads for the same owner and source context", async () => {
  render(<MemoryRouter>
    <SourceImportProvider context={{ conversation_id: "chat", history_id: "answer" }} sources={sources}><ChatSourcePanel sources={sources} embedded onClose={() => {}} /></SourceImportProvider>
    <SourceImportProvider context={{ history_id: "answer", conversation_id: "chat" }} sources={sources}><ChatSourcePanel sources={sources} embedded onClose={() => {}} /></SourceImportProvider>
  </MemoryRouter>);
  await waitFor(() => expect(api.inspectSources).toHaveBeenCalledTimes(1));
  expect(api.listSourceImports).toHaveBeenCalledTimes(1);
});
