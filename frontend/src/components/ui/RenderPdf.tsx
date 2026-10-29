import {PdfPaperGuide} from "./PdfPaperGuide";
import {PdfBilingualReader} from "./PdfBilingualReader";
import {PdfVisualStudy} from "./PdfVisualStudy";
import {PdfFigureLayer} from "./PdfFigureLayer";
import {usePaperReaderLabels,paperVisualLabel} from "./paperReaderLabels";
import "./PdfPaperReader.css";
import { useTranslation } from "react-i18next";
import { useEffect, useMemo, useRef, useState } from "react";
import { Document, Page, pdfjs } from "react-pdf";
import { Tooltip } from "antd";
import "react-pdf/dist/Page/AnnotationLayer.css";
import "react-pdf/dist/Page/TextLayer.css";
import { isSingleEnglishWord } from "@/modules/knowledge/api/translation";
import { extractPdfSelectionContext } from "./pdfSelectionContext";
import { isLearningActionCompatible, type LearningSelectionAction } from "./learningSelection";
import { referenceActionAtPoint, referenceActionForSelection, type PdfReferenceAction } from "./pdfReferenceGeometry";
import { usePdfStudyLayout } from "./usePdfStudyLayout";
import { PdfFormulaStudy } from "./PdfFormulaStudy";
import { PdfTableStudy } from "./PdfTableStudy";
import { PdfTableLayer, hasTableGeometry } from "./PdfTableLayer";
import { formulaNumberMap, formulaReferenceForClick } from "./pdfFormulaReferences";
import type { FormulaEvidence } from "@/modules/knowledge/api/formulaExplanation";
import type { PdfIRBlock, PdfIRDocument } from "@/modules/knowledge/api/pdfIR";
import { PdfFormulaLayer, hasFormulaGeometry } from "./PdfFormulaLayer";
import { usePdfIR, usablePdfIR, type PdfIRTarget } from "@/modules/knowledge/api/pdfIR";
import pdfWorkerUrl from "@react-pdf-worker/pdf.worker.min.mjs?url";
export type { LearningSelectionAction } from "./learningSelection";
export type { PdfReferenceAction } from "./pdfReferenceGeometry";

pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerUrl;

interface RenderPdfProps {
  onPdfIRDocumentChange?: (document: PdfIRDocument | undefined) => void;
  pdfNavigateTo?: {id: string; requestId: number};
  pdfIRTarget?: PdfIRTarget;
  pdfSourceHash?: string;
  onPreviewError?: () => void;
  fileData: string | ArrayBuffer | File | null;
  metadata?: Record<string, unknown> | null;
  content?: string | null;
  defaultPageWidth?: number;
  renderMode?: "canvas" | "custom" | "none";
  loadingText?: string;
  className?: string;
  style?: React.CSSProperties;
  gapBackground?: string;
  onAskSelection?: (selection: PdfTextSelection) => void;
  askSelectionLabel?: string;
  onTranslateSelection?: (selection: PdfTextSelection) => void;
  onRetranslateSelection?: (selection: PdfTextSelection) => void;
  retranslateSelectionLabel?: string;
  onAddVocabularySelection?: (selection: PdfTextSelection) => void;
  addVocabularySelectionLabel?: string;
  translateSelectionLabel?: string;
  translateSelectionDisabled?: boolean;
  translateSelectionDisabledTip?: string;
  translateSelectionConfigureLabel?: string;
  translateSelectionConfigureUrl?: string;
  learningSelectionActions?: LearningSelectionAction[];
  onLearningSelection?: (key:string, selection:PdfTextSelection)=>void;
  onPdfKindDetected?: (kind: "image_only" | "native_text" | "mixed") => void;
  onImportReferenceSelection?: (selection: PdfTextSelection) => void;
  referenceActions?: PdfReferenceAction[];
  viewPosition?: PdfViewPosition;
  onViewPositionChange?: (position: PdfViewPosition) => void;
}

export interface PdfViewPosition {
  page: number;
  progress: number;
}

export interface PdfTextSelection {
  text: string;
  page: number;
  context?: string;
  bbox?: [number, number, number, number];
  referenceId?: string;
}

interface SelectionAction {
  selection: PdfTextSelection;
  left: number;
  top: number;
  referenceAction?: PdfReferenceAction;
  referenceHover?: boolean;
}

const GAP = 20;
const BUFFER_PAGES = 3;
const GAP_BACKGROUND = "#DFE6EF";
const PAGE_BACKGROUND = "#ffffff";
const WIDTH_PT = 595;
const HEIGHT_PT = 842;

function hasUsablePdfText(value: string): boolean {
  const compact = Array.from(value).filter((character) => !/\s/u.test(character));
  if (compact.length < 10) return false;
  const suspicious = compact.filter((character) => !/[\x20-\x7E\u3000-\u303F\u3400-\u9FFF\uFF00-\uFFEF]/u.test(character)).length;
  return suspicious / compact.length < 0.08;
}

