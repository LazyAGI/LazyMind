import { useLayoutEffect, useRef, useState } from "react";
import type {
  PaperGuideBlock,
  PaperGuidePublishedSection,
  PaperGuideRevisionResponse,
  PaperGuideProgressRequest,
} from "@/api/generated/core-client";
import type { DocumentChatSelection } from "../PdfTemporaryChat/types";
import { restoreTextPosition, visibleTextOffset } from "./readingPosition";
import { unicodeRange } from "./usePaperGuide";
interface Props {
  revision: PaperGuideRevisionResponse;
  title: string;
  metadata?: { authors: string[]; year: number };
  language: string;
  onEvidence: (id: string) => void;
  onSelection: (selection: DocumentChatSelection) => void;
  progress?: PaperGuideProgressRequest | null;
  onProgress?: (progress: PaperGuideProgressRequest) => void;
  compact?: boolean;
  renderAsset?: (block: PaperGuideBlock) => React.ReactNode;
}
export default function PaperGuideReader({
  revision,
  title,
  metadata,
  language,
  onEvidence,
  onSelection,
  progress,
  onProgress,
  compact,
  renderAsset,
}: Props) {
  const zh = language === "zh";
  const root = useRef<HTMLDivElement>(null);
  const restored = useRef("");
  const [active, setActive] = useState("");
  const [selected, setSelected] = useState<{
    section: PaperGuidePublishedSection;
    block: PaperGuideBlock;
    start: number;
    end: number;
    text: string;
  }>();
  const numbers = new Map<string, number>();
  for (const s of revision.sections)
    for (const b of s.blocks)
      for (const id of b.evidence_ids || [])
        if (!numbers.has(id)) numbers.set(id, numbers.size + 1);
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
    if (text) restoreTextPosition(container, text, saved.offset);
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
        revision_id: revision.id,
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
    if (!element || !element.contains(range.endContainer)) return;
    const blockId = element.dataset.guideText;
    const section = revision.sections.find((s) =>
      s.blocks.some((b) => b.id === blockId),
    );
    const block = section?.blocks.find((b) => b.id === blockId);
    if (!section || !block) return;
    const before = range.cloneRange();
    before.selectNodeContents(element);
    before.setEnd(range.startContainer, range.startOffset);
    const start = before.toString().length;
    setSelected({
      section,
      block,
      ...unicodeRange(block.text, start, start + range.toString().length),
      text: range.toString(),
    });
  }
  function navigate(id: string) {
    const el = document.getElementById(`guide-${revision.id}-${id}`);
    el?.scrollIntoView?.({ block: "start" });
    el?.focus({ preventScroll: true });
  }
  return (
    <div className={`paper-guide-reader${compact ? " is-comparing" : ""}`}>
      <nav
        className="paper-guide-outline"
        aria-label={zh ? "本文目录" : "Article outline"}
      >
        <details open={!compact}>
          <summary>{zh ? "本文目录" : "Article outline"}</summary>
          {revision.sections.map((s) => (
            <button
              key={s.role}
              aria-current={active === s.key ? "location" : undefined}
              onClick={() => navigate(s.blocks[0]?.id || s.key)}
            >
              {s.title}
            </button>
          ))}
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
          const section = revision.sections.find((s) =>
            s.blocks.some((b) => b.id === el?.dataset.guideBlock),
          );
          if (el && section) {
            setActive(section.key);
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
          <h1>{title}</h1>
          {metadata && (
            <p className="paper-guide-byline">
              {[
                metadata.authors.join(", "),
                metadata.year > 0 ? String(metadata.year) : "",
              ]
                .filter(Boolean)
                .join(" · ")}
            </p>
          )}
          {revision.sections.map((section) => (
            <section key={section.role} aria-label={section.title}>
              <h2
                id={`guide-${revision.id}-${section.blocks[0]?.id || section.key}`}
                tabIndex={-1}
              >
                {section.title}
              </h2>
              {section.publication_status === "correction_required" && (
                <p role="status" className="paper-guide-warning">
                  {zh
                    ? "待更正：这一节已发现问题，以下保留原文供核对。"
                    : "Correction pending"}
                </p>
              )}
              {section.blocks.map((block) => (
                <div
                  key={`${section.key}/${block.id}`}
                  data-guide-block={block.id}
                  data-guide-section={section.key}
                  className={`paper-guide-block paper-guide-block--${block.kind}`}
                >
                  <strong className="paper-guide-attribution">
                    {block.attribution === "external_background"
                      ? zh
                        ? "外部背景 · 未核实为本篇论文依据"
                        : "External background · not verified as evidence from this paper"
                      : block.attribution === "teaching" ||
                          block.kind === "example"
                        ? zh
                          ? "教学例子 · 假设情境"
                          : "Teaching example · hypothetical"
                        : block.attribution === "interpretation"
                          ? zh
                            ? "导读解释 · 分析性说明"
                            : "Guide interpretation"
                          : zh
                            ? "论文陈述 · 导读转述"
                            : "Paper statement · guide paraphrase"}
                  </strong>
                  {block.kind === "detail" ? (
                    <details>
                      <summary>
                        {zh ? "补充基础" : "Further background"}
                      </summary>
                      <p data-guide-text={block.id}>{block.text}</p>
                    </details>
                  ) : block.kind === "heading" ? (
                    <h3 data-guide-text={block.id}>{block.text}</h3>
                  ) : (
                    <p data-guide-text={block.id}>{block.text}</p>
                  )}
                  {block.asset_id && renderAsset?.(block)}
                  <div className="paper-guide-block-tools">
                    {(block.evidence_ids || []).map((id) => (
                      <button
                        key={id}
                        className="paper-guide-citation"
                        onClick={() => onEvidence(id)}
                      >
                        {zh ? "来源" : "Source"} {numbers.get(id)}
                      </button>
                    ))}
                    <details className="paper-guide-passage-actions">
                      <summary aria-label={zh ? "选段操作" : "Passage actions"}>
                        …
                      </summary>
                      <button onClick={() => ask(section, block)}>
                        {zh ? "解释这段" : "Explain this passage"}
                      </button>
                      <button onClick={() => ask(section, block, true)}>
                        {zh ? "举个例子" : "Give an example"}
                      </button>
                      {block.evidence_ids?.[0] && (
                        <button
                          onClick={() => onEvidence(block.evidence_ids[0])}
                        >
                          {zh ? "查看原文" : "View source"}
                        </button>
                      )}
                    </details>
                  </div>
                </div>
              ))}
            </section>
          ))}
          {!["complete", "limited", "failed", "canceled", "paused"].includes(
            revision.status,
          ) && (
            <p className="paper-guide-next" role="status">
              {zh
                ? "下一节通过核验后会出现在这里。"
                : "The next section will appear here after review."}
            </p>
          )}
          {revision.coverage?.warnings?.length > 0 && (
            <aside className="paper-guide-warning">
              <h2>{zh ? "材料与覆盖范围" : "Materials and coverage"}</h2>
              <ul>
                {revision.coverage.warnings.map((warning, i) => (
                  <li key={i}>{warning}</li>
                ))}
              </ul>
            </aside>
          )}
        </article>
      </div>
      {selected && (
        <div
          className="paper-guide-selection"
          role="toolbar"
          aria-label={zh ? "导读选段" : "Guide passage"}
        >
          <button
            onClick={() =>
              ask(selected.section, selected.block, false, selected)
            }
          >
            {zh ? "解释这段" : "Explain this passage"}
          </button>
          <button
            onClick={() =>
              ask(selected.section, selected.block, true, selected)
            }
          >
            {zh ? "举个例子" : "Give an example"}
          </button>
          {selected.block.evidence_ids?.[0] && (
            <button
              onClick={() => {
                onEvidence(selected.block.evidence_ids[0]);
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
