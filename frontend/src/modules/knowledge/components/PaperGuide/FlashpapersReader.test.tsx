import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import PaperGuideReader from "./PaperGuideReader";
import PaperGuidePlayground, { playgroundDocument } from "./PaperGuidePlayground";
import { revision, section } from "./fixtures";
import GuideGlossaryEntry from "./GuideGlossaryEntry";
import { sourceBoundary } from "./sourceText";
import { isGuideRunActive } from "./guideRunState";

it("publishes arbitrary chapters in plan order with pending placeholders without source action buttons", () => {
  const r = revision();
  r.plan = { layout: "flashpapers", title: "Understanding low rank", subtitle: "A matrix you can change", claims: [], questions: [], required_assets: [], term_policy: [], profiles: { primary: null, secondary: [], uncertainty_reason: "" }, sections: ["overview", "rank-lab", "results"].map(role => ({ role, title: role, focus: "", terms: [], asset_ids: [], claim_ids: [], evidence_ids: [] })) };
  r.sections = [section("results"), section("overview")];
  r.sections[1].blocks[0].evidence_ids = ["e1", "e2"];
  r.run.stage = "writing";
  const open = vi.fn();
  const view = render(<PaperGuideReader revision={r} title="Original" language="en" onEvidence={open} onSelection={() => {}} />);
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Understanding low rank");
  expect(screen.getAllByRole("heading", { level: 2 }).map(el => el.textContent)).toEqual(["Paper Summary", "rank-lab", "Experimental Results"]);
  expect(screen.getByText(/This chapter updates automatically/)).toBeVisible();
  expect(screen.getByRole("button", { name: "rank-lab Queued" })).toBeInTheDocument();
  expect(view.container.querySelector(".guide-skeleton")).not.toBeNull();
  const pending = { ...section("rank-lab"), key: "section:rank-lab:v1", blocks: [], publication_status: "generating" };
  r.sections.push(pending);
  view.rerender(<PaperGuideReader revision={{ ...r }} title="Original" language="en" onEvidence={open} onSelection={() => {}} />);
  fireEvent.click(screen.getByRole("button", { name: "rank-lab Generating" }));
  expect(screen.getByRole("heading", { name: "rank-lab" })).toHaveFocus();
  expect(screen.queryByText("Source 1")).toBeNull();
  expect(screen.queryByText("Sources (2)")).toBeNull();
  expect(screen.queryByLabelText("Passage actions")).toBeNull();
  expect(screen.queryByRole("button", { name: "View source" })).toBeNull();
  expect(open).not.toHaveBeenCalled();
  r.sections = [section("results"), section("rank-lab"), section("overview")];
  view.rerender(<PaperGuideReader revision={{ ...r }} title="Original" language="en" onEvidence={open} onSelection={() => {}} />);
  expect(screen.queryByText(/This chapter updates automatically/)).toBeNull();
  expect(screen.getAllByRole("heading", { level: 2 }).map(el => el.textContent)).toEqual(["Paper Summary", "rank-lab", "Experimental Results"]);
});

it("renders the five planned layers inside the pending overview", () => {
  const r = revision();
  r.plan = { ...r.plan!, layout: "flashpapers", paper_flow: {
    field: "生成建模", problems: ["问题甲", "问题乙"], method: "扩散模型",
    modules: ["前向扩散", "反向去噪"], datasets: ["CIFAR-10"],
  }, sections: [{ role: "overview", title: "论文简述", focus: "", terms: [], asset_ids: [], claim_ids: [], evidence_ids: [] }] };
  r.sections = [];
  r.run.stage = "writing";
  const props = { title: "DDPM", language: "zh", onEvidence: vi.fn(), onSelection: vi.fn() };
  const view = render(<PaperGuideReader revision={r} {...props} />);
  const graph = screen.getByRole("figure", { name: "论文脉络图" });
  expect(within(graph).getAllByRole("term").map(node => node.textContent)).toEqual(["领域", "存在问题", "提出方法", "主要模块", "实验验证"]);
  expect(within(graph).getAllByRole("list").map(row => row.children.length)).toEqual([1, 2, 1, 2, 1]);
  expect(graph.querySelectorAll("svg")).toHaveLength(4);

  const overview = section("overview");
  const paragraph = overview.blocks[0];
  overview.blocks = [paragraph, { ...paragraph, id: "summary", kind: "callout", text: "摘要" }];
  view.rerender(<PaperGuideReader revision={{ ...r, sections: [overview] }} {...props} />);
  const readyGraph = screen.getByRole("figure", { name: "论文脉络图" });
  expect(readyGraph.previousElementSibling).toHaveAttribute("data-guide-block", "summary");
  expect(readyGraph.nextElementSibling).toHaveAttribute("data-guide-block", paragraph.id);
});

