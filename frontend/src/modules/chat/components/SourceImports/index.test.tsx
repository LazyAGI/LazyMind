import { fireEvent, render, screen, waitFor, cleanup, within, act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import MarkdownViewer from "../MarkdownViewer";
import { SourceImportProvider, uniquePapers } from "./index";
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
  it("opens the currently selected citation and offers single import from a grouped inline card", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    try {
      const { container } = view(<MarkdownViewer sources={sources}>{"Compare [1](#source-a) and [2](#source-b)."}</MarkdownViewer>);
      fireEvent.focus(container.querySelector(".md-source-chip")!);
      fireEvent.click(await screen.findByRole("button", { name: "Next source" }));
      fireEvent.click(screen.getByRole("button", { name: "打开原文" }));
      expect(open).toHaveBeenCalledWith(sources[1].url, "_blank", "noopener,noreferrer");
      expect(await screen.findByRole("button", { name: "加入知识库" })).toBeEnabled();
    } finally {
      open.mockRestore();
    }
  });
  it("excludes a saved PDF from mixed selections in the chosen collection", async () => {
    const rows = [inspected[0], { ...inspected[1], paper_key: "doi:10.1234/new" }];
    vi.mocked(api.inspectSources).mockImplementation(async (_context, target) =>
      target?.datasetID === "kb" ? [{ ...rows[0], target_presence: "already_present" }, rows[1]] : rows);
    vi.mocked(api.submitSourceImport).mockResolvedValue({ batch_id: "batch", target_dataset_id: "kb" });
    view(<ChatSourcePanel sources={sources.slice(0, 2)} onClose={() => {}} embedded />);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = within(await screen.findByRole("dialog", { name: "加入知识库" }));
    fireEvent.mouseDown(dialog.getByRole("combobox", { name: "选择论文集" }));
    fireEvent.click(await screen.findByText("My papers"));
    await dialog.findByText("已在此论文集");
    expect(dialog.getByRole("checkbox", { name: "Paper abstract" })).toBeDisabled();
    expect(dialog.getByRole("checkbox", { name: "Paper abstract" })).not.toBeChecked();
    expect(dialog.getByRole("checkbox", { name: "Paper PDF" })).toBeChecked();
    fireEvent.click(dialog.getByRole("button", { name: "加入 1 篇" }));
    await waitFor(() => expect(api.submitSourceImport).toHaveBeenCalledWith(expect.objectContaining({
      target_dataset_id: "kb", source_ids: ["b"],
    })));
  });
  it.each(["recheck", "submit"] as const)("lets the reader recover when the selected collection disappears during %s", async path => {
    const rows = [inspected[0], { ...inspected[1], paper_key: "doi:10.1234/new" }];
    const notFound = { isAxiosError: true, response: { status: 404, data: { code: "TARGET_NOT_FOUND" } } };
    let deleted = false;
    vi.mocked(api.sourceTargets).mockResolvedValue([
      { dataset_id: "kb", display_name: "My papers" },
      { dataset_id: "other", display_name: "Other papers" },
    ]);
    vi.mocked(api.inspectSources).mockImplementation(async (_context, target) => {
      if (path === "recheck" && deleted && target?.datasetID === "kb") throw notFound;
      return rows;
    });
    vi.mocked(api.submitSourceImport).mockImplementation(async request => {
      if (path === "submit" && request.target_dataset_id === "kb") throw notFound;
      return { batch_id: "batch", target_dataset_id: "other" };
    });
    view(<ChatSourcePanel sources={sources.slice(0, 2)} onClose={() => {}} embedded />);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = within(await screen.findByRole("dialog", { name: "加入知识库" }));
    const target = dialog.getByRole("combobox", { name: "选择论文集" });
    fireEvent.click(dialog.getByRole("checkbox", { name: "Paper abstract" }));
    fireEvent.mouseDown(target);
    fireEvent.click(await screen.findByText("My papers"));
    const add = dialog.getByRole("button", { name: "加入 1 篇" });
    await waitFor(() => expect(add).toBeEnabled());

    deleted = true;
    if (path === "recheck") fireEvent(document, new Event("visibilitychange"));
    else fireEvent.click(add);
    await dialog.findByText("「My papers」不存在，请重新选择或新建知识库。");
    expect(target).toHaveAttribute("aria-invalid", "true");
    expect(add).toBeDisabled();
    expect(dialog.getByRole("checkbox", { name: "Paper abstract" })).not.toBeChecked();
    expect(dialog.getByRole("checkbox", { name: "Paper PDF" })).toBeChecked();

    fireEvent.mouseDown(target);
    expect(screen.queryByText("My papers", { exact: true })).not.toBeInTheDocument();
    fireEvent.click(await screen.findByText("Other papers"));
    await waitFor(() => {
      expect(add).toBeEnabled();
      expect(target).not.toHaveAttribute("aria-invalid", "true");
    });
    fireEvent.click(add);
    await waitFor(() => expect(api.submitSourceImport).toHaveBeenLastCalledWith(expect.objectContaining({
      target_dataset_id: "other", source_ids: ["b"],
    })));
  });
  it("keeps the chosen collection and can retry a temporary recheck failure", async () => {
    let unavailable = false;
    vi.mocked(api.inspectSources).mockImplementation(async (_context, target) => {
      if (target && unavailable) throw new Error("Network Error");
      return inspected;
    });
    view(<ChatSourcePanel sources={sources} onClose={() => {}} embedded />);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = within(await screen.findByRole("dialog", { name: "加入知识库" }));
    fireEvent.mouseDown(dialog.getByRole("combobox", { name: "选择论文集" }));
    fireEvent.click(await screen.findByText("My papers"));
    const add = dialog.getByRole("button", { name: "加入 1 篇" });
    await waitFor(() => expect(add).toBeEnabled());

    unavailable = true;
    fireEvent(window, new Event("focus"));
    await dialog.findByText("暂时无法检查所选论文集。");
    expect(dialog.getByText("My papers", { exact: true })).toBeInTheDocument();
    expect(add).toBeDisabled();
    unavailable = false;
    fireEvent.click(dialog.getByRole("button", { name: "重试查询" }));
    await waitFor(() => expect(add).toBeEnabled());
    expect(dialog.getByText("My papers", { exact: true })).toBeInTheDocument();
  });
  it("groups a confirmed paper and lets the reader open each source version", async () => {
    view(<ChatSourcePanel sources={sources} onClose={() => {}} embedded />);
    const versions = await screen.findByRole("button", { name: /2 个来源/ });
    expect(screen.getByText("2 项 · 3 个来源")).toBeInTheDocument();
    expect(screen.queryByText("Paper PDF")).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    fireEvent.click(versions);
    expect(screen.getByRole("heading", { name: "来源版本" })).toBeInTheDocument();
    const entries = within(screen.getByRole("list", { name: "Paper abstract 的来源" }));
    for (const source of sources.slice(0, 2)) {
      expect(entries.getByText(source.url!)).toBeInTheDocument();
      expect(entries.queryByText(source.content!)).not.toBeInTheDocument();
    }
    fireEvent.click(entries.getByRole("button", { name: /Paper PDF/ }));
    expect(screen.getByRole("heading", { name: "Paper PDF" })).toBeInTheDocument();
    expect(screen.getByText("Version two evidence")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "返回来源版本" }));
    fireEvent.click(screen.getByRole("button", { name: "返回来源列表" }));
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
    expect(screen.queryByText(/^\d+ 个来源$/)).not.toBeInTheDocument();
  });
  it("merges confirmed cross-provider identities and updates the reference count", async () => {
    const rows = [sources[0], { ...sources[1], url: "https://publisher.example/paper" }, sources[2]];
    vi.mocked(api.inspectSources).mockResolvedValue(rows.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: i < 2, paper_key: i < 2 ? "doi:10.1234/react" : undefined })));
    view(<ReferenceSourcesButton sources={rows} />, rows);
    expect(api.inspectSources).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole("button", { name: "参考来源 (3)" }));
    await screen.findByRole("button", { name: "参考来源 (2)" });
    fireEvent.click(await screen.findByRole("button", { name: /2 个来源/ }));
    expect(screen.getByText(rows[1].url)).toBeInTheDocument();
  });
  it("keeps sources separate when inspection fails", async () => {
    vi.mocked(api.inspectSources).mockRejectedValue(new Error("unavailable"));
    view(<ChatSourcePanel sources={sources} onClose={() => {}} embedded />);
    await screen.findByText("暂时无法检查文献是否可加入，请重新打开来源");
    expect(screen.getByRole("button", { name: /Paper abstract/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Paper PDF/ })).toBeInTheDocument();
    expect(screen.queryByText(/^\d+ 个来源$/)).not.toBeInTheDocument();
  });
  it("groups before paging four papers at a time and restores the page after viewing versions", async () => {
    const rows: ChatSource[] = Array.from({ length: 9 }, (_, i) => ({ source_type: "external", citation_id: String(i), title: `Source-${i}`, url: `https://example.com/${i}` }));
    let resolveInspection!: (rows: api.InspectedSource[]) => void;
    vi.mocked(api.inspectSources).mockReturnValue(new Promise(resolve => { resolveInspection = resolve; }));
    view(<ChatSourcePanel sources={rows} onClose={() => {}} embedded />, rows);
    expect(screen.getAllByRole("button", { name: /Source-\d/ })).toHaveLength(4);
    fireEvent.click(screen.getByTitle("3"));
    expect(screen.getAllByRole("button", { name: /Source-\d/ })).toHaveLength(1);
    expect(screen.getByRole("button", { name: /Source-8/ })).toBeInTheDocument();
    await act(async () => resolveInspection(rows.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: true, paper_key: i >= 7 ? "arxiv:2210.03629" : `doi:10.1234/${i}` }))));
    expect(screen.queryByTitle("3")).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /Source-\d/ })).toHaveLength(4);
    fireEvent.click(screen.getByRole("button", { name: /2 个来源.*Source-7/ }));
    const versions = within(screen.getByRole("list", { name: "Source-7 的来源" }));
    expect(versions.getAllByRole("button")).toHaveLength(2);
    fireEvent.click(screen.getByRole("button", { name: "返回来源列表" }));
    expect(screen.getByRole("button", { name: /Source-7/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Source-0/ })).not.toBeInTheDocument();
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
  it("shows when a cited source has no original link", async () => {
    const rows: ChatSource[] = [{ source_type: "external", title: "Paper without URL", index: "10.1" }];
    view(<ChatSourcePanel sources={rows} onClose={() => {}} embedded />, rows);
    fireEvent.click(await screen.findByRole("button", { name: /Paper without URL/ }));
    expect(screen.getByRole("button", { name: "暂无原文链接" })).toBeDisabled();
  });
  it("deduplicates versions without accepting unqualified PDFs", () => { expect(uniquePapers(inspected)).toHaveLength(1); });
  it("returns to the first page with PDF lookup results first and preserves deselection before submitting", async () => {
    const many: ChatSource[] = Array.from({ length: 7 }, (_, i) => ({ source_type: "external", citation_id: String(i), title: `Paper-${i}`, url: `https://doi.org/10.1234/${i}` }));
    const rows = many.map((row, i) => ({ source_id: row.citation_id!, title: row.title!, url: row.url!, eligible: true, paper_key: row.citation_id!, needs_pdf_lookup: i >= 5 }));
    vi.mocked(api.inspectSources).mockResolvedValue(rows);
    let resolveLookup!: (results: api.InspectedSource[]) => void;
    vi.mocked(api.lookupSourcePDFs).mockReturnValue(new Promise(resolve => { resolveLookup = resolve; }));
    vi.mocked(api.submitSourceImport).mockResolvedValue({ batch_id: "batch", target_dataset_id: "kb" });
    view(<ChatSourcePanel sources={many} onClose={() => {}} embedded />, many);
    fireEvent.click(await screen.findByRole("button", { name: "加入知识库" }));
    const dialog = within(screen.getByRole("dialog", { name: "加入知识库" }));
    expect(dialog.getByText("可入库论文 5 篇")).toBeInTheDocument();
    fireEvent.click(dialog.getByRole("checkbox", { name: "Paper-0" }));
    fireEvent.click(dialog.getByTitle("2"));
    const lookup = dialog.getByRole("button", { name: "补查 PDF" });
    fireEvent.click(lookup);
    expect(lookup).toHaveTextContent("补查 PDF");
    expect(dialog.getByRole("checkbox", { name: "Paper-4" })).toBeChecked();
    await act(async () => resolveLookup([{ ...rows[5], needs_pdf_lookup: false }, rows[6]]));
    expect(await dialog.findByText(/本次找到 1 篇全文。.*1 篇未找到可用全文。/)).toBeInTheDocument();
    expect(api.lookupSourcePDFs).toHaveBeenCalledWith({ conversation_id: "chat", history_id: "answer" }, ["5", "6"], expect.any(AbortSignal));
    expect(dialog.getByText("可入库论文 6 篇")).toBeInTheDocument();
    expect(dialog.getByRole("button", { name: "补查 PDF" })).toBeDisabled();
    const found = dialog.getByRole("checkbox", { name: "Paper-5" });
    expect(dialog.getAllByRole("checkbox")[0]).toBe(found);
    expect(found).toBeChecked();
    expect(within(found.closest("li")!).getByText("补查")).toBeInTheDocument();
    expect(dialog.getByRole("checkbox", { name: "Paper-0" })).not.toBeChecked();
    fireEvent.click(dialog.getByTitle("2"));
    expect(dialog.getByRole("checkbox", { name: "Paper-3" })).toBeChecked();
    fireEvent.click(dialog.getByRole("checkbox", { name: "Paper-3" }));
    fireEvent.click(dialog.getByTitle("1"));
    expect(dialog.getByRole("checkbox", { name: "Paper-0" })).not.toBeChecked();
    fireEvent.click(dialog.getByRole("radio", { name: "新建论文集" }));
    fireEvent.change(dialog.getByLabelText("名称"), { target: { value: "Shared_papers" } });
    fireEvent.click(dialog.getByRole("button", { name: "加入 4 篇" }));
    await waitFor(() => expect(api.submitSourceImport).toHaveBeenCalledWith(expect.objectContaining({ source_ids: ["5", "1", "2", "4"] })));
  });
  it("retries only failed items and restores persisted results", async () => {
    vi.mocked(api.listSourceImports).mockResolvedValue([
      { item_id: "good", batch_id: "batch", paper_key: "good", title: "Added", target_dataset_id: "kb", target_name: "Papers", created_library: false, status: "added", stage: "done", retryable: false },
      { item_id: "bad", batch_id: "batch", paper_key: "bad", title: "Failed", target_dataset_id: "kb", target_name: "Papers", created_library: false, status: "failed", stage: "download", retryable: true, error_message: "HTTP 404" },
    ]);
    vi.mocked(api.retrySourceImport).mockResolvedValue();
    view(<ChatSourcePanel sources={sources} onClose={() => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "入库记录" }));
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
    fireEvent.click(dialog.getByRole("button", { name: "补查 PDF" }));
    expect(await dialog.findByText(/本次找到 4 篇全文。.*1 篇未找到可用全文。.*1 篇查询失败/)).toBeInTheDocument();
    expect(vi.mocked(api.lookupSourcePDFs).mock.calls.map(call => call[1])).toEqual([["0", "1", "2", "3"], ["4", "5"]]);
    expect(dialog.getByRole("checkbox", { name: "Lookup-0" })).toBeChecked();
    fireEvent.click(dialog.getByRole("button", { name: "补查 PDF" }));
    await waitFor(() => expect(api.lookupSourcePDFs).toHaveBeenLastCalledWith(expect.anything(), ["5"], expect.any(AbortSignal)));
    expect(await dialog.findByText(/本次找到 0 篇全文。.*1 篇查询失败/)).toBeInTheDocument();
    expect(dialog.queryByText(/未找到可用全文/)).not.toBeInTheDocument();
    expect(dialog.getByRole("checkbox", { name: "Lookup-0" })).toBeChecked();
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
