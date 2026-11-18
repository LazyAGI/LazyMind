import PaperGuideFlow from "./PaperGuideFlow";
import GuideGlossaryEntry from "./GuideGlossaryEntry";
import GuideSectionIcon from "./GuideSectionIcon";
import { GuideSkeleton } from "./GuideGenerationProgress";
import PaperGuidePlayground from "./PaperGuidePlayground";
import "./FlashpapersReader.scss";
import PaperGuideTableText from "./PaperGuideTableText";
import PaperGuideInteraction from "./PaperGuideInteraction";
import { coverageWarning } from "./coverageWarning";
import { Fragment, useLayoutEffect, useRef, useState } from "react";
import type {
  PaperGuideBlock,
  PaperGuideClaim,
  PaperGuidePublishedSection,
  PaperGuideRevisionResponse,
  PaperGuideProgressRequest,
} from "@/api/generated/core-client";
import type { DocumentChatSelection } from "../PdfTemporaryChat/types";
import { restoreTextPosition, visibleTextOffset } from "./readingPosition";
import PaperGuideText, { guideTextSegments } from "./PaperGuideText";
import { sourceBoundary } from "./sourceText";
import { isGuideRunActive } from "./guideRunState";

function articleHeader(documentName: string, title: string, subtitle: string, zh: boolean) {
  // Earlier Chinese guides stored their localized headline in title.
  if (zh && /\p{Script=Han}/u.test(title)) {
    const source = documentName.replace(/\.pdf$/i, "").replace(/\s*\((?:arxiv:\s*)?\d{4}\.\d{4,5}(?:v\d+)?\)$/i, "").trim();
    if (/[a-z]{2}/i.test(source) && !/\p{Script=Han}/u.test(source) &&
        !/^(?:arxiv[: _-]*)?\d{4}\.\d{4,5}(?:v\d+)?$/i.test(source)) {
      return { title: source, subtitle: title };
    }
  }
  return { title: title || documentName, subtitle };
}

function headingId(revisionId: string, sectionKey: string, blockId?: string) {
  return blockId === undefined
    ? `guide-${revisionId}-${sectionKey}`
    : `guide-subsection-${[revisionId, sectionKey, blockId].map(encodeURIComponent).join("/")}`;
}

function blockEvidenceIds(block: PaperGuideBlock, claims: readonly PaperGuideClaim[]) {
  const ids = new Set(block.evidence_ids);
  const referenced = new Set(block.claim_ids);
  for (const claim of claims) {
    if (referenced.has(claim.id)) {
      for (const id of claim.evidence_ids ?? []) ids.add(id);
    }
  }
  return [...ids];
}

