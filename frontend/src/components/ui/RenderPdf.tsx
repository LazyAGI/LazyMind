import type { PdfScrollSyncEndpoint } from "./pdfScrollSync";
import { createPdfViewportScheduler } from "./pdfViewportScheduler";
import ReadingPreparationAction from "@/modules/knowledge/paper/ReadingPreparationAction";
import {PdfPageControl} from "./PdfPageControl";
import {PdfZoomControl} from "./PdfZoomControl";
import {PdfPinnedRegion} from "./PdfPinnedRegion";
import {PdfPinDock} from "./PdfPinDock";
import {usePdfPinBoard} from "./usePdfPinBoard";
import {isPersistablePdfPin,type PdfPinRecord,type PdfRegionPinRecord,type PdfStudyPinRecord} from "./pdfPinStore";
import {usePdfHandPan} from "./usePdfHandPan";
import {PdfStudyActivation,StudySparkle} from "./PdfStudyActivation";
import {usePdfReaderPreferences} from "./usePdfReaderPreferences";
import {AgentAppsAuth} from "@/components/auth";
import {authServiceApiUrl} from "@/runtime/apiBase";
import {usePaperReaderSession,type VisualExplanation} from "@/modules/knowledge/api/paperReader";
import {PdfVisualStudy} from "./PdfVisualStudy";
import {PdfFigureLayer} from "./PdfFigureLayer";
import {usePaperReaderLabels,paperVisualLabel} from "./paperReaderLabels";
import "./PdfPaperReader.css";
import { useTranslation } from "react-i18next";
import { message } from "antd";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Document, Page, pdfjs } from "react-pdf";
import { PdfCitationLayer } from "./PdfCitationLayer";
import { restorePdfLinkAnnotationBorders } from "./pdfNativeLinkBorders";
import { groupPdfCitationTargets, type PdfCitationTarget } from "./pdfCitationReferences";
import { createPdfCitationPreviewCache } from "./pdfCitationPreview";
import { addCitationToPaperLibrary } from "@/modules/knowledge/api/academicReferences";
import { getImportErrorMessage } from "@/modules/knowledge/utils/importError";
import {PdfSelectionComposer, type PdfComposerAction} from "./PdfSelectionComposer";
import {pdfNoteHighlightBackground,pdfNoteHighlightHitAreas,pdfNoteHighlightPaths,readPdfNoteHighlights,writePdfNoteHighlights,type PdfNoteColor,type PdfNoteHighlight} from "./pdfNoteHighlights";
import {ControlOutlined, MinusOutlined, PlusOutlined, LeftOutlined, RightOutlined} from "@ant-design/icons";
import "react-pdf/dist/Page/AnnotationLayer.css";
import "react-pdf/dist/Page/TextLayer.css";
import { isSingleEnglishWord } from "@/modules/knowledge/api/translation";
import { extractPdfSelectionContext } from "./pdfSelectionContext";
import { visibleLearningSelectionActions, type LearningSelectionAction } from "./learningSelection";
import { normalizePdfSelectionText } from "./pdfSelectionText";
import { referenceActionAtPoint, referenceActionForSelection, type PdfReferenceAction } from "./pdfReferenceGeometry";
import { usePdfStudyLayout } from "./usePdfStudyLayout";
import { PdfFormulaStudy } from "./PdfFormulaStudy";
import {PdfStudyDialog,type PinnedGeometry} from "./PdfStudyDialog";
import {cropPdfBlockPreview} from './pdfBlockPreview';
import { PdfTableStudy } from "./PdfTableStudy";
import { PdfTableLayer, hasTableGeometry } from "./PdfTableLayer";
import { formulaNumberMap, formulaReferenceForClick } from "./pdfFormulaReferences";
import type { FormulaEvidence,FormulaExplanation } from "@/modules/knowledge/api/formulaExplanation";
import type { PdfIRBlock, PdfIRDocument } from "@/modules/knowledge/api/pdfIR";
import { PdfFormulaLayer, hasFormulaGeometry } from "./PdfFormulaLayer";
import { usePdfIR, usablePdfIR, type PdfIRTarget } from "@/modules/knowledge/api/pdfIR";
import {readNativePdfOutline,type PdfNativeOutlineItem} from "@/modules/knowledge/api/pdfOutline";
import pdfWorkerUrl from "@react-pdf-worker/pdf.worker.min.mjs?url";
export type { LearningSelectionAction } from "./learningSelection";
export type { PdfReferenceAction } from "./pdfReferenceGeometry";
type LoadedPdfDocument = Parameters<NonNullable<React.ComponentProps<typeof Document>["onLoadSuccess"]>>[0];

pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerUrl;

interface RenderPdfProps {
  pdfScrollSync?: PdfScrollSyncEndpoint;
  paperMode?: boolean;
  pdfToolbarMode?: "full" | "navigation";
  pdfSelectableText?: boolean;
  pdfHeaderStart?: React.ReactNode;
  pdfHeaderEnd?: React.ReactNode | ((tools: React.ReactNode) => React.ReactNode);
  onStudyOpenChange?: (open: boolean) => void;
  onPdfIRDocumentChange?: (document: PdfIRDocument | undefined) => void;
  onPdfOutlineChange?: (outline: PdfNativeOutlineItem[]) => void;
  pdfNavigateTo?: {id: string; requestId: number};
  pdfNavigateToPage?: {page:number;requestId:number};
  pdfNavigateToPosition?: {page:number;progress:number;requestId:number};
  savedNoteHighlights?: PdfNoteHighlight[];
  onNoteHighlightClick?: (id:string) => void;
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
  onNoteSelection?: (selection: PdfTextSelection, note: string, color: PdfNoteColor, id: string) => Promise<void>;
  askSelectionLabel?: string;
  onTranslateSelection?: (selection: PdfTextSelection) => void;
  onRetranslateSelection?: (selection: PdfTextSelection) => void;
  retranslateSelectionLabel?: string;
  onAddVocabularySelection?: (selection: PdfTextSelection) => void;
  addVocabularySelectionLabel?: string;
  translateSelectionLabel?: string;
  translateSelectionDisabled?: boolean;
  translateSelectionDisabledTip?: string;
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
  question?: string;
  text: string;
  page: number;
  context?: string;
  contextTranslation?: string;
  bbox?: [number, number, number, number];
  rects?: Array<[number,number,number,number]>;
  referenceId?: string;
}

interface SelectionAction {
  selection: PdfTextSelection;
  left: number;
  top: number;
  highlightRects?: Array<[number,number,number,number]>;
  referenceAction?: PdfReferenceAction;
  referenceHover?: boolean;
}