it("reads a legacy overview and renders a dataset-free English flow when present", () => {
  const r = revision();
  r.plan = { layout: "flashpapers", sections: [], claims: [], questions: [], required_assets: [], term_policy: [], profiles: { primary: null, secondary: [], uncertainty_reason: "" } };
  r.sections = [section("overview")];
  const props = { revision: r, title: "Paper", language: "en", onEvidence: vi.fn(), onSelection: vi.fn() };
  const view = render(<PaperGuideReader {...props} />);
  expect(screen.getByRole("heading", { name: "Paper Summary" })).toBeVisible();
  expect(screen.queryByRole("figure", { name: "Paper at a glance" })).toBeNull();
  view.rerender(<PaperGuideReader {...props} revision={{ ...r, plan: { ...r.plan, paper_flow: {
    field: "Theory", problems: ["Bound estimation"], method: "Analytical bound", modules: ["Proof"], datasets: [],
  } } }} />);
  const graph = screen.getByRole("figure", { name: "Paper at a glance" });
  expect(within(graph).getAllByRole("term").map(node => node.textContent)).toEqual(["Field", "Problems", "Proposed method", "Main components", "Evaluation"]);
  expect(within(graph).getByText("No dataset reported")).toBeVisible();
});

it("uses the same bookmark in the outline and heading for each optional deep dive", () => {
  const r = revision();
  const roles = ["overview", "attention-method", "results", "attention-analysis", "rank-budget", "limitations", "glossary"];
  r.plan = { ...r.plan!, layout: "flashpapers", sections: roles.map(role => ({
    role, title: role, focus: "", terms: [], asset_ids: [], claim_ids: [], evidence_ids: [],
  })) };
  r.sections = [section("attention-analysis")];
  r.run.stage = "writing";
  render(<PaperGuideReader revision={r} title="Paper" language="en" onEvidence={vi.fn()} onSelection={vi.fn()} />);
  for (const [name, icon] of [["attention-method", "attention"], ["attention-analysis", "bookmark"], ["rank-budget", "bookmark"], ["Limitations", "boundary"]]) {
    const heading = screen.getByRole("heading", { name });
    const entry = screen.getByRole("button", { name: new RegExp(name) });
    for (const element of [entry, heading]) expect(element.querySelector("[data-icon]")).toHaveAttribute("data-icon", icon);
  }
});

it("puts CSP before generated code and removes navigation and nested documents", () => {
  const html = playgroundDocument('<html><head><meta http-equiv="refresh" content="0;url=https://bad.test"><base href="https://bad.test"><script>window.example=1</script></head><body><iframe src="https://bad.test"></iframe><a href="https://bad.test">link</a><svg><circle r="4"/></svg></body></html>');
  const doc = new DOMParser().parseFromString(html, "text/html");
  expect(doc.head.firstElementChild?.getAttribute("http-equiv")).toBe("Content-Security-Policy");
  expect(doc.head.firstElementChild?.getAttribute("content")).toContain("default-src 'none'");
  expect(html).not.toContain("unsafe-eval");
  expect(doc.querySelector("iframe,base,meta[http-equiv=refresh],a[href]")).toBeNull();
  expect(doc.querySelector("circle")).not.toBeNull();
  expect(html).toContain("--guide-accent");
});