export default function RenderPdf({
  onPreviewError,
  onPdfIRDocumentChange,
  pdfNavigateTo,
  pdfIRTarget,
  pdfSourceHash,
  fileData,
  metadata,
  defaultPageWidth,
  renderMode = "canvas",
  loadingText = "正在加载PDF...",
  className,
  gapBackground = GAP_BACKGROUND,
  style,
  onAskSelection,
  askSelectionLabel = "向 LazyMind 提问",
  onTranslateSelection,
  onRetranslateSelection,
  retranslateSelectionLabel = "重译此段",
  onAddVocabularySelection,
  addVocabularySelectionLabel = "加入生词",
  translateSelectionLabel = "翻译",
  translateSelectionDisabled = false,
  translateSelectionDisabledTip,
  translateSelectionConfigureLabel = "去配置",
  translateSelectionConfigureUrl,
  learningSelectionActions = [],
  onLearningSelection,
  onPdfKindDetected,
  onImportReferenceSelection,
  referenceActions = [],
  viewPosition,
  onViewPositionChange,
}: RenderPdfProps) {
  const { t } = useTranslation();
  const {label:readerLabel}=usePaperReaderLabels();
  const [readerMode,setReaderMode]=useState<"original"|"bilingual"|"insight">("original");
  const [navigationOrigin,setNavigationOrigin]=useState<"bilingual"|"insight">();
  const [visualSelection,setVisualSelection]=useState<PdfIRBlock>();
  const lastRequestedNavigation=useRef("");
  const pdfIR = usePdfIR(pdfIRTarget);
  const [formulasEnabled, setFormulasEnabled] = useState(true);
  const formulaDocument = usablePdfIR(pdfIR.state, pdfSourceHash);
  useEffect(()=>{onPdfIRDocumentChange?.(formulaDocument);return()=>onPdfIRDocumentChange?.(undefined);},[formulaDocument,onPdfIRDocumentChange]);
  const imageBlocks=useMemo(()=>(formulaDocument?.blocks || []).filter(block=>block.type==="image"),[formulaDocument]);
  const studyReaderRef = useRef<HTMLDivElement>(null);
  const studyLayout = usePdfStudyLayout(studyReaderRef);
  const [studyOpen, setStudyOpen] = useState(false);
  const [tablesEnabled, setTablesEnabled] = useState(true);
  const [tableStudyOpen, setTableStudyOpen] = useState(false);
  const [tableSelection, setTableSelection] = useState<PdfIRBlock>();
  const tableBlocks = useMemo(() => (formulaDocument?.blocks || []).filter(block => block.type === "table" && block.table), [formulaDocument]);
  const tablePages = useMemo(() => {
    const pages = new Map<number, PdfIRBlock[]>();
    for (const block of tableBlocks) if (hasTableGeometry(block)) pages.set(block.page, [...(pages.get(block.page) || []), block]);
    return pages;
  }, [tableBlocks]);
  const [studySelection, setStudySelection] = useState<{block: PdfIRBlock; explain: boolean}>();
  const [studyHighlight, setStudyHighlight] = useState<FormulaEvidence>();
  const [studyReturn, setStudyReturn] = useState<PdfViewPosition>();
  const [pendingStudyNavigation, setPendingStudyNavigation] = useState<FormulaEvidence>();
  const studyPointer = useRef<{x:number;y:number}>();
  const studyBlocks = useMemo(() => (formulaDocument?.blocks || []).filter(block => block.type === "formula" && !!block.latex?.trim()), [formulaDocument]);
  const numberedFormulas = useMemo(() => formulaNumberMap(studyBlocks), [studyBlocks]);
  const studySourceKey = formulaDocument ? `${formulaDocument.source_sha256}:${formulaDocument.version}:${formulaDocument.projection_version || 0}:${formulaDocument.parser.name}:${formulaDocument.parser.model}` : "";
  useEffect(() => {
    setReaderMode("original");setNavigationOrigin(undefined);setVisualSelection(undefined);
    setStudyOpen(false); setTableStudyOpen(false); setTableSelection(undefined); setStudySelection(undefined); setStudyHighlight(undefined); setStudyReturn(undefined); setPendingStudyNavigation(undefined);
  }, [studySourceKey]);
  const explainFormula = (block: PdfIRBlock) => { setVisualSelection(undefined);setTableStudyOpen(false); setStudySelection({block,explain:true}); setStudyOpen(true); };
  const formulaPages = useMemo(() => {
    const pages = new Map<number, import("@/modules/knowledge/api/pdfIR").PdfIRBlock[]>();
    for (const block of formulaDocument?.blocks || []) {
      if (!hasFormulaGeometry(block)) continue;
      pages.set(block.page, [...(pages.get(block.page) || []), block]);
    }
    return pages;
  }, [formulaDocument]);
  const formulaCount = Array.from(formulaPages.values()).reduce((count, blocks) => count + blocks.length, 0);
  const formulaBusy = pdfIR.starting || pdfIR.state.status === "queued" || pdfIR.state.status === "running";
  const [numPages, setNumPages] = useState(1);
  const [pdfLoaded, setPdfLoaded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pageSizesMap, setPageSizesMap] = useState<
    Record<number, { width: number; height: number }>
  >({});
  const pageRefs = useRef<(HTMLDivElement | null)[]>([]);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const appliedPositionRef = useRef("");
  const [selectionAction, setSelectionAction] =
    useState<SelectionAction | null>(null);
  const [selectionHighlights, setSelectionHighlights] = useState<Array<{
    left: number;
    top: number;
    width: number;
    height: number;
  }>>([]);
  const referenceHoverTimer = useRef<number | undefined>(undefined);
  const referenceHoverCloseTimer = useRef<number | undefined>(undefined);

  const tagReferenceTextLayer = (pageIndex: number) => {
    const pageElement = pageRefs.current[pageIndex];
    if (!pageElement) return;
    const spans = Array.from(pageElement.querySelectorAll<HTMLElement>(".react-pdf__Page__textContent span"));
    spans.forEach((span) => delete span.dataset.referenceId);
    const byKey = new Map(referenceActions
      .filter((action) => action.referenceKey)
      .map((action) => [String(action.referenceKey), action]));
    if (!byKey.size) return;
    const marker = /^\s*(?:[-•]\s*)?(?:\[(\d{1,4})\]|(\d{1,4})[.)])\s*/;
    const firstMarker = spans.map((span) => span.textContent?.match(marker)).find(Boolean);
    if (!firstMarker) return;
    let currentKey = String(Number(firstMarker[1] || firstMarker[2]) - 1);
    for (const span of spans) {
      const match = span.textContent?.match(marker);
      if (match) currentKey = match[1] || match[2];
      const action = byKey.get(currentKey);
      if (action) span.dataset.referenceId = action.referenceId;
    }
  };

  const [viewportHeight, setViewportHeight] = useState(0);
  const [containerWidth, setContainerWidth] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);

  const pageWidthPx = useMemo(() => {
    if (defaultPageWidth) {
      return defaultPageWidth;
    }
    if (!containerWidth) {
      return 900;
    }
    return Math.floor(containerWidth * 0.94);
  }, [defaultPageWidth, containerWidth]);
  const [pageSlotHeight, setPageSlotHeight] = useState(0);

  const [pendingHighlight, setPendingHighlight] = useState(false);

  useEffect(() => {
    if (!selectionAction) setSelectionHighlights([]);
  }, [selectionAction]);

  const originBbox = useMemo(
    () => (metadata?.bbox as number[]) || [],
    [metadata?.bbox],
  );
  const pageIndex = useMemo(
    () => (metadata?.page as number) || 0,
    [metadata?.page],
  );

  useEffect(() => {
    if (originBbox.length === 4) {
      setPendingHighlight(true);
    }
  }, [originBbox, pageIndex]);

  useEffect(() => {
    const updateViewport = () => {
      if (!containerRef.current) {
        return;
      }
      const {clientWidth,clientHeight}=containerRef.current;
      // Hidden reading modes report zero; retain the rendered viewport so PDF.js
      // does not resize/cancel canvases and evict virtual pages while invisible.
      if(clientWidth<=0 || clientHeight<=0)return;
      setViewportHeight(clientHeight);
      setContainerWidth(clientWidth);
    };
    updateViewport();
    const resizeObserver = typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(updateViewport);
    if (containerRef.current) resizeObserver?.observe(containerRef.current);
    window.addEventListener("resize", updateViewport);
    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener("resize", updateViewport);
    };
  }, []);

  const onScroll = () => {
    if (!containerRef.current) {
      return;
    }
    setScrollTop(containerRef.current.scrollTop);
    setSelectionAction(null);
    if (onViewPositionChange && pageTops.length) {
      const currentTop = containerRef.current.scrollTop;
      let currentPage = 0;
      for (let index = 0; index < pageTops.length; index += 1) {
        if (pageTops[index] <= currentTop + 1) currentPage = index;
        else break;
      }
      const height = Math.max(pageHeightsPx[currentPage] || 1, 1);
      onViewPositionChange({
        page: currentPage + 1,
        progress: Math.min(1, Math.max(0, (currentTop - pageTops[currentPage]) / height)),
      });
    }
  };

  const updateSelectionAction = () => {
    // A hover timer may have been scheduled while the pointer was moving
    // during text selection. The explicit selection toolbar always wins.
    window.clearTimeout(referenceHoverTimer.current);
    window.clearTimeout(referenceHoverCloseTimer.current);
    if ((!onAskSelection && !onTranslateSelection && !onRetranslateSelection && !onImportReferenceSelection) || !containerRef.current) {
      return;
    }
    const selection = window.getSelection();
    const text = selection?.toString().trim() || "";
    if (!selection || selection.rangeCount === 0 || selection.isCollapsed || !text) {
      setSelectionAction(null);
      return;
    }
    const range = selection.getRangeAt(0);
    if (!containerRef.current.contains(range.commonAncestorContainer)) {
      setSelectionAction(null);
      return;
    }
    const selectionRect = range.getBoundingClientRect();
    const startElement = range.startContainer instanceof Element
      ? range.startContainer
      : range.startContainer.parentElement;
    const pageElement = startElement?.closest<HTMLElement>("[data-pdf-page-index]");
    const selectedPageIndex = Number(pageElement?.dataset.pdfPageIndex ?? 0);
    const pageRect = pageElement?.getBoundingClientRect();
    const pageSize = pageSizesMap[selectedPageIndex];
    let bbox: PdfTextSelection["bbox"];
    if (pageRect && pageSize && pageRect.width > 0 && pageRect.height > 0) {
      const scaleX = pageSize.width / pageRect.width;
      const scaleY = pageSize.height / pageRect.height;
      bbox = [
        Math.max(0, selectionRect.left - pageRect.left) * scaleX,
        Math.max(0, selectionRect.top - pageRect.top) * scaleY,
        Math.min(pageRect.width, selectionRect.right - pageRect.left) * scaleX,
        Math.min(pageRect.height, selectionRect.bottom - pageRect.top) * scaleY,
      ];
    }
    const containerRect = containerRef.current.getBoundingClientRect();
    setSelectionHighlights(Array.from(range.getClientRects())
      .filter((rect) => rect.width > 0 && rect.height > 0)
      .map((rect) => ({
        left: containerRef.current!.scrollLeft + rect.left - containerRect.left,
        top: containerRef.current!.scrollTop + rect.top - containerRect.top,
        width: rect.width,
        height: rect.height,
      })));
    setSelectionAction({
      selection: {
        text,
        page: selectedPageIndex + 1,
        context: extractPdfSelectionContext(pageElement?.innerText || "", text),
        bbox,
      },
      left: containerRef.current.scrollLeft + Math.min(
        Math.max(selectionRect.left - containerRect.left + selectionRect.width / 2, 72),
        containerRect.width - 72,
      ),
      top: containerRef.current.scrollTop
        + Math.max(selectionRect.top - containerRect.top - 44, 8),
      referenceAction: (() => {
        const tagged = Array.from(pageElement?.querySelectorAll<HTMLElement>("[data-reference-id]") || [])
          .find((span) => range.intersectsNode(span));
        return referenceActions.find((action) => action.referenceId === tagged?.dataset.referenceId)
          || referenceActionForSelection(referenceActions, selectedPageIndex + 1, bbox);
      })(),
    });
  };

  const handleReferenceHover = (event: React.MouseEvent<HTMLDivElement>) => {
    if (!onImportReferenceSelection || !referenceActions.length || !containerRef.current || window.getSelection()?.isCollapsed === false) return;
    if (selectionAction && !selectionAction.referenceHover) return;
    window.clearTimeout(referenceHoverCloseTimer.current);
    const eventElement = event.target instanceof HTMLElement ? event.target : null;
    if (eventElement?.closest("[data-pdf-selection-toolbar]")) {
      window.clearTimeout(referenceHoverTimer.current);
      return;
    }
    window.clearTimeout(referenceHoverTimer.current);
    const target = eventElement?.closest<HTMLElement>(".react-pdf__Page__textContent span") || null;
    if (!target) {
      if (selectionAction?.referenceHover) {
        referenceHoverCloseTimer.current = window.setTimeout(() => setSelectionAction(null), 250);
      }
      return;
    }
    referenceHoverTimer.current = window.setTimeout(() => {
      // Re-check at execution time: mouseup can create a selection after this
      // timer was scheduled, and a stale hover must not replace its toolbar.
      if (window.getSelection()?.isCollapsed === false) return;
      const pageElement = target.closest<HTMLElement>("[data-pdf-page-index]");
      if (!pageElement || !containerRef.current) return;
      const selectedPageIndex = Number(pageElement.dataset.pdfPageIndex || 0);
      const pageRect = pageElement.getBoundingClientRect();
      const pageSize = pageSizesMap[selectedPageIndex];
      if (!pageSize || pageRect.width <= 0 || pageRect.height <= 0) return;
      const taggedAction = referenceActions.find((item) => item.referenceId === target.dataset.referenceId);
      const action = taggedAction || referenceActionAtPoint(
        referenceActions,
        selectedPageIndex + 1,
        (event.clientX - pageRect.left) * pageSize.width / pageRect.width,
        (event.clientY - pageRect.top) * pageSize.height / pageRect.height,
      );
      if (!action) return;
      const rect = target.getBoundingClientRect();
      const containerRect = containerRef.current.getBoundingClientRect();
      setSelectionAction({
        selection: { text: action.rawText, page: selectedPageIndex + 1, referenceId: action.referenceId },
        left: containerRef.current.scrollLeft + Math.min(Math.max(rect.left - containerRect.left + rect.width / 2, 72), containerRect.width - 72),
        top: containerRef.current.scrollTop + Math.max(rect.top - containerRect.top - 44, 8),
        referenceAction: action,
        referenceHover: true,
      });
    }, 500);
  };

  const pageHeightsPx = useMemo(() => {
    const arr = [];
    for (let i = 0; i < numPages; i++) {
      const { width, height } = pageSizesMap[i] || {
        width: WIDTH_PT,
        height: HEIGHT_PT,
      };
      const pageHeightPx = (pageWidthPx * height) / width;
      arr.push(pageHeightPx);
    }
    return arr;
  }, [pageSizesMap, pageWidthPx, numPages]);

  const pageTops = useMemo(() => {
    const arr = [];
    let cumulativeTop = 0;
    for (let i = 0; i < numPages; i++) {
      arr.push(cumulativeTop);
      cumulativeTop += pageHeightsPx[i] + GAP;
    }
    return arr;
  }, [pageHeightsPx, numPages]);

  useEffect(() => {
    if (!viewPosition || !pdfLoaded || !containerRef.current || !pageTops.length) return;
    const page = Math.min(Math.max(viewPosition.page, 1), numPages) - 1;
    const progress = Math.min(1, Math.max(0, viewPosition.progress));
    const key = `${page}:${progress.toFixed(4)}:${Math.round(pageWidthPx)}`;
    if (appliedPositionRef.current === key) return;
    appliedPositionRef.current = key;
    const top = (pageTops[page] || 0) + (pageHeightsPx[page] || 0) * progress;
    containerRef.current.scrollTo({ top, behavior: "auto" });
    setScrollTop(top);
  }, [numPages, pageHeightsPx, pageTops, pageWidthPx, pdfLoaded, viewPosition]);

  useEffect(() => {
    const fallback = pageHeightsPx[0]
      ? pageHeightsPx[0] + GAP
      : Math.ceil((pageWidthPx * HEIGHT_PT) / WIDTH_PT) + GAP;
    setPageSlotHeight(fallback);
  }, [pageHeightsPx, pageWidthPx]);

  const visibleRange = useMemo(() => {
    if (!pageTops.length || !viewportHeight) {
      return { start: 0, end: 0 };
    }
    let start = 0;
    for (let i = 0; i < pageTops.length; i++) {
      const top = pageTops[i];
      const bottom = top + pageHeightsPx[i];
      if (bottom >= scrollTop) {
        start = Math.max(0, i - BUFFER_PAGES);
        break;
      }
    }
    let end = numPages - 1;
    const viewBottom = scrollTop + viewportHeight;
    for (let i = pageTops.length - 1; i >= 0; i--) {
      if (pageTops[i] <= viewBottom) {
        end = Math.min(numPages - 1, i + BUFFER_PAGES);
        break;
      }
    }
    return { start, end };
  }, [scrollTop, viewportHeight, pageTops, pageHeightsPx, numPages]);

  const visiblePages = useMemo(() => {
    const set = new Set<number>();
    if (pageSlotHeight && viewportHeight) {
      for (let i = visibleRange.start; i <= visibleRange.end; i++) {
        set.add(i);
      }
    }
    if (pageIndex >= 0 && pageIndex < numPages) {
      set.add(pageIndex);
    }
    return Array.from(set).sort((a, b) => a - b);
  }, [visibleRange, pageIndex, numPages, pageSlotHeight, viewportHeight]);

  const clearAllHighlights = () => {
    containerRef.current
      ?.querySelectorAll(".pdf-text-highlight-position-box")
      .forEach((el) => el.remove());
  };

  const highlightPositionTextFn = (
    dom: HTMLElement,
    bbox: number[],
    renderedW: number,
    renderedH: number,
    pdfW: number,
    pdfH: number,
  ) => {
    clearAllHighlights();
    if (!bbox || bbox.length !== 4) {
      return;
    }
    const [x0, y0, x1, y1] = bbox;
    const scaleX = renderedW / pdfW;
    const scaleY = renderedH / pdfH;
    const left = Math.ceil(x0 * scaleX);
    const top = Math.ceil(y0 * scaleY);
    const width = Math.ceil((x1 - x0) * scaleX);
    const height = Math.ceil((y1 - y0) * scaleY);
    const div = document.createElement("div");
    div.className = "pdf-text-highlight-position-box";
    div.style.position = "absolute";
    div.style.left = `${left}px`;
    div.style.top = `${top}px`;
    div.style.width = `${width}px`;
    div.style.height = `${height}px`;
    div.style.backgroundColor = "rgba(255, 255, 0, 0.4)";
    div.style.border = "2px solid rgba(255, 255, 0, 0.8)";
    div.style.pointerEvents = "none";
    div.style.zIndex = "10";
    dom.appendChild(div);
    div.scrollIntoView({
      behavior: "smooth",
      block: "center",
      inline: "nearest",
    });
  };

  useEffect(() => {
    if (!pdfLoaded || originBbox.length !== 4 || pageIndex < 0) {
      return;
    }
    if (!containerRef.current || !pageSlotHeight || !pendingHighlight) {
      return;
    }
    const targetScrollTop = pageTops[pageIndex] ?? pageIndex * pageSlotHeight;
    containerRef.current.scrollTo({ top: targetScrollTop, behavior: "auto" });
  }, [
    pendingHighlight,
    pdfLoaded,
    originBbox,
    pageIndex,
    pageSlotHeight,
    pageTops,
  ]);

  useEffect(() => {
    if (
      !pendingHighlight ||
      !pdfLoaded ||
      !originBbox.length ||
      pageIndex < 0
    ) {
      return;
    }
    if (!containerRef.current) {
      return;
    }
    const target = pageTops[pageIndex] ?? pageIndex * pageSlotHeight;
    containerRef.current.scrollTo({ top: target, behavior: "smooth" });

    let cancelled = false;
    const start = Date.now();
    const MAX_WAIT_MS = 3000;
    const tryDrawLoop = () => {
      if (cancelled) {
        return;
      }
      const ref = pageRefs.current[pageIndex];
      const size = pageSizesMap[pageIndex];
      if (ref && size) {
        const renderedH = Math.max(
          1,
          Math.round(
            pageHeightsPx[pageIndex] ||
              (pageWidthPx * (size.height || HEIGHT_PT)) /
                (size.width || WIDTH_PT),
          ),
        );
        highlightPositionTextFn(
          ref,
          originBbox,
          pageWidthPx,
          renderedH,
          size.width,
          size.height,
        );
        setPendingHighlight(false);
        return;
      }
      if (Date.now() - start < MAX_WAIT_MS) {
        requestAnimationFrame(tryDrawLoop);
      }
    };
    const raf = requestAnimationFrame(tryDrawLoop);
    return () => {
      cancelled = true;
      cancelAnimationFrame(raf);
    };
  }, [
    pendingHighlight,
    pdfLoaded,
    originBbox,
    pageIndex,
    pageTops,
    pageSlotHeight,
    pageSizesMap,
    pageHeightsPx,
    pageWidthPx,
  ]);

  const tryHighlightAfterRender = (index: number) => {
    if (!pendingHighlight || index !== pageIndex) {
      return;
    }
    const ref = pageRefs.current[index];
    const size = pageSizesMap[index];
    if (ref && size) {
      const renderedH = Math.max(
        1,
        Math.round(
          pageHeightsPx[index] ||
            (pageWidthPx * (size.height || HEIGHT_PT)) /
              (size.width || WIDTH_PT),
        ),
      );
      highlightPositionTextFn(
        ref,
        originBbox,
        pageWidthPx,
        renderedH,
        size.width,
        size.height,
      );
      setPendingHighlight(false);
    }
  };

  useEffect(() => {
    if (!pendingHighlight || !pdfLoaded) {
      return;
    }
    const size = pageSizesMap[pageIndex];
    const ref = pageRefs.current[pageIndex];
    if (ref && size) {
      const renderedH = Math.max(
        1,
        Math.round(
          pageHeightsPx[pageIndex] ||
            (pageWidthPx * (size.height || HEIGHT_PT)) /
              (size.width || WIDTH_PT),
        ),
      );
      highlightPositionTextFn(
        ref,
        originBbox,
        pageWidthPx,
        renderedH,
        size.width,
        size.height,
      );
      setPendingHighlight(false);
    }
  }, [
    pendingHighlight,
    pdfLoaded,
    pageIndex,
    originBbox,
    pageSizesMap,
    pageHeightsPx,
    pageWidthPx,
  ]);

  const renderNoDataFn = () => {
    return (
      <div
        style={{
          display: "flex",
          justifyContent: "center",
          alignItems: "center",
          height: "90vh",
          color: "#666",
          fontSize: "20px",
        }}
      >
        {loading ? loadingText : "没有可显示的PDF文件"}
      </div>
    );
  };

  const navigateStudyEvidence = (evidence: FormulaEvidence) => {
    if (!Number.isInteger(evidence.page) || evidence.page < 0 || evidence.page >= numPages) return;
    if (!studyReturn && containerRef.current) {
      const top = containerRef.current.scrollTop;
      let page = 0;
      for (let index = 0; index < pageTops.length; index++) if (pageTops[index] <= top) page = index;
      setStudyReturn({page:page+1,progress:Math.max(0,(top-pageTops[page])/(pageHeightsPx[page] || 1))});
    }
    setStudyHighlight(evidence);
    setPendingStudyNavigation(evidence);
  };
  useEffect(() => {
    if (!pendingStudyNavigation) return;
    const timer = window.setTimeout(() => {
      const page = pendingStudyNavigation.page;
      const y = pendingStudyNavigation.bbox?.[1] || 0;
      const top = Math.max(0,(pageTops[page] || 0) + y * (pageHeightsPx[page] || 0) - 64);
      containerRef.current?.scrollTo({top,behavior:"auto"});
      setScrollTop(top);
      if (pageSizesMap[page]) setPendingStudyNavigation(undefined);
    }, 100);
    return () => window.clearTimeout(timer);
  }, [pendingStudyNavigation,pageTops,pageHeightsPx,pageSizesMap]);
  const openTableStudy = (block: PdfIRBlock) => {
    setVisualSelection(undefined);setStudyOpen(false); setTableStudyOpen(true); setTableSelection(block);
    navigateStudyEvidence({id:block.id,text:block.table?.captions.join("\n") || "",page:block.page,bbox:block.bbox});
  };
  const closeStudy = () => {
    setVisualSelection(undefined);setStudyOpen(false);setTableStudyOpen(false);setStudyHighlight(undefined);setPendingStudyNavigation(undefined);setStudyReturn(undefined);
  };
  const returnFromStudy = () => {
    if (!studyReturn) return;
    const page=studyReturn.page-1;
    const top=(pageTops[page] || 0) + studyReturn.progress * (pageHeightsPx[page] || 0);
    containerRef.current?.scrollTo({top,behavior:"auto"}); setScrollTop(top);
    setStudyHighlight(undefined); setPendingStudyNavigation(undefined); setStudyReturn(undefined);
  };

  const navigateSourceBlock = (id:string) => {
    const block=formulaDocument?.blocks.find(item=>item.id===id);if(!block)return;
    if(readerMode!=="original")setNavigationOrigin(readerMode);
    setReaderMode("original");
    navigateStudyEvidence({id:block.id,text:block.text || block.latex || "",page:block.page,bbox:block.bbox});
  };
  const openVisualStudy = (block:PdfIRBlock) => {
    setStudyOpen(false);setTableStudyOpen(false);setVisualSelection(block);navigateSourceBlock(block.id);
  };
  const openRelatedStudy = (block:PdfIRBlock) => {
    if(block.type==="formula"){navigateSourceBlock(block.id);explainFormula(block);}
    else openVisualStudy(block);
  };
  const returnReaderMode = () => {
    if(navigationOrigin){setReaderMode(navigationOrigin);setNavigationOrigin(undefined);closeStudy();}
    else returnFromStudy();
  };
  useEffect(()=>{
    if(!pdfNavigateTo || !pdfLoaded || !formulaDocument)return;
    const request=`${pdfNavigateTo.requestId}:${pdfNavigateTo.id}`;
    if(lastRequestedNavigation.current===request || !formulaDocument.blocks.some(block=>block.id===pdfNavigateTo.id))return;
    lastRequestedNavigation.current=request;navigateSourceBlock(pdfNavigateTo.id);
  // Navigation events are keyed by request ID; source/readiness may arrive later.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  },[pdfNavigateTo?.id,pdfNavigateTo?.requestId,pdfLoaded,formulaDocument]);

  const renderVirtualPage = (index: number) => {
    const { width, height } = pageSizesMap[index] || {
      width: WIDTH_PT,
      height: HEIGHT_PT,
    };
    const pageHeightPx =
      pageHeightsPx[index] || Math.ceil((pageWidthPx * height) / width);
    const top = pageTops[index] ?? index * pageSlotHeight;

    return (
      <div
        key={`page_${index + 1}`}
        data-pdf-page-index={index}
        onPointerDownCapture={event => { studyPointer.current = {x:event.clientX,y:event.clientY}; }}
        onClickCapture={event => {
          if (!formulasEnabled || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          const block=formulaReferenceForClick(event.currentTarget,numberedFormulas,event.target as Element,studyPointer.current,event.clientX,event.clientY,window.getSelection()?.toString() || "");
          if (block) {
            event.preventDefault(); event.stopPropagation();
            setVisualSelection(undefined);setTableStudyOpen(false);setStudyOpen(true);setStudySelection({block,explain:false});
            navigateStudyEvidence({id:block.id,page:block.page,text:block.latex || "",bbox:block.bbox});
          }
        }}
        ref={(el) => (pageRefs.current[index] = el)}
        style={{
          position: "absolute",
          top,
          left: "50%",
          transform: "translateX(-50%)",
          width: pageWidthPx,
          height: pageHeightPx,
          background: PAGE_BACKGROUND,
          boxShadow: "0 2px 6px rgba(0,0,0,0.08)",
          overflow: "hidden",
        }}
      >
        <Page
          pageNumber={index + 1}
          width={pageWidthPx}
          renderTextLayer={Boolean(pdfIRTarget || onAskSelection || onTranslateSelection || onImportReferenceSelection)}
          renderAnnotationLayer
          onRenderSuccess={() => {
            tryHighlightAfterRender(index);
            window.requestAnimationFrame(() => tagReferenceTextLayer(index));
          }}
          onRenderTextLayerSuccess={() => tagReferenceTextLayer(index)}
          onLoadSuccess={(page) => {
            const viewport = page.getViewport({ scale: 1 });
            setPageSizesMap((prev) => ({
              ...prev,
              [index]: {
                width: viewport.width,
                height: viewport.height,
              },
            }));
          }}
        />
        {studyHighlight?.page === index && <div className="pdf-study-highlight" style={(() => {
          const box = studyHighlight.bbox;
          const valid = box && box.length === 4 && box.every(value=>Number.isFinite(value)&&value>=0&&value<=1) && box[0]<box[2] && box[1]<box[3];
          return valid ? {left:`${box[0]*100}%`,top:`${box[1]*100}%`,width:`${(box[2]-box[0])*100}%`,height:`${(box[3]-box[1])*100}%`} : {inset:3,background:"transparent"};
        })()} />}
        {formulasEnabled && formulaDocument && formulaPages.has(index) && <PdfFormulaLayer key={`${formulaDocument.source_sha256}:${formulaDocument.version}`} blocks={formulaPages.get(index)!} onExplain={explainFormula} />}
        {formulaDocument && <PdfFigureLayer blocks={imageBlocks.filter(block=>block.page===index)} allBlocks={formulaDocument.blocks} onSelect={openVisualStudy}/> }
        {tablesEnabled && formulaDocument && tablePages.has(index) && <PdfTableLayer key={`tables:${studySourceKey}`} blocks={tablePages.get(index)!} onSelect={openTableStudy} />}
        <div
          style={{
            position: "absolute",
            bottom: 6,
            right: 10,
            padding: "2px 8px",
            fontSize: 12,
            background: "rgba(0,0,0,0.45)",
            color: "#fff",
            borderRadius: 12,
            lineHeight: 1.2,
            pointerEvents: "none",
            fontFamily: "system-ui, sans-serif",
          }}
        >
          {index + 1}/{numPages}
        </div>
      </div>
    );
  };

  if (error) {
    return (
      <div style={{ padding: 20, textAlign: "center", color: "red" }}>
        <p>加载失败: {error}</p>
      </div>
    );
  }

  const totalHeight =
    (pageHeightsPx.reduce((s, h) => s + h, 0) || pageSlotHeight * numPages) +
    GAP * Math.max(0, numPages - 1);

  const translationUnavailable=translateSelectionDisabled&&!isSingleEnglishWord(selectionAction?.selection.text||"");
  return (
    <div className="pdf-reader-workspace" style={{height:style?.height || "calc(100vh - 220px)",width:"100%"}}>
    {pdfIRTarget&&<nav className="pdf-reader-modebar" aria-label={readerLabel("reading")}><div role="tablist"><button role="tab" aria-selected={readerMode!=="insight"} onClick={()=>setReaderMode("original")}>{readerLabel("reading")}</button><button role="tab" aria-selected={readerMode==="insight"} onClick={()=>setReaderMode("insight")}>{readerLabel("insight")}</button></div>{readerMode!=="insight"&&<div className="pdf-reader-view-toggle"><button aria-pressed={readerMode==="original"} onClick={()=>setReaderMode("original")}>{readerLabel("original")}</button><button aria-pressed={readerMode==="bilingual"} onClick={()=>setReaderMode("bilingual")}>{readerLabel("bilingual")}</button></div>}{navigationOrigin&&<button onClick={returnReaderMode}>{readerLabel(navigationOrigin==="insight"?"returnGuide":"returnBilingual")}</button>}</nav>}
    <div className="pdf-reader-stage">
    {pdfIRTarget&&formulaDocument&&<><div className="pdf-reader-reflow" hidden={readerMode!=="insight"}><PdfPaperGuide target={pdfIRTarget} document={formulaDocument} active={readerMode==="insight"} onNavigate={navigateSourceBlock} onStudy={openRelatedStudy}/></div><div className="pdf-reader-reflow" hidden={readerMode!=="bilingual"}><PdfBilingualReader target={pdfIRTarget} document={formulaDocument} active={readerMode==="bilingual"} onNavigate={navigateSourceBlock} onStudy={openRelatedStudy}/></div></>}
    {pdfIRTarget&&!formulaDocument&&readerMode!=="original"&&<div className="pdf-reader-empty">{readerLabel("needsIR")}<button onClick={()=>setReaderMode("original")}>{readerLabel("original")}</button></div>}
    <div ref={studyReaderRef} data-study-layout={studyLayout} className="pdf-reader-with-study" style={{height:"100%",width:"100%",display:readerMode==="original"?"flex":"none"}}>
    <div
      className={className}
      ref={containerRef}
      onScroll={onScroll}
      onMouseUp={updateSelectionAction}
      onMouseMove={handleReferenceHover}
      onMouseLeave={() => {
        window.clearTimeout(referenceHoverTimer.current);
        if (selectionAction?.referenceHover) {
          referenceHoverCloseTimer.current = window.setTimeout(() => setSelectionAction(null), 250);
        }
      }}
      style={{
        overflow: "auto",
        position: "relative",
        backgroundColor: gapBackground,
        ...style,
        flex: "1 1 0", minWidth: 0, minHeight: 0, height: "100%",
      }}
    >
      {pdfIRTarget && <div className="pdf-formula-controls" onMouseUp={event => event.stopPropagation()}>
        <span role="status">{formulaBusy ? t("pdfFormula.processing") : pdfIR.state.status === "ready" ? !globalThis.crypto?.subtle ? t("pdfFormula.insecure") : !pdfSourceHash ? t("pdfFormula.checking") : formulaDocument ? formulaCount ? t("pdfFormula.count", {count:formulaCount}) : t("pdfFormula.empty") : t("pdfFormula.mismatch") : pdfIR.state.error_message || t("pdfFormula.intro")}</span>
        {formulaDocument && studyBlocks.length > 0 && <button onClick={() => {setVisualSelection(undefined);setTableStudyOpen(false);setStudyOpen(true);}}>{t("pdfFormula.openCatalog")}</button>}
        {tableBlocks.length > 0 && <button onClick={() => {setVisualSelection(undefined);setStudyOpen(false);setTableStudyOpen(true);}}>{t("pdfTable.openCatalog", {count:tableBlocks.length})}</button>}
        {imageBlocks.length>0&&<select aria-label={readerLabel("visuals")} value="" onChange={event=>{const block=imageBlocks.find(item=>item.id===event.target.value);if(block)openVisualStudy(block);}}><option value="" disabled>{readerLabel("imageRegion")} ({imageBlocks.length})</option>{imageBlocks.filter(block=>block.image?.number).map(block=><option key={block.id} value={block.id}>{paperVisualLabel(block,formulaDocument!.blocks,readerLabel)}</option>)}{imageBlocks.some(block=>!block.image?.number)&&<optgroup label={readerLabel("imageRegion")}>{imageBlocks.filter(block=>!block.image?.number).map(block=><option key={block.id} value={block.id}>{paperVisualLabel(block,formulaDocument!.blocks,readerLabel)}</option>)}</optgroup>}</select>}
        {tableStudyOpen&&tableSelection&&<button onClick={()=>openVisualStudy(tableSelection)}>{readerLabel("explainVisual")}</button>}
        <details className="pdf-reader-display-settings"><summary>{readerLabel("displaySettings")}</summary><div>
        {formulaDocument && formulaCount > 0 && <button onClick={() => setFormulasEnabled(value => !value)} aria-pressed={formulasEnabled}>{formulasEnabled ? t("pdfFormula.hide") : t("pdfFormula.show")}</button>}
        {tableBlocks.length > 0 && <button onClick={() => setTablesEnabled(value => !value)} aria-pressed={tablesEnabled}>{t(tablesEnabled ? "pdfTable.hide" : "pdfTable.show")}</button>}
        {!formulaBusy && pdfIRTarget.canWrite !== false && <button onClick={() => void pdfIR.start(pdfIR.state.status === "ready" || pdfIR.state.status === "failed")}>{pdfIR.state.status === "not_requested" ? t("pdfFormula.enable") : t("pdfFormula.retry")}</button>}
        </div></details>
      </div>}
      {selectionHighlights.map((highlight, index) => (
        <div
          key={`${highlight.left}-${highlight.top}-${index}`}
          data-pdf-selection-highlight
          style={{
            position: "absolute",
            zIndex: 20,
            left: highlight.left,
            top: highlight.top,
            width: highlight.width,
            height: highlight.height,
            background: "rgba(22, 119, 255, 0.28)",
            borderRadius: 1,
            pointerEvents: "none",
          }}
        />
      ))}
      {selectionAction ? (
        <div
          data-pdf-selection-toolbar
          onMouseDown={(event) => event.stopPropagation()}
          onMouseUp={(event) => event.stopPropagation()}
          onMouseEnter={() => window.clearTimeout(referenceHoverCloseTimer.current)}
          onMouseLeave={() => {
            if (selectionAction.referenceHover) {
              referenceHoverCloseTimer.current = window.setTimeout(() => setSelectionAction(null), 250);
            }
          }}
          style={{
            position: "absolute",
            zIndex: 30,
            left: selectionAction.left,
            top: selectionAction.top,
            transform: "translateX(-50%)",
            borderRadius: 8,
            padding: 4,
            background: "#fff",
            boxShadow: "0 4px 16px rgba(0, 0, 0, 0.2)",
            fontSize: 13,
            whiteSpace: "nowrap",
            display: "flex",
            gap: 4,
          }}
        >
          {onAskSelection && !selectionAction.referenceHover ? (
            <button
              type="button"
              aria-label={askSelectionLabel}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                onAskSelection(selectionAction.selection);
                window.getSelection()?.removeAllRanges();
                setSelectionAction(null);
              }}
              style={{ border: 0, borderRadius: 6, padding: "6px 10px", background: "#1677ff", color: "#fff", cursor: "pointer" }}
            >
              {askSelectionLabel}
            </button>
          ) : null}
          {onTranslateSelection && !selectionAction.referenceHover ? (
            <Tooltip
              mouseEnterDelay={0}
              title={translationUnavailable ? (
                <span>
                  {translateSelectionDisabledTip}
                  {translateSelectionConfigureUrl ? (
                    <>
                      {" · "}
                      <a
                        href={translateSelectionConfigureUrl}
                        onMouseDown={(event) => event.stopPropagation()}
                      >
                        {translateSelectionConfigureLabel}
                      </a>
                    </>
                  ) : null}
                </span>
              ) : undefined}
            >
              <span style={{ display: "inline-flex" }}>
                <button
                  type="button"
                  aria-label={translateSelectionLabel}
                  disabled={translationUnavailable}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => {
                    onTranslateSelection(selectionAction.selection);
                    window.getSelection()?.removeAllRanges();
                    setSelectionAction(null);
                  }}
                  style={{
                    border: "1px solid #d9d9d9",
                    borderRadius: 6,
                    padding: "5px 10px",
                    background: translationUnavailable ? "#f5f5f5" : "#fff",
                    color: translationUnavailable ? "rgba(0,0,0,.25)" : "#1677ff",
                    cursor: translationUnavailable ? "not-allowed" : "pointer",
                    pointerEvents: translationUnavailable ? "none" : "auto",
                  }}
                >
                  {translateSelectionLabel}
                </button>
              </span>
            </Tooltip>
          ) : null}
          {onRetranslateSelection && !selectionAction.referenceHover ? (
            <button
              type="button"
              aria-label={retranslateSelectionLabel}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                onRetranslateSelection(selectionAction.selection);
                window.getSelection()?.removeAllRanges();
                setSelectionAction(null);
              }}
              style={{ border: "1px solid #d9d9d9", borderRadius: 6, padding: "5px 10px", background: "#fff", color: "#1677ff", cursor: "pointer" }}
            >
              {retranslateSelectionLabel}
            </button>
          ) : null}
          {onImportReferenceSelection && selectionAction.referenceAction ? (
            <button
              type="button"
              aria-label={selectionAction.referenceAction.kind === "open" ? "打开知识库文档" : selectionAction.referenceAction.kind === "external" ? "打开链接" : "下载并加入知识库"}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                if ((selectionAction.referenceAction?.kind === "open" || selectionAction.referenceAction?.kind === "external") && selectionAction.referenceAction.href) {
                  if (selectionAction.referenceAction.kind === "external") window.open(selectionAction.referenceAction.href, "_blank", "noopener,noreferrer");
                  else window.location.assign(selectionAction.referenceAction.href);
                } else {
                  onImportReferenceSelection({
                    ...selectionAction.selection,
                    text: selectionAction.referenceAction?.rawText || selectionAction.selection.text,
                    referenceId: selectionAction.referenceAction?.referenceId,
                  });
                }
                window.getSelection()?.removeAllRanges();
                setSelectionAction(null);
              }}
              style={{ border: "1px solid #d9d9d9", borderRadius: 6, padding: "5px 10px", background: "#fff", color: "#1677ff", cursor: "pointer" }}
            >
              {selectionAction.referenceAction.kind === "open" ? "打开知识库文档" : selectionAction.referenceAction.kind === "external" ? "打开链接" : "下载并加入知识库"}
            </button>
          ) : null}
          {onAddVocabularySelection && !selectionAction.referenceHover && isSingleEnglishWord(selectionAction.selection.text) ? (
            <button
              type="button"
              aria-label={addVocabularySelectionLabel}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                onAddVocabularySelection(selectionAction.selection);
                window.getSelection()?.removeAllRanges();
                setSelectionAction(null);
              }}
              style={{ border: "1px solid #d9d9d9", borderRadius: 6, padding: "5px 10px", background: "#fff", color: "#1677ff", cursor: "pointer" }}
            >
              {addVocabularySelectionLabel}
            </button>
          ) : null}
          {!selectionAction.referenceHover && learningSelectionActions.filter((action) => isLearningActionCompatible(action, selectionAction.selection.text)).map((action) => (
            <Tooltip key={action.key} title={action.disabled ? action.disabledTip : undefined}>
              <span><button type="button" aria-label={action.label} disabled={action.disabled} onMouseDown={(event)=>event.preventDefault()} onClick={() => { onLearningSelection?.(action.key,selectionAction.selection); window.getSelection()?.removeAllRanges(); setSelectionAction(null); }} style={{border:"1px solid #d9d9d9",borderRadius:6,padding:"5px 10px",background:action.disabled?"#f5f5f5":"#fff",color:action.disabled?"rgba(0,0,0,.25)":"#1677ff",cursor:action.disabled?"not-allowed":"pointer"}}>{action.label}</button></span>
            </Tooltip>
          ))}
        </div>
      ) : null}
      <Document
        file={fileData}
        renderMode={renderMode}
        loading={renderNoDataFn()}
        noData={renderNoDataFn()}
        onLoadSuccess={(doc) => {
          setNumPages(doc.numPages);
          if (onPdfKindDetected) {
            void (async () => {
              let pagesWithText = 0;
              const sampledPages = Math.min(doc.numPages, 5);
              for (let pageNo = 1; pageNo <= sampledPages; pageNo++) {
                const page = await doc.getPage(pageNo);
                const text = await page.getTextContent();
                const value = text.items.map((item) => "str" in item ? item.str : "").join(" ");
                if (hasUsablePdfText(value)) pagesWithText++;
              }
              onPdfKindDetected(pagesWithText === 0 ? "image_only" : pagesWithText === sampledPages ? "native_text" : "mixed");
            })().catch(() => undefined);
          }
          doc.getPage(1).then((firstPage) => {
            const view = (
              firstPage as unknown as {
                view?: [number, number, number, number];
              }
            ).view;
            setPageSizesMap((prev) => ({
              ...prev,
              0: {
                width: Math.ceil(view ? view[2] : WIDTH_PT),
                height: Math.ceil(view ? view[3] : HEIGHT_PT),
              },
            }));
          });
          setPdfLoaded(true);
          setLoading(false);
          setError(null);
        }}
        onLoadError={(err) => {
          onPreviewError?.();
          setError(err.message || "加载失败");
          setLoading(false);
        }}
      >
        <div
          style={{
            position: "relative",
            height: totalHeight,
            minWidth: pageWidthPx + 32,
            background: gapBackground,
          }}
        >
          {visiblePages.map((i) => renderVirtualPage(i))}
        </div>
      </Document>
    </div>
    {studyOpen && formulaDocument && pdfIRTarget && <PdfFormulaStudy key={studySourceKey} target={pdfIRTarget} sourceKey={studySourceKey} blocks={studyBlocks} selected={studySelection?.block} autoExplain={studySelection?.explain} onSelect={block=>{setStudySelection({block,explain:false});navigateStudyEvidence({id:block.id,text:block.latex || "",page:block.page,bbox:block.bbox});}} onNavigate={navigateStudyEvidence} canReturn={!!studyReturn} onReturn={returnFromStudy} onClose={closeStudy} />}
    {tableStudyOpen && formulaDocument && <PdfTableStudy key={`tables:${studySourceKey}`} sourceKey={studySourceKey} blocks={tableBlocks} allBlocks={formulaDocument.blocks} selected={tableSelection} onSelect={openTableStudy} onNavigate={navigateStudyEvidence} canReturn={!!studyReturn} onReturn={returnFromStudy} onClose={closeStudy} />}
    {visualSelection&&formulaDocument&&pdfIRTarget&&<PdfVisualStudy key={`${studySourceKey}:${visualSelection.id}`} target={pdfIRTarget} document={formulaDocument} block={visualSelection} onNavigate={navigateSourceBlock} onInspectTable={openTableStudy} onClose={closeStudy} onReturn={returnReaderMode} canReturn={!!navigationOrigin || !!studyReturn}/>}
    </div>
    </div>
    </div>
  );
}