interface Props {
  revision: PaperGuideRevisionResponse;
  title: string;
  language: string;
  onEvidence: (id: string, opener: HTMLButtonElement) => void;
  onSelection: (selection: DocumentChatSelection) => void;
  progress?: PaperGuideProgressRequest | null;
  onProgress?: (progress: PaperGuideProgressRequest) => void;
  compact?: boolean;
  renderAsset?: (block: PaperGuideBlock) => React.ReactNode;
}
export default function PaperGuideReader({
  revision,
  title,
  language,
  onEvidence,
  onSelection,
  progress,
  onProgress,
  compact,
  renderAsset,
}: Props) {
  const zh = language === "zh";
  const flash = revision.plan?.layout === "flashpapers";
  const header = articleHeader(title, flash ? revision.plan?.title?.trim() || "" : "", flash ? revision.plan?.subtitle?.trim() || "" : "", zh);
  const running = isGuideRunActive(revision.run.stage);
  const chapterState = (section: PaperGuidePublishedSection) => section.blocks.length ? (zh ? "可阅读" : "Ready") : !running ? (zh ? "已暂停" : "Paused") : section.publication_status === "generating" ? (zh ? "生成中" : "Generating") : (zh ? "排队中" : "Queued");
  const fixedTitles: Record<string, string> = zh
    ? { overview: "论文简述", "why-it-matters": "问题与瓶颈", "big-idea": "核心想法", results: "实验结果", limitations: "局限", glossary: "术语表" }
    : { overview: "Paper Summary", "why-it-matters": "Problem and Bottlenecks", "big-idea": "The Big Idea", results: "Experimental Results", limitations: "Limitations", glossary: "Glossary" };
  const rawSections: PaperGuidePublishedSection[] = flash && revision.plan?.sections.length
    ? revision.plan.sections.map(planned => revision.sections.find(section => section.role === planned.role) ?? {
      key: `section:${planned.role}:v1`, role: planned.role, title: planned.title, blocks: [], claims: [], claim_ids: [], summary: "", change_version: 0, publication_status: "pending",
    }) : revision.sections;
  const orderedSections = rawSections.map(section => flash ? { ...section, title: fixedTitles[section.role] ?? section.title } : section);
  const resultsIndex = orderedSections.findIndex(section => section.role === "results");
  const limitationsIndex = orderedSections.findIndex(section => section.role === "limitations");
  function renderSectionIcon(section: PaperGuidePublishedSection, index: number) {
    const optional = resultsIndex >= 0 && index > resultsIndex && index < limitationsIndex;
    return <GuideSectionIcon role={section.role} title={section.title} optional={optional}
      description={revision.plan?.sections.find(planned => planned.role === section.role)?.focus}/>;
  }
  const root = useRef<HTMLDivElement>(null);
  const restored = useRef("");
  const [active, setActive] = useState("");
  const [selected, setSelected] = useState<{
    revisionId: string;
    includesTable: boolean;
    section: PaperGuidePublishedSection;
    block: PaperGuideBlock;
    start: number;
    end: number;
    text: string;
  }>();
  useLayoutEffect(() => {
    if (
      selected &&
      (selected.revisionId !== revision.id ||
        !revision.sections.some(
          (section) => section.key === selected.section.key,
        ))
    ) {
      setSelected(undefined);
    }
  }, [revision, selected]);
  const numbers = new Map<string, number>();
  const sectionsWithSources = orderedSections.map((section) => {
    // Match server selection context: direct sources, then plan and local claims.
    const claims = [...(revision.plan?.claims ?? []), ...(section.claims ?? [])];
    const blocks = section.blocks.map((block) => {
      const sources = blockEvidenceIds(block, claims);
      for (const id of sources)
        if (!numbers.has(id)) numbers.set(id, numbers.size + 1);
      return { block, sources };
    });
    const flow = section.role === "overview" ? revision.plan?.paper_flow : undefined;
    const summary = flow && blocks.find(({ block }) => block.kind === "callout");
    return { section, blocks: summary ? [summary, ...blocks.filter(item => item !== summary)] : blocks, flow, summary };
  });
  const selectedSourceId =
    selected && selected.revisionId === revision.id
      ? sectionsWithSources
          .find(({ section }) => section.key === selected.section.key)
          ?.blocks.find(({ block }) => block.id === selected.block.id)?.sources[0]
      : undefined;
  const anchor = useRef<PaperGuideProgressRequest>();
  const lastWidth = useRef(0);
  const layoutPending = useRef(false);
  const frame = useRef<number>();
  const currentRevision = useRef(revision);
  currentRevision.current = revision;
  function restoreAnchor() {
    const container = root.current;
    const saved = anchor.current;
    if (
      !container ||
      !saved ||
      saved.revision_id !== currentRevision.current.id ||
      container.getBoundingClientRect().width <= 0
    )
      return;
    const block = Array.from(
      container.querySelectorAll<HTMLElement>("[data-guide-block]"),
    ).find(
      (element) =>
        element.dataset.guideBlock === saved.block_id &&
        element.dataset.guideSection === saved.section_key,
    );
    const text = block?.querySelector<HTMLElement>("[data-guide-text]");
    if (text) {
      if (text instanceof HTMLDetailsElement && saved.offset > 0) text.open = true;
      restoreTextPosition(container, text, saved.offset);
    }
  }
  function scheduleLayoutRestore() {
    layoutPending.current = true;
    if (frame.current !== undefined) cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      frame.current = undefined;
      restoreAnchor();
      layoutPending.current = false;
    });
  }
  // Only initialize once per revision. Ordinary checkpoint appends do not move the reader.
  useLayoutEffect(() => {
    if (restored.current === revision.id || !revision.sections.length) return;
    restored.current = revision.id;
    anchor.current =
      progress?.revision_id === revision.id ? progress : undefined;
    lastWidth.current = root.current?.getBoundingClientRect().width || 0;
    restoreAnchor();
  }, [revision, progress]);
  useLayoutEffect(() => {
    const container = root.current;
    if (!container) return;
    lastWidth.current = container.getBoundingClientRect().width;
    const observer = new ResizeObserver(() => {
      const width = container.getBoundingClientRect().width;
      if (width === lastWidth.current) return;
      lastWidth.current = width;
      // The last scroll anchor was measured before reflow. Never capture from
      // the newly wrapped lines (or a hidden zero-width pane) here.
      scheduleLayoutRestore();
    });
    observer.observe(container);
    return () => {
      observer.disconnect();
      if (frame.current !== undefined) cancelAnimationFrame(frame.current);
    };
  }, []);
  const lastCompact = useRef(compact);
  useLayoutEffect(() => {
    if (lastCompact.current === compact) return;
    lastCompact.current = compact;
    scheduleLayoutRestore();
  }, [compact]);
  function ask(
    section: PaperGuidePublishedSection,
    block: PaperGuideBlock,
    example = false,
    range?: typeof selected,
  ) {
    if (!revision.source_current) return;
    const start = range?.start ?? 0,
      end = range?.end ?? Array.from(block.text).length;
    onSelection({
      source: "paper_guide",
      text: range?.text ?? block.text,
      question: example
        ? zh
          ? "请为这段导读举一个明确标注为假设的教学例子。"
          : "Give a clearly labeled hypothetical example for this guide passage."
        : zh
          ? "请解释这段导读，区分教学说明和论文证据。"
          : "Explain this guide passage, separating teaching from paper evidence.",
      quickExplain: true,
      guideRef: {
        revision_id: range?.revisionId ?? revision.id,
        section_key: section.key,
        block_id: block.id,
        start,
        end,
        language,
      },
    });
    setSelected(undefined);
  }
  function capture() {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount) {
      setSelected(undefined);
      return;
    }
    const range = sel.getRangeAt(0);
    const element = (
      range.startContainer instanceof Element
        ? range.startContainer
        : range.startContainer.parentElement
    )?.closest<HTMLElement>("[data-guide-text]");
    if (!element || !element.contains(range.endContainer)) {
      setSelected(undefined);
      return;
    }
    const blockId = element.dataset.guideText;
    const sectionKey = element.closest<HTMLElement>("[data-guide-section]")
      ?.dataset.guideSection;
    const section = revision.sections.find((s) => s.key === sectionKey);
    const block = section?.blocks.find((b) => b.id === blockId);
    if (!section || !block) {
      setSelected(undefined);
      return;
    }
    const start = sourceBoundary(
      element,
      range.startContainer,
      range.startOffset,
      "start",
    );
    const end = sourceBoundary(
      element,
      range.endContainer,
      range.endOffset,
      "end",
    );
    if (start >= end) {
      setSelected(undefined);
      return;
    }
    setSelected({
      revisionId: revision.id,
      includesTable: Array.from(
        element.querySelectorAll<HTMLElement>('[data-guide-atomic="table"]'),
      ).some(
        (table) =>
          Number(table.dataset.guideSourceStart) < end &&
          Number(table.dataset.guideSourceEnd) > start,
      ),
      section,
      block,
      start,
      end,
      text: Array.from(block.text).slice(start, end).join(""),
    });
  }
  function navigate(id: string) {
    const el = document.getElementById(id);
    setActive(id);
    const scroller = root.current;
    if (el && scroller?.contains(el)) {
      // scrollIntoView also scrolls ancestor panes and can hide the mode toolbar.
      scroller.scrollTop += el.getBoundingClientRect().top - scroller.getBoundingClientRect().top - 24;
    }
    el?.focus({ preventScroll: true });
  }
  return (
    <div className={`paper-guide-reader${flash ? " is-flashpapers" : ""}${compact ? " is-comparing" : ""}`}>
      <nav
        className="paper-guide-outline"
        aria-label={zh ? "本文目录" : "Article outline"}
      >
        <details open={flash || !compact}>
          <summary>{zh ? "本文目录" : "Article outline"}</summary>
          {orderedSections.map((section, sectionIndex) => {
            const sectionId = headingId(revision.id, section.key);
            return (
              <div key={section.role}>
                <button type="button" aria-current={active === sectionId || (flash && !active && sectionIndex === 0) ? "location" : undefined} onClick={() => navigate(sectionId)}>
                  {flash && renderSectionIcon(section, sectionIndex)}
                  <span className={flash ? "guide-outline-label" : undefined}>{section.title}{flash && <small className={section.blocks.length ? "guide-ready-label" : undefined} data-state={section.blocks.length ? "ready" : running ? section.publication_status : "paused"}>{chapterState(section)}</small>}</span>
                </button>
                {!flash && section.blocks.filter(({ kind }) => kind === "heading").map((block) => {
                  const id = headingId(revision.id, section.key, block.id);
                  return (
                    <button key={id} type="button" className="paper-guide-outline-subsection" aria-current={active === id ? "location" : undefined} onClick={() => navigate(id)}>
                      {block.text}
                    </button>
                  );
                })}
              </div>
            );
          })}
        </details>
      </nav>
      <div
        className="paper-guide-scroll"
        ref={root}
        onMouseUp={capture}
        onKeyUp={capture}
        onTouchEnd={capture}
        onScroll={() => {
          const geometry = root.current?.getBoundingClientRect();
          if (
            !geometry ||
            geometry.width <= 0 ||
            layoutPending.current ||
            geometry.width !== lastWidth.current
          )
            return;
          const top = geometry.top;
          const elements = Array.from(
            root.current?.querySelectorAll<HTMLElement>("[data-guide-block]") ||
              [],
          );
          const el = elements.find(
            (e) => e.getBoundingClientRect().bottom > top + 40,
          );
          const section = revision.sections.find(
            (s) => s.key === el?.dataset.guideSection,
          );
          if (el && section) {
            let index = section.blocks.findIndex((block) => block.id === el.dataset.guideBlock);
            while (index >= 0 && section.blocks[index].kind !== "heading") index--;
            const subsection = section.blocks[index];
            setActive(headingId(revision.id, section.key, flash ? undefined : subsection?.id));
            const position: PaperGuideProgressRequest = {
              revision_id: revision.id,
              section_key: section.key,
              block_id: el.dataset.guideBlock!,
              offset: el.querySelector<HTMLElement>("[data-guide-text]")
                ? visibleTextOffset(
                    el.querySelector<HTMLElement>("[data-guide-text]")!,
                    top + 24,
                  )
                : 0,
              mode: "article",
            };
            anchor.current = position;
            onProgress?.(position);
          }
        }}
      >
        <article className="paper-guide-article">
          <h1>{header.title}</h1>
          {header.subtitle && header.subtitle !== header.title && <p className="flashpapers-subtitle">{header.subtitle}</p>}
          {sectionsWithSources.map(({ section, blocks, flow, summary }, sectionIndex) => (
            <section key={section.role} aria-label={section.title} data-guide-role={section.role}>
              <h2 id={headingId(revision.id, section.key)} tabIndex={-1}>
                {flash && renderSectionIcon(section, sectionIndex)}
                {section.title}
              </h2>
              {flow && !summary && <PaperGuideFlow flow={flow} zh={zh} />}
              {flash && section.blocks.length === 0 && <div className="guide-chapter-placeholder" aria-busy={running}>
                <p role="status"><strong>{chapterState(section)}</strong> · {running ? (zh ? "整篇预计约 2 分钟，完成后自动展示本章内容。" : "The guide usually takes about 2 minutes. This chapter updates automatically.") : (zh ? "本章尚未完成，继续生成后会自动更新。" : "Resume generation to finish this chapter.")}</p>
                {running && <GuideSkeleton/>}
              </div>}
              {section.publication_status === "correction_required" && (
                <p role="status" className="paper-guide-warning">
                  {zh
                    ? "待更正：这一节已发现问题，以下保留原文供核对。"
                    : "Correction pending"}
                </p>
              )}
              {blocks.map(({ block, sources }) => (
                <Fragment key={`${section.key}/${block.id}`}>
                <div
                  data-guide-block={block.id}
                  data-guide-section={section.key}
                  className={`paper-guide-block paper-guide-block--${block.kind}`}
                >
                  {!flash && <strong className="paper-guide-attribution">
                    {block.attribution === "external_background"
                      ? zh
                        ? "外部背景 · 未核实为本篇论文依据"
                        : "External background · not verified as evidence from this paper"
                      : block.kind === "example"
                        ? zh
                          ? "教学例子 · 假设情境"
                          : "Teaching example · hypothetical"
                        : block.attribution === "teaching"
                          ? zh
                            ? "教学说明"
                            : "Teaching explanation"
                          : block.attribution === "interpretation"
                            ? zh
                              ? "导读解释 · 分析性说明"
                              : "Guide interpretation"
                            : zh
                              ? "论文陈述 · 导读转述"
                              : "Paper statement · guide paraphrase"}
                  </strong>}
                  {block.kind === "figure" && renderAsset ? null : flash && section.role === "glossary" && block.kind === "paragraph" ? (
                    <GuideGlossaryEntry text={block.text} id={block.id}/>
                  ) : block.kind === "formula" ? (
                    <div data-guide-text={block.id}><PaperGuideText text={block.text} displayMath /></div>
                  ) : block.kind === "table" ? (
                    <div
                      className="paper-guide-table-text"
                      data-guide-text={block.id}
                    >
                      <PaperGuideTableText
                        text={block.text}
                        language={language}
                      />
                    </div>
                  ) : block.kind === "detail" ? (
                    <details>
                      <summary>
                        {zh ? "补充基础" : "Further background"}
                      </summary>
                      <p data-guide-text={block.id}>
                        <PaperGuideText text={block.text} />
                      </p>
                    </details>
                  ) : block.kind === "heading" ? (
                    <h3 data-guide-text={block.id} id={headingId(revision.id, section.key, block.id)} tabIndex={-1}>
                      <PaperGuideText text={block.text} />
                    </h3>
                  ) : (
                    <p data-guide-text={block.id}>
                      <PaperGuideText text={block.text} emphasis={block.kind === "paragraph" ? block.emphasis : undefined} />
                    </p>
                  )}
                  {flash && block.kind === "playground" && block.playground && <PaperGuidePlayground html={block.playground.html} language={language}
                    results={section.role === "results"}
                    title={section.role === "results" ? (zh ? "实验结果对照" : "Experimental comparison") : `${section.title} · ${zh ? "交互探索" : "Interactive exploration"}`} />}
                  {block.kind === "interactive" && block.interaction && (
                    <PaperGuideInteraction key={revision.id} interaction={block.interaction} language={language} />
                  )}
                  {block.asset_id && renderAsset?.(block)}
                  {!flash && !block.asset_id && sources.length > 0 &&
                    (block.kind === "paragraph" || block.kind === "callout") &&
                    guideTextSegments(block.text).some(segment =>
                      segment.display || segment.latex?.includes("=")
                    ) && (
                    <small className="paper-guide-attribution">
                      {zh
                        ? "导读公式说明 · 无单独原式预览，可从来源打开原文。"
                        : "Guide equation explanation · Open the source to see the original in context."}
                    </small>
                  )}
                  {!flash && <div className="paper-guide-block-tools">
                    {!flash && sources.map((id) => (
                      <button
                        key={id}
                        className="paper-guide-citation"
                        onClick={(event) => onEvidence(id, event.currentTarget)}
                      >
                        {zh ? "来源" : "Source"} {numbers.get(id)}
                      </button>
                    ))}
                    <details className="paper-guide-passage-actions">
                      <summary aria-label={zh ? "选段操作" : "Passage actions"}>
                        …
                      </summary>
                      <button disabled={!revision.source_current} onClick={() => ask(section, block)}>
                        {zh ? "解释这段" : "Explain this passage"}
                      </button>
                      <button disabled={!revision.source_current} onClick={() => ask(section, block, true)}>
                        {zh ? "举个例子" : "Give an example"}
                      </button>
                      {sources[0] && (
                        <button
                          onClick={(event) =>
                            onEvidence(sources[0], event.currentTarget)
                          }
                        >
                          {zh ? "查看原文" : "View source"}
                        </button>
                      )}
                    </details>
                  </div>}
                </div>
                {flow && summary?.block === block && <PaperGuideFlow flow={flow} zh={zh} />}
                </Fragment>
              ))}
            </section>
          ))}
          {!flash && revision.status === "partial" &&
            isGuideRunActive(revision.run.stage) && (
              <p className="paper-guide-next" role="status">
                {zh
                  ? "下一节通过核验后会出现在这里。"
                  : "The next section will appear here after review."}
              </p>
            )}
          {revision.coverage?.warnings?.length > 0 && (
            <details open={!flash} className="paper-guide-warning flashpapers-materials">
              <summary>{zh ? "材料与覆盖范围" : "Materials and coverage"}</summary>
              <ul>
                {revision.coverage.warnings.map((warning, i) => (
                  <li key={i}>{coverageWarning(warning, zh)}</li>
                ))}
              </ul>
            </details>
          )}
        </article>
      </div>
      {selected && (
        <div
          className="paper-guide-selection"
          role="toolbar"
          aria-label={zh ? "导读选段" : "Guide passage"}
        >
          {selected.includesTable && (
            <p className="paper-guide-table-selection-hint">
              {zh
                ? "表格选段将引用整表，以保留行列关系。"
                : "The full table is included to preserve row and column context."}
            </p>
          )}
          <p className="paper-guide-selection-preview">{selected.text}</p>
          <button
            disabled={!revision.source_current}
            onClick={() =>
              ask(selected.section, selected.block, false, selected)
            }
          >
            {zh ? "解释这段" : "Explain this passage"}
          </button>
          <button
            disabled={!revision.source_current}
            onClick={() =>
              ask(selected.section, selected.block, true, selected)
            }
          >
            {zh ? "举个例子" : "Give an example"}
          </button>
          {selectedSourceId && (
            <button
              onClick={(event) => {
                onEvidence(selectedSourceId, event.currentTarget);
                setSelected(undefined);
              }}
            >
              {zh ? "查看原文" : "View source"}
            </button>
          )}
          <button
            onClick={() => setSelected(undefined)}
            aria-label={zh ? "关闭" : "Close"}
          >
            ×
          </button>
        </div>
      )}
    </div>
  );
}