it("prepares full HTML without parsing generated markup in the host browser", () => {
  const hostParser = vi.spyOn(DOMParser.prototype, "parseFromString");
  let html: string;
  try {
    html = playgroundDocument('<html lang="zh"><head><title>排序</title></head><body class="demo"><template><a href="/reference">参考</a></template><svg viewBox="0 0 24 24"></svg></body></html>');
    expect(hostParser).not.toHaveBeenCalled();
  } finally {
    hostParser.mockRestore();
  }
  const doc = new DOMParser().parseFromString(html, "text/html");
  expect(doc.documentElement.lang).toBe("zh");
  expect(doc.title).toBe("排序");
  expect(doc.body.className).toBe("demo");
  expect(doc.querySelector("svg")?.getAttribute("viewBox")).toBe("0 0 24 24");
  expect(doc.querySelector("template")?.content.querySelector("a")?.getAttribute("href")).toBeNull();
});

it("isolates scripts and accepts only bounded height/error messages from its own frame", () => {
  const view = render(<PaperGuidePlayground html="<button>Change rank</button>" title="Rank experiment" language="en" />);
  const frame = screen.getByTitle("Rank experiment") as HTMLIFrameElement;
  expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
  expect(view.container.querySelector("button")?.textContent).toBe("Reset");
  act(() => window.dispatchEvent(new MessageEvent("message", { source: window, data: { type: "guide-playground-height", height: 600 } })));
  expect(frame.style.height).toBe("420px");
  act(() => window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow!, data: { type: "guide-playground-height", height: 1e8 } })));
  expect(frame.style.height).toBe("760px");
  act(() => window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow!, data: { type: "guide-playground-height", height: NaN } })));
  expect(frame.style.height).toBe("760px");
  act(() => window.dispatchEvent(new MessageEvent("message", { source: window, data: { type: "guide-playground-error" } })));
  expect(screen.queryByRole("status")).toBeNull();
  act(() => window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow!, data: { type: "guide-playground-error" } })));
  expect(screen.getByRole("status")).toHaveTextContent("could not run");
  fireEvent.click(screen.getByText("Reset"));
  expect(screen.queryByRole("status")).toBeNull();
  expect(screen.getByTitle("Rank experiment")).not.toBe(frame);
});

it("offers expansion only for clipped content and preserves the running interaction", () => {
  render(<PaperGuidePlayground html="<p>Scene</p>" title="Scene" language="en" />);
  const frame = screen.getByTitle("Scene");
  const resize = (height: number) => act(() => window.dispatchEvent(new MessageEvent("message", {
    source: (frame as HTMLIFrameElement).contentWindow!, data: { type: "guide-playground-height", height },
  })));
  resize(180);
  expect(frame.style.height).toBe("180px");
  expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();
  resize(1040);
  expect(frame.style.height).toBe("760px");
  fireEvent.click(screen.getByRole("button", { name: "Expand" }));
  expect(screen.getByTitle("Scene")).toBe(frame);
  expect(frame.style.height).toBe("1040px");
  fireEvent.click(screen.getByRole("button", { name: "Collapse" }));
  expect(frame.style.height).toBe("760px");
  resize(300);
  expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();
});

it.each(["reading", "planning", "writing", "checking"])("keeps %s runs polling", stage => expect(isGuideRunActive(stage)).toBe(true));

it("renders custom scenes only in the Flashpapers reader", () => {
  const r = revision();
  r.sections[0].blocks[0].kind = "playground";
  r.sections[0].blocks[0].playground = { html: "<svg><circle r='12'/></svg><script>let rank=2</script>" };
  const props = { revision: r, title: "Paper", language: "en", onEvidence: vi.fn(), onSelection: vi.fn() };
  const view = render(<PaperGuideReader {...props} />);
  expect(view.container.querySelector("iframe")).toBeNull();
  r.plan = { layout: "flashpapers", sections: [], claims: [], questions: [], required_assets: [], term_policy: [], profiles: { primary: null, secondary: [], uncertainty_reason: "" } };
  view.rerender(<PaperGuideReader {...props} revision={{ ...r }} />);
  expect(view.container.querySelector("iframe")?.srcdoc).toContain("let rank=2");
  expect(view.container.querySelector("circle")).toBeNull();
});