const GAP = 20;
const BUFFER_PAGES = 3;
const GAP_BACKGROUND = "#ffffff";
const PAGE_BACKGROUND = "#ffffff";
// A fixed raster size prevents PDF.js clearing canvases during sidebar animation.
const RASTER_WIDTH = 1200;
// The toolbar's 100% is a comfortable reading size, adapted to the visible pane.
const PDF_READING_BASE_SCALE = 1.2;
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
  paperMode = false,
  pdfToolbarMode = "full",
  pdfSelectableText = false,
  pdfScrollSync,
  pdfHeaderStart,
  pdfHeaderEnd,
  onStudyOpenChange,
  onPdfIRDocumentChange,
  onPdfOutlineChange,
  pdfNavigateTo,
  pdfNavigateToPage,
  pdfNavigateToPosition,
  savedNoteHighlights = [],
  onNoteHighlightClick,
  pdfIRTarget,
  pdfSourceHash,
  fileData,
  metadata,
  defaultPageWidth,
  renderMode = "canvas",
  loadingText = "正在加载PDF...",
  className,
  style,
  onAskSelection,
  onNoteSelection,
  onTranslateSelection,
  onAddVocabularySelection,
  addVocabularySelectionLabel = "加入生词",
  translateSelectionDisabled = false,
  translateSelectionDisabledTip,
  learningSelectionActions = [],
  onLearningSelection,
  onPdfKindDetected,
  onImportReferenceSelection,
  referenceActions = [],
  viewPosition,
  onViewPositionChange,
}: RenderPdfProps) {
  const { t } = useTranslation();
  const {label:readerLabel,language:readerLanguage}=usePaperReaderLabels();
  const preferenceKey = pdfSourceHash && pdfIRTarget ? `pdf-reader:${pdfIRTarget?.dataset || ""}:${pdfIRTarget?.document || ""}:${pdfSourceHash}` : undefined;
  const {zoom,setZoom} = usePdfReaderPreferences(preferenceKey,fileData,pdfIRTarget ? `${pdfIRTarget.dataset}:${pdfIRTarget.document}` : undefined);
  const readerSession = usePaperReaderSession();
  const readerAccount = AgentAppsAuth.getUserInfo();
  // Never restore legacy unscoped highlights: their account owner is unknown.
  // Keep credentials out of storage keys, while separating users, tenants and servers.
  const noteHighlightKey = readerAccount?.userId && pdfIRTarget && pdfSourceHash
    ? `pdf-note-highlights:v2:${JSON.stringify([authServiceApiUrl("auth/refresh"),readerAccount.userId,readerAccount.tenantId || readerAccount.tenant_id || readerAccount.tenantKey || readerAccount.tenant_key || "",pdfIRTarget.dataset,pdfIRTarget.document,pdfSourceHash])}` : undefined;
  const pinBoardKey = readerAccount?.userId && pdfIRTarget && pdfSourceHash
    ? `pdf-pin-board:v1:${JSON.stringify([authServiceApiUrl("auth/refresh"),readerAccount.userId,readerAccount.tenantId || readerAccount.tenant_id || readerAccount.tenantKey || readerAccount.tenant_key || "",pdfIRTarget.dataset,pdfIRTarget.document,pdfSourceHash])}` : undefined;
  const pinBoard=usePdfPinBoard(pinBoardKey,fileData);
  const pinBoardReady=pinBoard.ready&&(!pdfIRTarget||!!pinBoardKey);
  useEffect(()=>{
    if(pinBoard.saveError)message.warning(readerLanguage==='zh'?'钉住内容暂时无法保存到本机，请检查浏览器存储空间。':'Pinned content could not be saved on this device. Check browser storage.');
  },[pinBoard.saveError,readerLanguage]);
  const [noteHighlightState,setNoteHighlightState] = useState<{key?:string;session:string;highlights:PdfNoteHighlight[]}>({session:readerSession,highlights:[]});
  const noteHighlights = noteHighlightState.key === noteHighlightKey && noteHighlightState.session === readerSession ? noteHighlightState.highlights : [];
  useEffect(()=>setNoteHighlightState({key:noteHighlightKey,session:readerSession,highlights:readPdfNoteHighlights(noteHighlightKey)}),[noteHighlightKey,readerSession,fileData]);
  const [noteReturn,setNoteReturn]=useState<PdfViewPosition>();
  const [pinCaptureActive,setPinCaptureActive] = useState(false);
  const pinStageRef=useRef<HTMLDivElement>(null);
  useEffect(()=>setNoteReturn(undefined),[fileData]);
  useEffect(()=>setPinCaptureActive(false),[fileData]);
  // PDF.js may transfer its input buffer to a worker. Retain original bytes for download.
  const pdfFile = useMemo(()=>fileData instanceof ArrayBuffer ? fileData.slice(0) : fileData,[fileData]);
  const currentSource = useRef(fileData);
  currentSource.current = fileData;
  const [visualSelection,setVisualSelection]=useState<PdfIRBlock>();
  const readerFrame=useRef<HTMLDivElement>(null);
  const [studyPulse,setStudyPulse]=useState(0);
  const [visualPreview,setVisualPreview]=useState<string>();
  const lastRequestedNavigation=useRef("");
  const pdfIR = usePdfIR(pdfIRTarget);
  const [formulasEnabled, setFormulasEnabled] = useState(true);
  const formulaDocument = usablePdfIR(pdfIR.state, pdfSourceHash);
  useEffect(()=>{onPdfIRDocumentChange?.(formulaDocument);return()=>onPdfIRDocumentChange?.(undefined);},[formulaDocument,onPdfIRDocumentChange]);
  const imageBlocks=useMemo(()=>(formulaDocument?.blocks || []).filter(block=>block.type==="image"),[formulaDocument]);
  const studyReaderRef = useRef<HTMLDivElement>(null);
  const studyLayout = usePdfStudyLayout(studyReaderRef);
  const [studyOpen, setStudyOpen] = useState(false);
  const [studyPinned, setStudyPinned] = useState(false);
  const [activeStudyPinId,setActiveStudyPinId]=useState<string>();
  const restoredStudyPinId=useRef<string>();
  const studyExplanationRef=useRef<{kind:'formula'|'image'|'table';blockId:string;data:FormulaExplanation|VisualExplanation}>();
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
    studyExplanationRef.current=undefined;
    setVisualSelection(undefined);
    setStudyOpen(false); setStudyPinned(false); setActiveStudyPinId(undefined); setTableStudyOpen(false); setTableSelection(undefined); setStudySelection(undefined); setStudyHighlight(undefined); setStudyReturn(undefined); setPendingStudyNavigation(undefined);
  }, [studySourceKey]);
  const releasePinnedStudy=()=>{
    if(!activeStudyPinId)return;
    pinBoard.update(activeStudyPinId,{minimized:true});
    setActiveStudyPinId(undefined);setStudyPinned(false);
    restoredStudyPinId.current=undefined;
    studyExplanationRef.current=undefined;
  };
  const explainFormula = (block: PdfIRBlock) => { releasePinnedStudy();setVisualSelection(undefined);setTableStudyOpen(false); setStudySelection({block,explain:true}); setStudyOpen(true); };
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
  const [toolsOpen,setToolsOpen] = useState(false);
  const toolsRef = useRef<HTMLDivElement>(null);
  useEffect(()=>{
    if(!toolsOpen)return;
    const close=(event:PointerEvent)=>{if(event.target instanceof Node && !toolsRef.current?.contains(event.target))setToolsOpen(false);};
    document.addEventListener("pointerdown",close);
    return()=>document.removeEventListener("pointerdown",close);
  },[toolsOpen]);
  const [numPages, setNumPages] = useState(1);
  const [loadedSource,setLoadedSource] = useState<{fileData:RenderPdfProps["fileData"]}>();
  const pdfLoaded = !!loadedSource && loadedSource.fileData === fileData;
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const errorSource = useRef<RenderPdfProps["fileData"]>();
  const [pageSizesMap, setPageSizesMap] = useState<
    Record<number, { width: number; height: number }>
  >({});
  const pageRefs = useRef<(HTMLDivElement | null)[]>([]);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const handPan=usePdfHandPan(containerRef,fileData);
  const appliedPositionRef = useRef("");
  const positionIdleTimer=useRef<ReturnType<typeof setTimeout>>();
  const pendingPosition=useRef<PdfViewPosition>();
  const reportedPage=useRef(viewPosition?.page||1);
  const reportPosition=useRef(onViewPositionChange);
  reportPosition.current=onViewPositionChange;
  useEffect(()=>()=>{clearTimeout(positionIdleTimer.current);if(pendingPosition.current)reportPosition.current?.(pendingPosition.current);},[]);
  const [selectionAction, setSelectionAction] =
    useState<SelectionAction | null>(null);
  const [selectionComposerMode,setSelectionComposerMode]=useState<'ask'|'note'>('ask');
  const [selectionHighlights, setSelectionHighlights] = useState<Array<{
    left: number;
    top: number;
    width: number;
    height: number;
  }>>([]);
  const referenceHoverTimer = useRef<number | undefined>(undefined);
  const referenceHoverCloseTimer = useRef<number | undefined>(undefined);
  const citationTargets = useMemo(() => groupPdfCitationTargets(referenceActions), [referenceActions]);
  const citationDatasetId = pdfIRTarget?.dataset;
  const canAddCitationToLibrary = Boolean(citationDatasetId && pdfIRTarget?.document && pdfIRTarget.canWrite !== false);
  const addCitation = useCallback(async (target: PdfCitationTarget) => {
    if (!citationDatasetId || !canAddCitationToLibrary) return;
    const key = `citation-library:${citationDatasetId}:${target.referenceId}`;
    message.open({ key, type: "loading", content: t("pdfCitation.addStarted"), duration: 0 });
    try {
      const result = await addCitationToPaperLibrary(citationDatasetId, target.referenceId);
      message.open({ key, type: "success", content: t(result.status === "pending" ? "pdfCitation.addSucceeded" : "pdfCitation.addedToLibrary"), duration: 3 });
    } catch (error) {
      message.open({ key, type: "error", content: getImportErrorMessage(error, t("pdfCitation.addFailed")), duration: 5 });
      throw error;
    }
  }, [citationDatasetId, canAddCitationToLibrary, t]);
  const [citationPreview, setCitationPreview] = useState<{source: RenderPdfProps["fileData"]; cache: ReturnType<typeof createPdfCitationPreviewCache>}>();
  useEffect(() => {
    const cache = createPdfCitationPreviewCache();
    setCitationPreview({source:fileData,cache});
    return () => cache.dispose();
  }, [fileData]);

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

  const zoomPosition = useRef<PdfViewPosition>();
  const pdfDocument = useRef<{source: RenderPdfProps["fileData"]; document: LoadedPdfDocument}>();
  const getCitationDocument = useCallback(() => pdfDocument.current?.source === fileData ? pdfDocument.current.document : undefined, [fileData]);
  const lastPageNavigation = useRef("");
  useEffect(() => {
    lastPageNavigation.current = "";
    clearTimeout(positionIdleTimer.current);
    pendingPosition.current=undefined;
    reportedPage.current=viewPosition?.page||1;
    setScrollTop(0);
    appliedPositionRef.current = "";
  }, [fileData]);
  useEffect(() => { onStudyOpenChange?.(studyOpen || tableStudyOpen || !!visualSelection); }, [studyOpen, tableStudyOpen, visualSelection, onStudyOpenChange]);
  const [viewportHeight, setViewportHeight] = useState(0);
  const [containerWidth, setContainerWidth] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);

  const pageWidthPx = useMemo(() => {
    const baseScale = paperMode ? PDF_READING_BASE_SCALE : 1;
    const fitDefaultPaper = (requested:number) => paperMode && containerWidth > 0
      ? Math.min(requested, Math.max(1, containerWidth - 20)) : requested;
    if (defaultPageWidth) {
      return Math.round(fitDefaultPaper(defaultPageWidth * baseScale) * zoom);
    }
    if (!containerWidth) {
      return Math.round(900 * baseScale * zoom);
    }
    return Math.round(fitDefaultPaper(Math.min(960, Math.max(1, containerWidth - 32), Math.floor(containerWidth * 0.94)) * baseScale) * zoom);
  }, [defaultPageWidth, containerWidth, zoom, paperMode]);
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
    const viewportUpdates = createPdfViewportScheduler(updateViewport);
    const resizeObserver = typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(viewportUpdates.schedule);
    if (containerRef.current) resizeObserver?.observe(containerRef.current);
    window.addEventListener("resize", viewportUpdates.schedule);
    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener("resize", viewportUpdates.schedule);
      viewportUpdates.cancel();
    };
  }, []);

  useEffect(() => {
    if (pdfLoaded && containerRef.current) return pdfScrollSync?.connect(containerRef.current);
  }, [pdfScrollSync, pdfLoaded]);

  const onScroll = () => {
    pdfScrollSync?.onScroll();
    if (!containerRef.current) {
      return;
    }
    setScrollTop(containerRef.current.scrollTop);
    setSelectionAction(null);
    if (reportPosition.current && pageTops.length) {
      const currentTop = containerRef.current.scrollTop;
      let currentPage = 0;
      for (let index = 0; index < pageTops.length; index += 1) {
        if (pageTops[index] <= currentTop + 1) currentPage = index;
        else break;
      }
      const height = Math.max(pageHeightsPx[currentPage] || 1, 1);
      const position={
        page: currentPage + 1,
        progress: Math.min(1, Math.max(0, (currentTop - pageTops[currentPage]) / height)),
      };
      pendingPosition.current=position;
      clearTimeout(positionIdleTimer.current);
      const publish=()=>{const latest=pendingPosition.current;if(!latest)return;pendingPosition.current=undefined;reportPosition.current?.(latest);};
      if(position.page!==reportedPage.current){reportedPage.current=position.page;publish();}
      else positionIdleTimer.current=setTimeout(publish,250);
    }
  };

  const updateSelectionAction = () => {
    if (document.activeElement?.closest("[data-pdf-selection-toolbar]")) return;
    // A hover timer may have been scheduled while the pointer was moving
    // during text selection. The explicit selection toolbar always wins.
    window.clearTimeout(referenceHoverTimer.current);
    window.clearTimeout(referenceHoverCloseTimer.current);
    if ((!onAskSelection && !onNoteSelection && !onTranslateSelection && !onImportReferenceSelection && !onAddVocabularySelection && !onLearningSelection) || !containerRef.current) {
      return;
    }
    const selection = window.getSelection();
    const rawText = selection?.toString().trim() || "";
    const text = normalizePdfSelectionText(rawText);
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
    const highlightRects:SelectionAction['highlightRects']=pageRect?.width && pageRect.height ? Array.from(range.getClientRects())
      .filter(rect=>rect.width>0&&rect.height>0&&rect.right>pageRect.left&&rect.left<pageRect.right&&rect.bottom>pageRect.top&&rect.top<pageRect.bottom)
      .slice(0,50)
      .map(rect=>{
        const clamp=(value:number)=>Math.max(0,Math.min(1,value));
        return [clamp((rect.left-pageRect.left)/pageRect.width),clamp((rect.top-pageRect.top)/pageRect.height),clamp((rect.right-pageRect.left)/pageRect.width),clamp((rect.bottom-pageRect.top)/pageRect.height)];
      }).filter(rect=>rect[0]<rect[2]&&rect[1]<rect[3]) as Array<[number,number,number,number]> : [];
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
        context: normalizePdfSelectionText(extractPdfSelectionContext(pageElement?.innerText || "", rawText)),
        bbox,
        rects:highlightRects,
      },
      left: containerRef.current.scrollLeft + Math.min(
        Math.max(selectionRect.left - containerRect.left + selectionRect.width / 2, 72),
        containerRect.width - 72,
      ),
      top: containerRef.current.scrollTop
        + Math.max(selectionRect.top - containerRect.top - 44, 8),
      highlightRects,
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
      if (!action || action.kind === "download") return;
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
      const { width, height } = pageSizesMap[i] || pageSizesMap[0] || {
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
    let cumulativeTop = handPan.gutterY;
    for (let i = 0; i < numPages; i++) {
      arr.push(cumulativeTop);
      cumulativeTop += pageHeightsPx[i] + GAP;
    }
    return arr;
  }, [pageHeightsPx, numPages, handPan.gutterY]);

  const [nativeDestination,setNativeDestination] = useState<{source:RenderPdfProps["fileData"];pageNumber:number;dest?:unknown[];bbox?:[number,number,number,number]}>();
  const [resolvedDestination,setResolvedDestination] = useState<{source:RenderPdfProps["fileData"];page:number;progress:number}>();
  const navigateCitation = useCallback((target:PdfCitationTarget) => {
    const first = target.regions[0];
    const pageNumber = first?.page ?? target.fallbackPage;
    if (pageNumber !== undefined) setNativeDestination({source:fileData,pageNumber,bbox:first?.bbox});
  }, [fileData]);
  useEffect(()=>{
    const request=nativeDestination;
    const loaded=pdfDocument.current;
    if(!request || request.source!==fileData || loaded?.source!==fileData || !pdfLoaded) return;
    const page=request.pageNumber;
    if(!Number.isInteger(page)||page<1||page>numPages)return;
    let cancelled=false;
    void loaded.document.getPage(page).then(pdfPage=>{
      if(cancelled || currentSource.current!==request.source)return;
      const viewport=pdfPage.getViewport({scale:1});
      const dest=request.dest;
      const kind=(dest?.[1] as {name?:string}|undefined)?.name;
      const y=kind==='XYZ'?dest?.[3]:kind==='FitH'||kind==='FitBH'?dest?.[2]:kind==='FitR'?dest?.[5]:undefined;
      const x=kind==='XYZ'||kind==='FitR'?dest?.[2]:0;
      const point=typeof y==='number'&&Number.isFinite(y)?viewport.convertToViewportPoint(typeof x==='number'?x:0,y):[0,0];
      let top = point[1];
      if (request.bbox) {
        // Parsed bibliography boxes use unrotated top-left coordinates. Convert
        // through PDF space so rotated and cropped pages land at the same spot.
        const base = pdfPage.getViewport({scale:1,rotation:0});
        const [left,upper,right,lower] = request.bbox;
        top = Math.min(...[[left,upper],[right,upper],[right,lower],[left,lower]].map(([x,y]) => {
          const pdfPoint = base.convertToPdfPoint(x,y);
          return viewport.convertToViewportPoint(pdfPoint[0],pdfPoint[1])[1];
        }));
      }
      const progress=Math.max(0,Math.min(.999,top/viewport.height));
      setPageSizesMap(previous=>({...previous,[page-1]:{width:viewport.width,height:viewport.height}}));
      setResolvedDestination({source:request.source,page,progress});
    }).catch(()=>{
      if(!cancelled && currentSource.current===request.source)setResolvedDestination({source:request.source,page,progress:0});
    });
    return()=>{cancelled=true;};
  },[nativeDestination,fileData,pdfLoaded,numPages]);


  const currentPage = Math.max(1, pageTops.reduce((page, top, index) => top <= scrollTop + 1 ? index + 1 : page, 1));
  const jumpPage = (value: number) => {
    if (!pdfLoaded || !Number.isFinite(value)) return;
    const page = Math.min(numPages, Math.max(1, Math.round(value)));
    const top = pageTops[page - 1] || 0;
    containerRef.current?.scrollTo({top, behavior:"auto"});
    setScrollTop(top);
    onViewPositionChange?.({page,progress:0});
  };
  useEffect(() => {
    if (!pdfNavigateToPage || !Number.isFinite(pdfNavigateToPage.page) || !pdfLoaded || pdfDocument.current?.source !== fileData) return;
    const key = `${pdfNavigateToPage.requestId}:${pdfNavigateToPage.page}`;
    if (lastPageNavigation.current === key) return;
    lastPageNavigation.current = key;
    jumpPage(pdfNavigateToPage.page);
  }, [pdfNavigateToPage?.page,pdfNavigateToPage?.requestId,pdfLoaded,pageTops,numPages,fileData]);
  const lastNoteNavigation=useRef('');
  useEffect(()=>{
    const request=pdfNavigateToPosition;
    if(!request||!pdfLoaded||pdfDocument.current?.source!==fileData||!pageTops.length)return;
    if(!Number.isInteger(request.page)||request.page<1||request.page>numPages||!Number.isFinite(request.progress))return;
    const key=`${request.requestId}:${request.page}:${request.progress}`;
    if(lastNoteNavigation.current===key)return;
    lastNoteNavigation.current=key;
    let originPage=0;
    for(let index=0;index<pageTops.length;index++)if(pageTops[index]<=scrollTop+1)originPage=index;else break;
    setNoteReturn({page:originPage+1,progress:Math.max(0,Math.min(1,(scrollTop-(pageTops[originPage]||0))/(pageHeightsPx[originPage]||1)))});
    const page=request.page,progress=Math.max(0,Math.min(.999,request.progress));
    const top=(pageTops[page-1]||0)+(pageHeightsPx[page-1]||0)*progress;
    containerRef.current?.scrollTo({top,behavior:'auto'});setScrollTop(top);setSelectionAction(null);
    onViewPositionChange?.({page,progress});
  },[pdfNavigateToPosition,pdfLoaded,fileData,numPages,pageTops,pageHeightsPx,scrollTop,onViewPositionChange]);
  const changeZoom = (value: number) => {
    zoomPosition.current = {page:currentPage,progress:Math.max(0,(scrollTop-(pageTops[currentPage-1]||0))/(pageHeightsPx[currentPage-1]||1))};
    setZoom(Math.min(2, Math.max(.5, Math.round(value * 100) / 100)));
  };
  const previousGeometry = useRef({width:pageWidthPx,tops:pageTops,heights:pageHeightsPx});
  useLayoutEffect(() => {
    const previous = previousGeometry.current;
    previousGeometry.current = {width:pageWidthPx,tops:pageTops,heights:pageHeightsPx};
    if (previous.width === pageWidthPx && previous.heights.length === pageHeightsPx.length && previous.heights.every((height,index)=>height===pageHeightsPx[index]) && !zoomPosition.current) return;
    const previousPage = previous.tops.reduce((page, top, index)=>top<=scrollTop+1?index:page,0);
    const position = zoomPosition.current || {page:previousPage+1,progress:Math.max(0,(scrollTop-(previous.tops[previousPage]||0))/(previous.heights[previousPage]||1))};
    zoomPosition.current = undefined;
    const top = (pageTops[position.page-1]||0) + (pageHeightsPx[position.page-1]||0)*position.progress;
    containerRef.current?.scrollTo({top,behavior:"auto"});
    setScrollTop(top);
  }, [pageWidthPx,pageTops,pageHeightsPx]);
  useLayoutEffect(()=>{
    if(!resolvedDestination || resolvedDestination.source!==fileData)return;
    const {page,progress}=resolvedDestination;
    const top=(pageTops[page-1]||0)+(pageHeightsPx[page-1]||0)*progress;
    containerRef.current?.scrollTo({top,behavior:"auto"});
    setScrollTop(top);
    setSelectionAction(null);
    onViewPositionChange?.({page,progress});
    setResolvedDestination(undefined);
    setNativeDestination(undefined);
  },[resolvedDestination,fileData,pageTops,pageHeightsPx,onViewPositionChange]);
  useEffect(() => {
    if (!viewPosition || !pdfLoaded || !containerRef.current || !pageTops.length) return;
    const page = Math.min(Math.max(viewPosition.page, 1), numPages) - 1;
    const progress = Math.min(1, Math.max(0, viewPosition.progress));
    const key = `${page}:${progress.toFixed(4)}`;
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
    // Render an offscreen study source for its preview without scrolling to it.
    if(visualSelection && visualSelection.page>=0 && visualSelection.page<numPages)set.add(visualSelection.page);
    return Array.from(set).sort((a, b) => a - b);
  }, [visibleRange, pageIndex, numPages, pageSlotHeight, viewportHeight,visualSelection?.page]);
  useEffect(()=>{
    if(!visualSelection){setVisualPreview(undefined);return;}
    const preview=cropPdfBlockPreview(pageRefs.current[visualSelection.page],visualSelection.bbox);
    if(preview)setVisualPreview(preview);
  },[visualSelection,visiblePages]);

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
  const navigateFromStudy = (evidence:FormulaEvidence) => {
    navigateStudyEvidence(evidence);
    if(!studyPinned){setVisualSelection(undefined);setStudyOpen(false);setTableStudyOpen(false);}
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
    releasePinnedStudy();
    setVisualSelection(undefined);setStudyOpen(false); setTableStudyOpen(true); setTableSelection(block);
    setPendingStudyNavigation(undefined);setStudyHighlight(undefined);setStudyReturn(undefined);
  };
  const currentStudyPin = ():Omit<PdfStudyPinRecord,'id'|'geometry'|'minimized'>|undefined => {
    const kind=studyOpen?'formula':tableStudyOpen?'table':visualSelection?.type==='table'?'table':visualSelection?'image':undefined;
    const block=kind==='formula'?(studySelection?.block||studyBlocks[0]):kind==='table'?(visualSelection?.type==='table'?visualSelection:tableSelection||tableBlocks[0]):visualSelection;
    if(!kind||!block)return;
    const name=readerLanguage==='zh'?{formula:'公式',table:'表格',image:'图片'}[kind]:{formula:'Formula',table:'Table',image:'Figure'}[kind];
    return {kind,blockId:block.id,page:block.page+1,title:readerLanguage==='zh'?`${name} · 第 ${block.page+1} 页`:`${name} · Page ${block.page+1}`,...(visualSelection?.type==='table'&&kind==='table'?{view:'visual' as const}:{})};
  };
  const showStudyPin=useCallback((record:PdfStudyPinRecord)=>{
    const block=formulaDocument?.blocks.find(item=>item.id===record.blockId&&item.type===record.kind);
    if(!block)return false;
    restoredStudyPinId.current=record.id;
    studyExplanationRef.current=record.explanation?{kind:record.kind,blockId:record.blockId,data:record.explanation}:undefined;
    setActiveStudyPinId(record.id);setStudyPinned(true);
    setStudyOpen(record.kind==='formula');setTableStudyOpen(record.kind==='table'&&record.view!=='visual');
    setStudySelection(record.kind==='formula'?{block,explain:!record.explanation}:undefined);
    setTableSelection(record.kind==='table'&&record.view!=='visual'?block:undefined);
    setVisualPreview(record.sourcePreview);
    setVisualSelection(record.kind==='image'||record.view==='visual'?block:undefined);
    setPendingStudyNavigation(undefined);setStudyHighlight(undefined);setStudyReturn(undefined);
    return true;
  },[formulaDocument]);
  const rememberStudyExplanation=(kind:'formula'|'table'|'image',blockId:string,data:FormulaExplanation|VisualExplanation)=>{
    studyExplanationRef.current={kind,blockId,data};
    if(!activeStudyPinId)return;
    const record=pinBoard.records.find(item=>item.id===activeStudyPinId);
    if(record&&record.kind===kind&&record.blockId===blockId&&record.explanation!==data&&isPersistablePdfPin({...record,explanation:data})){
      pinBoard.update(activeStudyPinId,{explanation:data});
    }
  };
  const handleStudyGeometryChange=useCallback((geometry:PinnedGeometry)=>{
    if(!activeStudyPinId)return;
    pinBoard.change(current=>{
      const stored=current.find(item=>item.id===activeStudyPinId);
      if(!stored||stored.geometry.x===geometry.x&&stored.geometry.y===geometry.y&&stored.geometry.width===geometry.width&&stored.geometry.height===geometry.height)return;
      return current.map(item=>item.id===activeStudyPinId?{...item,geometry}:item);
    });
  },[activeStudyPinId,pinBoard.change]);
  const handleStudyPinChange=(pinned:boolean,geometry?:PinnedGeometry)=>{
    if(!pinned){
      if(activeStudyPinId)pinBoard.remove(activeStudyPinId);
      setActiveStudyPinId(undefined);setStudyPinned(false);
      return;
    }
    if(!pinBoardReady){message.info(readerLanguage==='zh'?'正在恢复钉住内容，请稍后再试。':'Restoring pinned items. Try again shortly.');return false;}
    const snapshot=currentStudyPin();
    if(!snapshot||!geometry)return false;
    const matching=pinBoard.records.find((item):item is PdfStudyPinRecord=>item.kind!=='region'&&item.kind===snapshot.kind&&item.blockId===snapshot.blockId&&item.view===snapshot.view);
    const id=activeStudyPinId||matching?.id||crypto.randomUUID();
    const explanation=studyExplanationRef.current?.kind===snapshot.kind&&studyExplanationRef.current.blockId===snapshot.blockId?studyExplanationRef.current.data:matching?.explanation;
    const sourcePreview=visualSelection?visualPreview||matching?.sourcePreview:undefined;
    const base:PdfStudyPinRecord={...snapshot,id,geometry,minimized:false,...(explanation?{explanation}:{})};
    // An unusually large crop must not prevent the smaller study window itself
    // from being saved; the PDF can render its source preview again on restore.
    const withPreview=sourcePreview?{...base,sourcePreview}:base;
    const record:PdfStudyPinRecord=isPersistablePdfPin(withPreview)?withPreview:base;
    if(!isPersistablePdfPin(record)){message.warning(readerLanguage==='zh'?'钉住内容过大，无法保存到本机。':'This pin is too large to save on this device.');return false;}
    const saved=pinBoard.change(current=>{
      if(!current.some(item=>item.id===id)&&current.length>=6)return;
      const next=current.some(item=>item.id===id)?current.map(item=>item.id===id?record:item):[...current,record];
      return next.map(item=>item.kind!=='region'&&item.id!==id?{...item,minimized:true}:item);
    });
    if(!saved){message.warning(readerLanguage==='zh'?'最多钉住 6 项，请先关闭一项。':'You can pin up to 6 items. Close one first.');return false;}
    setActiveStudyPinId(id);setStudyPinned(true);
  };
  const minimizeStudy=(geometry:PinnedGeometry)=>{
    if(activeStudyPinId)pinBoard.update(activeStudyPinId,{geometry,minimized:true});
    setActiveStudyPinId(undefined);setStudyPinned(false);
    setVisualSelection(undefined);setStudyOpen(false);setTableStudyOpen(false);
    setStudyHighlight(undefined);setPendingStudyNavigation(undefined);setStudyReturn(undefined);
  };
  const restorePin=(id:string)=>{
    const record=pinBoard.records.find(item=>item.id===id);
    if(!record)return;
    if(record.kind==='region'){pinBoard.update(id,{minimized:false});return;}
    if(!showStudyPin(record)){
      message.warning(readerLanguage==='zh'?'原文结构尚未就绪，稍后再恢复。':'The paper structure is not ready yet. Try again shortly.');
      return;
    }
    pinBoard.change(current=>current.map(item=>item.kind!=='region'&&item.id===activeStudyPinId?{...item,minimized:true}:item.id===id?{...item,minimized:false}:item));
  };
  const closeStudy = () => {
    if(activeStudyPinId)pinBoard.remove(activeStudyPinId);
    restoredStudyPinId.current=undefined;
    setActiveStudyPinId(undefined);
    setVisualSelection(undefined);setStudyOpen(false);setStudyPinned(false);setTableStudyOpen(false);setStudyHighlight(undefined);setPendingStudyNavigation(undefined);setStudyReturn(undefined);
  };
  const returnFromStudy = () => {
    if (!studyReturn) return;
    const page=studyReturn.page-1;
    const top=(pageTops[page] || 0) + studyReturn.progress * (pageHeightsPx[page] || 0);
    containerRef.current?.scrollTo({top,behavior:"auto"}); setScrollTop(top);
    setStudyHighlight(undefined); setPendingStudyNavigation(undefined); setStudyReturn(undefined);
    if(!studyPinned){setVisualSelection(undefined); setStudyOpen(false); setTableStudyOpen(false);}
  };

  const navigateSourceBlock = (id:string) => {
    const block=formulaDocument?.blocks.find(item=>item.id===id);if(!block)return;
    navigateStudyEvidence({id:block.id,text:block.text || block.latex || "",page:block.page,bbox:block.bbox});
  };
  const openVisualStudy = (block:PdfIRBlock) => {
    releasePinnedStudy();
    studyExplanationRef.current=undefined;
    setStudyOpen(false);setTableStudyOpen(false);setVisualPreview(undefined);setVisualSelection(block);
    setPendingStudyNavigation(undefined);setStudyHighlight(undefined);setStudyReturn(undefined);
  };
  const currentStudySnapshot=currentStudyPin();
  const activeStoredStudyPin=activeStudyPinId?pinBoard.records.find((item):item is PdfStudyPinRecord=>item.kind!=='region'&&item.id===activeStudyPinId):undefined;
  const hasRestoredVisualSnapshot=!!activeStudyPinId&&!!visualSelection&&restoredStudyPinId.current===activeStudyPinId&&activeStoredStudyPin?.blockId===visualSelection.id&&!!activeStoredStudyPin?.explanation;
  useEffect(()=>{
    if(!activeStudyPinId||!visualSelection||!visualPreview)return;
    const stored=pinBoard.records.find(item=>item.id===activeStudyPinId);
    if(!stored||stored.kind==='region'||stored.blockId!==visualSelection.id||stored.sourcePreview===visualPreview)return;
    const next={...stored,sourcePreview:visualPreview};
    if(isPersistablePdfPin(next))pinBoard.update(activeStudyPinId,{sourcePreview:visualPreview});
  },[activeStudyPinId,visualSelection?.id,visualPreview,pinBoard.records,pinBoard.update]);
  useEffect(()=>{
    if(!activeStudyPinId||!currentStudySnapshot)return;
    const stored=pinBoard.records.find(item=>item.id===activeStudyPinId);
    if(!stored||stored.kind==='region'||stored.kind===currentStudySnapshot.kind&&stored.blockId===currentStudySnapshot.blockId&&stored.view===currentStudySnapshot.view&&stored.title===currentStudySnapshot.title)return;
    studyExplanationRef.current=undefined;
    pinBoard.change(current=>current.map(item=>item.id===activeStudyPinId?{...item,...currentStudySnapshot,explanation:undefined,sourcePreview:undefined} as PdfPinRecord:item));
  },[activeStudyPinId,currentStudySnapshot?.kind,currentStudySnapshot?.blockId,currentStudySnapshot?.view,currentStudySnapshot?.title,pinBoard.records,pinBoard.change]);
  useEffect(()=>{
    if(!formulaDocument||activeStudyPinId||studyOpen||tableStudyOpen||visualSelection)return;
    const pending=pinBoard.records.find((item):item is PdfStudyPinRecord=>item.kind!=='region'&&!item.minimized);
    if(pending)showStudyPin(pending);
  },[formulaDocument,activeStudyPinId,studyOpen,tableStudyOpen,visualSelection,pinBoard.records,showStudyPin]);
  useEffect(()=>{
    if(!pdfNavigateTo || !pdfLoaded || !formulaDocument)return;
    const request=`${pdfNavigateTo.requestId}:${pdfNavigateTo.id}`;
    if(lastRequestedNavigation.current===request || !formulaDocument.blocks.some(block=>block.id===pdfNavigateTo.id))return;
    lastRequestedNavigation.current=request;navigateSourceBlock(pdfNavigateTo.id);
  // Navigation events are keyed by request ID; source/readiness may arrive later.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  },[pdfNavigateTo?.id,pdfNavigateTo?.requestId,pdfLoaded,formulaDocument]);

  const renderVirtualPage = (index: number) => {
    const { width, height } = pageSizesMap[index] || pageSizesMap[0] || {
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
            releasePinnedStudy();
            setVisualSelection(undefined);setTableStudyOpen(false);setStudyOpen(true);setStudySelection({block,explain:false});
            navigateStudyEvidence({id:block.id,page:block.page,text:block.latex || "",bbox:block.bbox});
          }
        }}
        onClick={event => {
          if (!onNoteHighlightClick || event.button !== 0 || event.detail > 1 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          if ((event.target as Element).closest('a,button,input,textarea,select,[role="link"]') || window.getSelection()?.isCollapsed === false) return;
          const pointer = studyPointer.current;
          if (!pointer || Math.hypot(event.clientX - pointer.x, event.clientY - pointer.y) > 4) return;
          const bounds = event.currentTarget.getBoundingClientRect();
          if (!bounds.width || !bounds.height) return;
          const x = (event.clientX - bounds.left) / bounds.width;
          const y = (event.clientY - bounds.top) / bounds.height;
          const hit = pdfNoteHighlightHitAreas(savedNoteHighlights,index+1).find(({rect}) => x >= rect[0] && x <= rect[2] && y >= rect[1] && y <= rect[3]);
          if (hit) onNoteHighlightClick(hit.id);
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
          boxShadow: "none",
          overflow: "hidden",
        }}
      >
        {/* Keep PDF.js canvas and text geometry stable during workspace resizing. */}
        <div style={{width:RASTER_WIDTH,transform:`scale(${pageWidthPx / RASTER_WIDTH})`,transformOrigin:"top left"}}>
        <Page
          pageNumber={index + 1}
          width={RASTER_WIDTH}
          devicePixelRatio={Math.min(2, Math.max(1.5, window.devicePixelRatio || 1))}
          renderTextLayer={Boolean(pdfSelectableText || pdfIRTarget || onNoteSelection || onAskSelection || onTranslateSelection || onImportReferenceSelection || citationTargets.length)}
          renderAnnotationLayer
          filterAnnotations={restorePdfLinkAnnotationBorders}
          onRenderSuccess={() => {
            tryHighlightAfterRender(index);
            window.requestAnimationFrame(() => tagReferenceTextLayer(index));
            if(visualSelection?.page===index){const preview=cropPdfBlockPreview(pageRefs.current[index],visualSelection.bbox);if(preview)setVisualPreview(preview);}
          }}
          onRenderTextLayerSuccess={() => tagReferenceTextLayer(index)}
          onLoadSuccess={(page) => {
            const viewport = page.getViewport({ scale: 1 });
            setPageSizesMap(prev => prev[index]?.width === viewport.width && prev[index]?.height === viewport.height ? prev : {
              ...prev,[index]:{width:viewport.width,height:viewport.height},
            });
          }}
        />
        </div>
        {pdfNoteHighlightPaths([...noteHighlights,...savedNoteHighlights],index+1).map(({color,path})=><svg key={color} data-pdf-note-highlight={color} aria-hidden="true" viewBox="0 0 1 1" preserveAspectRatio="none" style={{position:'absolute',inset:0,width:'100%',height:'100%',mixBlendMode:'multiply',pointerEvents:'none',zIndex:5}}><path d={path} fill={pdfNoteHighlightBackground(color)} fillRule="nonzero"/></svg>)}
        {onNoteHighlightClick&&savedNoteHighlights.filter(note=>note.page===index+1&&note.rects.length>0).map(note=><button key={note.id} type="button" className="pdf-note-highlight-marker" aria-label={readerLanguage==='zh'?'打开笔记':'Open note'} onClick={event=>{event.stopPropagation();onNoteHighlightClick(note.id);}} style={{top:`${note.rects[0][1]*100}%`}}>✎</button>)}
        {studyHighlight?.page === index && <div className="pdf-study-highlight" style={(() => {
          const box = studyHighlight.bbox;
          const valid = box && box.length === 4 && box.every(value=>Number.isFinite(value)&&value>=0&&value<=1) && box[0]<box[2] && box[1]<box[3];
          return valid ? {left:`${box[0]*100}%`,top:`${box[1]*100}%`,width:`${(box[2]-box[0])*100}%`,height:`${(box[3]-box[1])*100}%`} : {inset:3,background:"transparent"};
        })()} />}
        {citationTargets.length > 0 && citationPreview?.source === fileData && <PdfCitationLayer targets={citationTargets} getDocument={getCitationDocument} previewCache={citationPreview.cache} onNavigate={navigateCitation} onAddToLibrary={canAddCitationToLibrary ? addCitation : undefined} sourceKey={fileData}/>}
        {formulasEnabled && formulaDocument && formulaPages.has(index) && <PdfFormulaLayer key={`${formulaDocument.source_sha256}:${formulaDocument.version}`} blocks={formulaPages.get(index)!} onExplain={explainFormula} />}
        {formulaDocument && <PdfFigureLayer blocks={imageBlocks.filter(block=>block.page===index)} allBlocks={formulaDocument.blocks} onSelect={openVisualStudy}/> }
        {tablesEnabled && formulaDocument && tablePages.has(index) && <PdfTableLayer key={`tables:${studySourceKey}`} blocks={tablePages.get(index)!} onSelect={pdfIRTarget?openVisualStudy:openTableStudy} />}
      </div>
    );
  };

  const pageControls = (
      <div className="pdf-reader-tools">
        <div className="pdf-reader-control-group"><button aria-label={readerLabel("previousPage")} disabled={!pdfLoaded||currentPage<=1} onClick={()=>jumpPage(currentPage-1)}><LeftOutlined/></button><PdfPageControl page={currentPage} total={numPages} disabled={!pdfLoaded} onChange={jumpPage}/><button aria-label={readerLabel("nextPage")} disabled={!pdfLoaded||currentPage>=numPages} onClick={()=>jumpPage(currentPage+1)}><RightOutlined/></button></div>
        <div className="pdf-reader-control-group"><button aria-label={readerLabel("zoomOut")} disabled={!pdfLoaded||zoom<=.5} onClick={()=>changeZoom(zoom-.1)}><MinusOutlined/></button><PdfZoomControl zoom={zoom} disabled={!pdfLoaded} onChange={changeZoom}/><button aria-label={readerLabel("zoomIn")} disabled={!pdfLoaded||zoom>=2} onClick={()=>changeZoom(zoom+.1)}><PlusOutlined/></button></div>
      </div>
  );
  const readerTools = (
    <div className="pdf-reader-modebar pdf-reader-toolbar" hidden={typeof pdfHeaderEnd !== "function" && !toolsOpen} style={{maxWidth:containerWidth ? Math.max(180,containerWidth-24) : undefined}}>
      {pdfIRTarget && <div className="pdf-formula-controls" onMouseUp={event => event.stopPropagation()}>

        {!formulaDocument && !formulaBusy && <div className="pdf-reader-service-notice"><span>{t("pdfFormula.serviceRequired")}</span><a href={`/settings?section=model_defaults&service=parsing&return_to=${encodeURIComponent(window.location.pathname+window.location.search)}`}>{t("pdfFormula.configureService")}</a></div>}
        <details className="pdf-reader-parser-details"><summary>{readerLabel("displaySettings")}</summary><div><span role="status">{formulaBusy ? t("pdfFormula.processing") : pdfIR.state.status === "ready" ? !globalThis.crypto?.subtle ? t("pdfFormula.insecure") : !pdfSourceHash ? t("pdfFormula.checking") : formulaDocument ? formulaCount ? t("pdfFormula.count", {count:formulaCount}) : t("pdfFormula.empty") : t("pdfFormula.mismatch") : pdfIR.state.error_message || t("pdfFormula.intro")}</span>
        {formulaDocument && formulaCount > 0 && <button onClick={() => setFormulasEnabled(value => !value)} aria-pressed={formulasEnabled}>{formulasEnabled ? t("pdfFormula.hide") : t("pdfFormula.show")}</button>}
        {tableBlocks.length > 0 && <button onClick={() => setTablesEnabled(value => !value)} aria-pressed={tablesEnabled}>{t(tablesEnabled ? "pdfTable.hide" : "pdfTable.show")}</button>}
        {!formulaBusy && pdfIRTarget.canWrite !== false && <button onClick={() => void pdfIR.start(pdfIR.state.status === "ready" || pdfIR.state.status === "failed")}>{pdfIR.state.status === "not_requested" ? t("pdfFormula.enable") : t("pdfFormula.retry")}</button>}
        </div></details>
        {formulaDocument && studyBlocks.length > 0 && <button type="button" className="pdf-study-ai-button" onClick={() => {releasePinnedStudy();setStudyPulse(value=>value+1);setVisualSelection(undefined);setTableStudyOpen(false);setStudyOpen(true);}}><StudySparkle/><span>{t("pdfFormula.openCatalog")}</span></button>}
        {tableBlocks.length > 0 && <button onClick={() => {releasePinnedStudy();setVisualSelection(undefined);setStudyOpen(false);setTableStudyOpen(true);}}>{t("pdfTable.openCatalog", {count:tableBlocks.length})}</button>}
        {imageBlocks.length>0&&<select aria-label={readerLabel("visuals")} value="" onChange={event=>{const block=imageBlocks.find(item=>item.id===event.target.value);if(block)openVisualStudy(block);}}><option value="" disabled>{readerLabel("imageRegion")} ({imageBlocks.length})</option>{imageBlocks.filter(block=>block.image?.number).map(block=><option key={block.id} value={block.id}>{paperVisualLabel(block,formulaDocument!.blocks,readerLabel)}</option>)}{imageBlocks.some(block=>!block.image?.number)&&<optgroup label={readerLabel("imageRegion")}>{imageBlocks.filter(block=>!block.image?.number).map(block=><option key={block.id} value={block.id}>{paperVisualLabel(block,formulaDocument!.blocks,readerLabel)}</option>)}</optgroup>}</select>}

      </div>}
    </div>
  );

  const readerHeader = (
    <nav className={`pdf-reader-modebar pdf-reader-header${pdfToolbarMode === "navigation" ? " pdf-reader-header--compact" : ""}`} aria-label={paperMode ? readerLabel("reading") : t("paperShell.document",{defaultValue:"Document"})}>
      <div className="pdf-reader-header-start">{pdfHeaderStart}</div>
      <div className="pdf-reader-header-end">
        {pdfToolbarMode === "navigation" ? <>{typeof pdfHeaderEnd === "function" ? pdfHeaderEnd(null) : pdfHeaderEnd}{pageControls}</> : <>
        {pageControls}
        <button type="button" className="pdf-reader-hand-trigger" aria-label={readerLabel("panPaper")} title={readerLabel("panPaper")} aria-pressed={handPan.active} disabled={!pdfLoaded} onClick={toggleHandPan}><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M8 13.5V7a1.5 1.5 0 0 1 3 0v4.5-7a1.5 1.5 0 0 1 3 0v7-5a1.5 1.5 0 0 1 3 0v5-3a1.5 1.5 0 0 1 3 0V16a6 6 0 0 1-6 6h-1.5a6 6 0 0 1-5-2.7l-3.3-4.8a1.6 1.6 0 0 1 2.6-1.8L8 13.5Z"/></svg></button>
        <button type="button" className="pdf-reader-pin-trigger" aria-label={readerLabel("captureRegion")} title={readerLabel("captureRegion")} aria-pressed={pinCaptureActive} disabled={!pdfLoaded||!pinBoardReady} onClick={()=>{handPan.disable();setPinCaptureActive(active=>!active);}}><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M6 3v12a3 3 0 0 0 3 3h12"/><path d="M18 21V9a3 3 0 0 0-3-3H3"/></svg></button>
        {pdfIRTarget && <ReadingPreparationAction key={`${pdfIRTarget.dataset}/${pdfIRTarget.document}`} target={pdfIRTarget} onPrepared={pdfIR.refresh} />}
        {typeof pdfHeaderEnd === "function" ? pdfHeaderEnd(pdfIRTarget ? readerTools : null) : <>
          <div ref={toolsRef} className="pdf-reader-tools-popover" onKeyDown={event=>{if(event.key==="Escape"){setToolsOpen(false);toolsRef.current?.querySelector<HTMLButtonElement>("button")?.focus();}}}>
            <button type="button" aria-label="阅读工具" title="阅读工具" aria-expanded={toolsOpen} onClick={()=>setToolsOpen(open=>!open)}><ControlOutlined/></button>
            {readerTools}
          </div>
          {pdfHeaderEnd}
        </>}
        </>}
      </div>
    </nav>
  );

  if (error && errorSource.current === fileData) {
    return (
      <div ref={readerFrame} className="pdf-reader-workspace" data-reader-mode="original" style={{height:style?.height || "100%",width:"100%"}}>
        {studyPulse>0&&<PdfStudyActivation key={studyPulse} frame={readerFrame}/>}
    {readerHeader}
        <div role="alert" style={{padding:20,textAlign:"center",color:"#a6574c"}}>加载失败: {error}</div>
      </div>
    );
  }

  const totalHeight =
    (pageHeightsPx.reduce((s, h) => s + h, 0) || pageSlotHeight * numPages) +
    GAP * Math.max(0, numPages - 1) + handPan.gutterY * 2;

  const translationUnavailable=translateSelectionDisabled&&!isSingleEnglishWord(selectionAction?.selection.text||"");
  const commitNoteHighlight=(color:PdfNoteColor)=>{
    const rects=selectionAction?.highlightRects;
    if(!rects?.length)return;
    const page=selectionAction?.selection.page;
    if(!page)return;
    const signature=JSON.stringify(rects);
    const next=[...noteHighlights.filter(item=>item.page!==page||JSON.stringify(item.rects)!==signature),{id:crypto.randomUUID(),page,color,rects}];
    setNoteHighlightState({key:noteHighlightKey,session:readerSession,highlights:next});
    writePdfNoteHighlights(noteHighlightKey,next);
  };
  function toggleHandPan(){
    setPinCaptureActive(false);
    setSelectionAction(null);setSelectionHighlights([]);
    window.clearTimeout(referenceHoverTimer.current);
    window.clearTimeout(referenceHoverCloseTimer.current);
    window.getSelection()?.removeAllRanges();
    handPan.toggle();
  }
  return (
    <div ref={readerFrame} className="pdf-reader-workspace" data-reader-mode="original" style={{height:style?.height || "calc(100vh - 220px)",width:"100%"}}>
    {studyPulse>0&&<PdfStudyActivation key={studyPulse} frame={readerFrame}/>}
    {readerHeader}
    <div ref={pinStageRef} className="pdf-reader-stage">
    {noteReturn&&<button type="button" className="pdf-reader-note-return" aria-label={t('pdfReader.returnPosition')} onClick={()=>{
      const page=Math.min(numPages,Math.max(1,noteReturn.page));
      const top=(pageTops[page-1]||0)+(pageHeightsPx[page-1]||0)*noteReturn.progress;
      containerRef.current?.scrollTo({top,behavior:'auto'});setScrollTop(top);onViewPositionChange?.(noteReturn);setNoteReturn(undefined);
    }}>← {t('pdfReader.returnPosition')}</button>}
    <div ref={studyReaderRef} data-study-layout={studyLayout} className="pdf-reader-with-study" style={{height:"100%",width:"100%",display:"flex"}}>
    <div
      className={[className,handPan.active?'pdf-reader-hand-active':'',handPan.dragging?'is-dragging':''].filter(Boolean).join(' ')}
      ref={containerRef}
      onScroll={onScroll}
      onPointerDownCapture={handPan.onPointerDown}
      onPointerMove={handPan.onPointerMove}
      onPointerUp={handPan.onPointerEnd}
      onPointerCancel={handPan.onPointerEnd}
      onLostPointerCapture={handPan.onPointerEnd}
      onClickCapture={event=>{if(handPan.active){event.preventDefault();event.stopPropagation();}}}
      onMouseUp={handPan.active?undefined:updateSelectionAction}
      onMouseMove={handPan.active?undefined:handleReferenceHover}
      onMouseLeave={() => {
        window.clearTimeout(referenceHoverTimer.current);
        if (selectionAction?.referenceHover && !document.activeElement?.closest("[data-pdf-selection-toolbar]")) {
          referenceHoverCloseTimer.current = window.setTimeout(() => setSelectionAction(null), 250);
        }
      }}
      style={{
        overflow: "auto",
        position: "relative",
        ...style,
        backgroundColor: GAP_BACKGROUND,
        flex: "1 1 0", minWidth: 0, minHeight: 0, height: "100%",
      }}
    >

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
            background: "color-mix(in srgb, var(--color-primary, #006ae6) 22%, transparent)",
            borderRadius: 1,
            pointerEvents: "none",
          }}
        />
      ))}
      {selectionAction ? (
        <div data-pdf-selection-toolbar
          onMouseDown={event=>event.stopPropagation()}
          onMouseUp={event=>event.stopPropagation()}
          onMouseEnter={()=>window.clearTimeout(referenceHoverCloseTimer.current)}
          style={{position:"absolute",zIndex:30,left:(containerRef.current?.scrollLeft||0)+Math.max(8,Math.min(selectionAction.left-(containerRef.current?.scrollLeft||0)-190,(containerRef.current?.clientWidth || 396)-388)),top:Math.max((containerRef.current?.scrollTop||0)+8,Math.min(selectionAction.top-86,(containerRef.current?.scrollTop||0)+(containerRef.current?.clientHeight||720)-160)),width:"min(380px, calc(100% - 16px))"}}>
          <PdfSelectionComposer
            key={`${selectionAction.selection.page}:${selectionAction.selection.text}`}
            selection={selectionAction.selection}
            onAsk={onAskSelection}
            onNote={onNoteSelection}
            initialMode={selectionComposerMode}
            onModeChange={setSelectionComposerMode}
            onHighlight={onNoteSelection ? commitNoteHighlight : undefined}
            onClose={()=>{window.getSelection()?.removeAllRanges();setSelectionAction(null);}}
            onTranslate={onTranslateSelection ? ()=>onTranslateSelection(selectionAction.selection) : undefined}
            translationDisabled={translationUnavailable}
            translationTitle={translationUnavailable ? translateSelectionDisabledTip : "翻译"}
            actions={(() => {
              const actions:PdfComposerAction[]=[];
              if(onAddVocabularySelection && /[A-Za-z]/.test(selectionAction.selection.text))actions.push({key:"vocabulary",label:addVocabularySelectionLabel,onClick:()=>onAddVocabularySelection(selectionAction.selection)});
              for(const action of visibleLearningSelectionActions(learningSelectionActions,selectionAction.selection.text,Boolean(onTranslateSelection)))actions.push({key:action.key,label:action.label,disabled:action.disabled,title:action.disabledTip,onClick:()=>onLearningSelection?.(action.key,selectionAction.selection)});
              const reference=selectionAction.referenceAction;
              if(reference?.href && (reference.kind==="open" || reference.kind==="external"))actions.push({key:"reference",label:reference.kind==="open"?"打开知识库文档":"打开链接",onClick:()=>{if(reference.kind==="external")window.open(reference.href,"_blank","noopener,noreferrer");else window.location.assign(reference.href!);}});
              return actions;
            })()}
          />
        </div>
      ) : null}
      <Document
        externalLinkTarget="_blank"
        externalLinkRel="noopener noreferrer"
        // react-pdf retains this handler from mount; read the active source from a ref.
        onItemClick={({pageNumber,dest})=>setNativeDestination({source:currentSource.current,pageNumber,dest:Array.isArray(dest)?dest:undefined})}
        file={pdfFile}
        renderMode={renderMode}
        loading={renderNoDataFn()}
        noData={renderNoDataFn()}
        onLoadSuccess={(doc) => {
          if (currentSource.current !== fileData) return;
          pdfDocument.current = {source:fileData,document:doc};
          onPdfOutlineChange?.([]);
          if(onPdfOutlineChange&&typeof doc.getOutline==='function')void readNativePdfOutline(doc as unknown as Parameters<typeof readNativePdfOutline>[0]).then(outline=>{if(currentSource.current===fileData)onPdfOutlineChange(outline);}).catch(()=>undefined);
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
              if (currentSource.current !== fileData) return;
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
          setLoadedSource({fileData});
          setLoading(false);
          setError(null);
        }}
        onLoadError={(err) => {
          if (currentSource.current !== fileData) return;
          onPreviewError?.();
          errorSource.current = fileData;
          setError(err.message || "加载失败");
          setLoading(false);
        }}
      >
        <div
          style={{
            position: "relative",
            height: totalHeight,
            minWidth: handPan.gutter ? Math.max(pageWidthPx + 32,containerWidth) + handPan.gutter * 2 : pageWidthPx + 32,
            background: GAP_BACKGROUND,
          }}
        >
          {visiblePages.map((i) => renderVirtualPage(i))}
        </div>
      </Document>
    </div>
    {(studyOpen||tableStudyOpen||!!visualSelection)&&formulaDocument&&<PdfStudyDialog key={activeStudyPinId||'current-study'} onClose={closeStudy} onPinChange={handleStudyPinChange} onMinimize={minimizeStudy} onGeometryChange={handleStudyGeometryChange} initialPinned={!!activeStudyPinId} initialGeometry={activeStudyPinId?pinBoard.records.find(item=>item.id===activeStudyPinId)?.geometry:undefined}>
      {studyOpen && pdfIRTarget && <PdfFormulaStudy key={studySourceKey} target={pdfIRTarget} sourceKey={studySourceKey} blocks={studyBlocks} selected={studySelection?.block} autoExplain={studySelection?.explain} initialExplanation={activeStoredStudyPin?.kind==='formula'&&activeStoredStudyPin.blockId===studySelection?.block?.id?activeStoredStudyPin.explanation as FormulaExplanation:undefined} onExplanationChange={data=>{if(studySelection?.block)rememberStudyExplanation('formula',studySelection.block.id,data);}} compact onSelect={block=>setStudySelection({block,explain:false})} onNavigate={navigateFromStudy} canReturn={!!studyReturn} onReturn={returnFromStudy} onClose={closeStudy} />}
      {tableStudyOpen && <PdfTableStudy key={`tables:${studySourceKey}`} sourceKey={studySourceKey} blocks={tableBlocks} allBlocks={formulaDocument.blocks} selected={tableSelection} onSelect={openTableStudy} onNavigate={navigateFromStudy} onExplainVisual={openVisualStudy} canReturn={!!studyReturn} onReturn={returnFromStudy} onClose={closeStudy} />}
      {visualSelection&&pdfIRTarget&&<PdfVisualStudy key={`${studySourceKey}:${visualSelection.id}`} target={pdfIRTarget} document={formulaDocument} block={visualSelection} sourcePreview={visualPreview} autoExplain={!hasRestoredVisualSnapshot} initialExplanation={activeStoredStudyPin?.kind===visualSelection.type&&activeStoredStudyPin.blockId===visualSelection.id?activeStoredStudyPin.explanation as VisualExplanation:undefined} onExplanationChange={data=>rememberStudyExplanation(visualSelection.type==='table'?'table':'image',visualSelection.id,data)} onNavigate={id=>{navigateSourceBlock(id);if(!studyPinned)setVisualSelection(undefined);}} onInspectTable={openTableStudy} onClose={closeStudy} onReturn={returnFromStudy} canReturn={!!studyReturn}/>}
    </PdfStudyDialog>}
    </div>
    <PdfPinnedRegion stageRef={pinStageRef} viewportRef={containerRef} pageRefs={pageRefs} sourceKey={fileData} active={pinCaptureActive} onActiveChange={setPinCaptureActive} language={readerLanguage === "zh" ? "zh" : "en"} pins={pinBoard.records.filter((item):item is PdfRegionPinRecord=>item.kind==='region')} onAddPin={pin=>!isPersistablePdfPin(pin)?(readerLanguage==='zh'?'截图过大，无法钉住并保存。':'The capture is too large to pin and save.'):pinBoard.add(pin)} onUpdatePin={record=>pinBoard.update(record.id,{geometry:record.geometry})} onRemovePin={pinBoard.remove} onMinimizePin={id=>{pinBoard.update(id,{minimized:true});}}/>
    </div>
    {(!(studyOpen||tableStudyOpen||visualSelection)||studyPinned)&&<PdfPinDock items={pinBoard.records.filter(item=>item.minimized).map(item=>({id:item.id,title:item.title,kind:item.kind}))} onRestore={restorePin} storageKey={pinBoardKey} language={readerLanguage==='zh'?'zh':'en'}/>}
    </div>
  );
}
