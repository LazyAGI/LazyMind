import { useState } from "react";
import type { PdfTextSelection } from "./RenderPdf";
import { normalizePdfSelectionText } from "./pdfSelectionText";
import type { PdfIRBlock, PdfIRDocument } from "@/modules/knowledge/api/pdfIR";
import { readablePaperText } from "@/modules/knowledge/api/paperReader";
import { FormulaMathText } from "./FormulaMathText";
import { usePaperReaderLabels } from "./paperReaderLabels";
import "./PdfPaperOverview.css";

interface Props {
  document?: PdfIRDocument;
  paperTitle?: string;
  onOpenSource: (blockId?: string) => void;
  onAddVocabularySelection?: (selection: PdfTextSelection) => void;
}

function paperMetadata(document?: PdfIRDocument, paperTitle?: string) {
  const firstPage = document?.blocks.filter(block => block.page === 0) || [];
  const isAbstractLabel = (block: PdfIRBlock) => /^(?:abstract|摘要)\s*[.:：]?$/i.test(block.text?.trim() || "");
  const title = firstPage.find(block => block.type === "title" && block.text?.trim() && !isAbstractLabel(block));
  const abstract = firstPage.find(block => block.type === "abstract" && !isAbstractLabel(block) || /^(?:abstract|摘要)\s*[.:：—-]\s*\S/i.test(block.text?.trim() || ""));
  let abstractText = abstract?.text?.replace(/^(?:abstract|摘要)\s*[.:：—-]?\s*/i, "") || "";
  if (!abstractText) {
    const headingIndex = firstPage.findIndex(isAbstractLabel);
    if (headingIndex >= 0) {
      const paragraphs: string[] = [];
      for (const block of firstPage.slice(headingIndex + 1)) {
        if (block.type === "heading" || block.type === "title" || /^(?:\d+(?:\.\d+)*\.?\s+)?(?:introduction|background|keywords|引言|绪论|关键词)\s*[.:：]?$/i.test(block.text?.trim() || "")) break;
        if (block.type !== "text" || !block.text?.trim()) continue;
        if (paragraphs.length >= 3 || paragraphs.join(" ").length + block.text.length > 6000) break;
        paragraphs.push(block.text.trim());
      }
      abstractText = paragraphs.join(" ");
    }
  }
  return {
    title: readablePaperText(title?.text || paperTitle || ""),
    abstract: readablePaperText(abstractText),
    abstractId: abstract?.id,
  };
}

export function PdfPaperOverview({ document, paperTitle, onOpenSource, onAddVocabularySelection }: Props) {
  const { language, label } = usePaperReaderLabels();
  const copy = language === "zh" ? {
    abstract: "原文摘要", missing: document ? "未从原文识别出摘要，请查看论文。" : "文档解析完成后，这里会显示原文摘要。",
    quickRead: "论文速读", unavailable: "论文速读暂时不可用", note: "你仍可以阅读原文摘要和完整论文。",
    source: "查看原文", paper: "阅读论文",
  } : {
    abstract: "Original abstract", missing: document ? "No abstract was identified in the source. Open the paper to read it." : "The original abstract will appear after document parsing finishes.",
    quickRead: "Quick read", unavailable: "Quick read is temporarily unavailable", note: "You can still read the original abstract and full paper.",
    source: "View source", paper: "Read paper",
  };
  const metadata = paperMetadata(document, paperTitle);
  const [vocabulary, setVocabulary] = useState<{source: string; selection: PdfTextSelection} | null>(null);
  const sourceKey = `${document?.source_sha256}:${metadata.abstract}`;
  return <article className="pdf-paper-overview" aria-label={label("insight")}>
    <header className="pdf-paper-overview__header">
      <span>{label("insight")}</span>
      <h1>{metadata.title || label("reading")}</h1>
    </header>
    <section className="pdf-paper-overview__abstract">
      <div className="pdf-paper-overview__section-heading"><h2>{copy.abstract}</h2>{metadata.abstractId && <button type="button" onClick={() => onOpenSource(metadata.abstractId)}>{copy.source} ↗</button>}</div>
      {metadata.abstract ? <p onMouseUp={event => {
        if (!onAddVocabularySelection) return;
        const selected = window.getSelection();
        const text = normalizePdfSelectionText(selected?.toString() || "");
        if (!selected || selected.isCollapsed || !selected.rangeCount || !/[A-Za-z]/.test(text)
          || !event.currentTarget.contains(selected.getRangeAt(0).commonAncestorContainer)) {
          setVocabulary(null);
          return;
        }
        // paperMetadata only exposes original abstract blocks from zero-based page 0.
        setVocabulary({source: sourceKey, selection: {text, context: metadata.abstract, page: 1}});
      }}><FormulaMathText text={metadata.abstract}/></p> : <p className="pdf-paper-overview__muted">{copy.missing}</p>}
      {onAddVocabularySelection && vocabulary?.source === sourceKey && <button type="button" className="pdf-reader-source-link" onMouseDown={event => event.preventDefault()} onClick={() => {
        onAddVocabularySelection(vocabulary.selection);
        setVocabulary(null);
      }}>{language === "zh" ? "加入生词" : "Add vocabulary"}</button>}
    </section>
    <section className="pdf-paper-overview__quick-read" aria-label={copy.quickRead}>
      <h2>{copy.quickRead}</h2>
      <p className="pdf-paper-overview__unavailable" role="status">{copy.unavailable}</p>
      <p className="pdf-paper-overview__muted">{copy.note}</p>
      <button type="button" onClick={() => onOpenSource()}>{copy.paper} →</button>
    </section>
  </article>;
}