it("keeps a single-level icon outline while retaining subheadings in the article", () => {
  const r = revision();
  r.plan = { layout: "flashpapers", title: "A paper", subtitle: "", claims: [], questions: [], required_assets: [], term_policy: [], profiles: { primary: null, secondary: [], uncertainty_reason: "" }, sections: ["overview", "results"].map(role => ({ role, title: role, focus: "", terms: [], asset_ids: [], claim_ids: [], evidence_ids: [] })) } as typeof r.plan;
  r.sections = [section("overview"), section("results")];
  r.sections[0].blocks.push({ ...r.sections[0].blocks[0], id: "detail-title", kind: "heading", text: "Details stay in the article" });
  const view = render(<PaperGuideReader revision={r} title="Paper" language="en" onEvidence={() => {}} onSelection={() => {}} />);
  const nav = screen.getByRole("navigation", { name: "Article outline" });
  expect(within(nav).getAllByRole("button")).toHaveLength(2);
  expect(within(nav).queryByText("Details stay in the article")).toBeNull();
  expect(screen.getByRole("heading", { name: "Details stay in the article" })).toBeVisible();
  expect(nav.querySelectorAll(".guide-section-icon[aria-hidden=true]")).toHaveLength(2);
  expect(within(nav).getByRole("button", { name: "Paper Summary Ready" })).toHaveAttribute("aria-current", "location");
  fireEvent.click(within(nav).getByRole("button", { name: "Experimental Results Ready" }));
  expect(within(nav).getByRole("button", { name: "Experimental Results Ready" })).toHaveAttribute("aria-current", "location");
  expect(view.container.querySelectorAll(".paper-guide-outline-subsection")).toHaveLength(0);
});

it("renders existing glossary terms as collapsed entries with source offsets and math", () => {
  const r = revision();
  r.plan = { layout: "flashpapers", sections: [] } as unknown as typeof r.plan;
  const s = section("glossary");
  s.blocks = [
    { ...s.blocks[0], id: "term", kind: "paragraph", text: "• Noise schedule 噪声调度：每一步的噪声方差为 $\\beta_t$。" },
    { ...s.blocks[0], id: "plain", kind: "paragraph", text: "A background paragraph without a term delimiter." },
  ];
  r.sections = [s];
  const view = render(<PaperGuideReader revision={r} title="Paper" language="zh" onEvidence={() => {}} onSelection={() => {}} />);
  const summary = screen.getByText("Noise schedule 噪声调度").closest("summary")!;
  const details = summary.closest("details")!;
  expect(details).not.toHaveAttribute("open");
  fireEvent.click(summary);
  expect(details).toHaveAttribute("open");
  expect(details.querySelector(".katex")).not.toBeNull();
  expect(details.querySelector("p [data-guide-source-start]")).toHaveAttribute("data-guide-source-start", String(Array.from("• Noise schedule 噪声调度：").length));
  expect(screen.getByText("A background paragraph without a term delimiter.")).toBeVisible();
  expect(view.container.querySelectorAll(".guide-glossary-entry")).toHaveLength(1);
});

it.each([
  ["Transformer（Transformer）", "Transformer"],
  ["Transformer (transformer)", "Transformer"],
  ["Transformer", "Transformer"],
  ["生成对抗网络（Generative Adversarial Network，GAN）", "生成对抗网络（Generative Adversarial Network，GAN）"],
  ["BERT (Bidirectional Encoder Representations from Transformers)", "BERT (Bidirectional Encoder Representations from Transformers)"],
])("maps glossary title %s and explanation back to their stored text", (label, title) => {
  const body = "𠮷是一个例子。";
  const text = `• ${label}： ${body}`;
  const { container } = render(<GuideGlossaryEntry id="term" text={text}/>);
  const entry = container.querySelector("details")!;
  const summary = entry.querySelector("summary")!;
  expect(summary.textContent).toBe(title);
  fireEvent.click(summary);
  for (const [element, expected] of [[summary, title], [entry.querySelector("p")!, body]] as const) {
    const range = document.createRange();
    range.selectNodeContents(element);
    const start = sourceBoundary(entry, range.startContainer, range.startOffset, "start");
    const end = sourceBoundary(entry, range.endContainer, range.endOffset, "end");
    expect(Array.from(text).slice(start, end).join("")).toBe(expected);
  }
});

it("uses fixed labels for standard chapters and preserves paper-specific method titles", () => {
  const r = revision();
  const entries = [["overview", "概览：DDPM"], ["why-it-matters", "为什么重要：噪声"], ["big-idea", "核心想法：逐步加噪"], ["noise-training", "如何训练噪声预测器"], ["results", "结果：图像质量"], ["deep-dive", "渐进生成与压缩"], ["limitations", "局限：速度"], ["glossary", "术语表：领域"]];
  r.plan = { ...r.plan!, layout: "flashpapers", sections: entries.map(([role,title]) => ({role,title,focus:"",terms:[],asset_ids:[],claim_ids:[],evidence_ids:[]})) };
  r.sections = entries.map(([role,title]) => ({ ...section(role), title }));
  render(<PaperGuideReader revision={r} title="DDPM" language="zh" onEvidence={() => {}} onSelection={() => {}} />);
  expect(screen.getAllByRole("heading", {level:2}).map(el => el.textContent)).toEqual(["论文简述", "问题与瓶颈", "核心想法", "如何训练噪声预测器", "实验结果", "渐进生成与压缩", "局限", "术语表"]);
  expect(screen.getByRole("button", {name:"核心想法 可阅读"})).toBeInTheDocument();
});

it.each([
  ["Mamba: Linear-Time Sequence Modeling with Selective State Spaces", "Mamba：用选择性状态空间进行线性时间序列建模", "upload.pdf"],
  ["Mamba：用选择性状态空间进行线性时间序列建模", "从选择性复制到硬件感知扫描", "Mamba: Linear-Time Sequence Modeling with Selective State Spaces (2312.00752v2).pdf"],
])("renders English above Chinese for current and earlier plans", (title, subtitle, documentName) => {
  const r = revision();
  r.plan = { layout: "flashpapers", title, subtitle, sections: [], claims: [], questions: [], required_assets: [], term_policy: [], profiles: { primary: null, secondary: [], uncertainty_reason: "" } };
  const view = render(<PaperGuideReader revision={r} title={documentName} language="zh" onEvidence={vi.fn()} onSelection={vi.fn()} />);
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Mamba: Linear-Time Sequence Modeling with Selective State Spaces");
  expect(view.container.querySelector(".flashpapers-subtitle")).toHaveTextContent("Mamba：用选择性状态空间进行线性时间序列建模");
});

it("presents a published results playground as evidence and keeps method controls", () => {
  const r = revision();
  r.plan = { layout: "flashpapers", sections: [], claims: [], questions: [], required_assets: [], term_policy: [],
    profiles: { primary: null, secondary: [], uncertainty_reason: "" } };
  r.sections = [section("method"), section("results")];
  for (const chapter of r.sections) {
    Object.assign(chapter.blocks[0], { kind: "playground", playground: { html: "<table><tr><td>17.4</td></tr></table>" } });
  }
  render(<PaperGuideReader revision={r} title="FlashAttention" language="zh" onEvidence={vi.fn()} onSelection={vi.fn()} />);
  const results = screen.getByTitle("实验结果对照").closest(".guide-playground") as HTMLElement;
  expect(within(results).getByText("实验结果")).toBeVisible();
  expect(within(results).queryByRole("button", { name: "重载交互" })).toBeNull();
  expect(screen.getAllByRole("button", { name: "重载交互" })).toHaveLength(1);
});
