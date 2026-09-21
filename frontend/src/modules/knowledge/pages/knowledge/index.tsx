import { Button, message, Modal, Popover, Progress, Segmented, Spin, Tag, Tooltip, Row, Col, Select, Switch, Tabs } from "antd";
import { useEffect, useMemo, useRef, useState, useCallback } from "react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import {
  CopyOutlined,
  DoubleLeftOutlined,
  DoubleRightOutlined,
  FileImageOutlined,
  FileSearchOutlined,
  HistoryOutlined,
  SnippetsOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import moment from "moment";
import { Doc } from "@/api/generated/core-client";
import type { Conversation } from "@/api/generated/chatbot-client";
import { Segment } from "@/api/generated/knowledge-client";

import type { Dataset as KnowledgeDataset } from "@/api/generated/knowledge-client";
import { TIME_FORMAT } from "@/modules/knowledge/constants/common";
import FileUtils from "@/modules/knowledge/utils/file";
import FileViewer, {
  type FileViewerRef,
} from "@/modules/knowledge/components/FileViewer";
import KnowledgeTabs from "./components/KnowledgeTabs";
import {
  DocumentServiceApi,
  SegmentServiceApi,
  KnowledgeBaseServiceApi,
  normalizeProxyableUrl,
} from "@/modules/knowledge/utils/request";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";
import { DetailPageHeader, type PdfReferenceAction, type PdfTextSelection, type PdfViewPosition } from "@/components/ui";
import type { DocumentChatSelection, DocumentTranslationRequest } from "@/modules/knowledge/components/PdfTemporaryChat/types";
import PdfTemporaryChat from "@/modules/knowledge/components/PdfTemporaryChat";
import { readCachedPdfChat, touchCachedPdfChat } from "@/modules/knowledge/components/PdfTemporaryChat/cache";
import { localizeErrorCode } from "@/components/request";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import { getTranslationStatus, translateSelectionText, TranslationUnavailableError } from "@/modules/knowledge/api/translation";
import { translateText } from "@/modules/knowledge/api/translation";
import {
  completePdfRenderJob,
  createSearchablePdfJob,
  ensureDocumentParsed,
  createTranslationPdfJob,
  deletePdfArtifact,
  getPdfCapabilities,
  getPdfData,
  listPdfLayoutBlocks,
  isActivePdfJob,
  latestActiveTranslationJob,
  latestTranslationJob,
  pdfArtifactContentUrl,
  updatePdfRenderJob,
  type PdfArtifact,
  type PdfCapabilities,
  type PdfLayoutBlock,
  type PdfRenderJob,
} from "@/modules/knowledge/api/pdfArtifacts";
import { buildSearchablePdf } from "@/modules/knowledge/utils/pdfDocumentRenderer";
import { translatableDocumentExtensions } from "@/modules/knowledge/utils/documentTranslation";
import AddVocabularyModal from "@/modules/vocabulary/AddVocabularyModal";
import DocumentVocabularyPanel from "@/modules/vocabulary/DocumentVocabularyPanel";
import { isVocabularyEnabled } from "@/runtime/mode";
import AddLearningContentModal, { type LearningSelection } from "@/modules/learning/AddLearningContentModal";
import { getKnowledgeBaseCapabilities, getLearningCatalog, type LearningCapability } from "@/modules/learning/api";
import DocumentLearningPanel from "@/modules/learning/DocumentLearningPanel";
import DocumentReferencesPanel from "@/modules/knowledge/components/DocumentReferencesPanel";
import { extractAcademicReferences, listAcademicReferences } from "@/modules/knowledge/api/academicReferences";
import { capabilityFamilies, capabilityFamily, capabilityFamilyI18nKey, chooseFamilyCapability, type CapabilityFamily } from "@/modules/learning/capabilityFamilies";
import {
  processingLevelSupportsSegments,
  type ProcessingLevel,
} from "@/modules/knowledge/utils/processingLevel";
import "./index.scss";

type KnowledgeDetail = Doc & {
  file_url?: string;
  download_file_url?: string;
};

type KnowledgeDatasetWithProcessingLevel = KnowledgeDataset & {
  processing_level?: ProcessingLevel;
};

const wait = (milliseconds: number) => new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds));
const isSingleEnglishWord = (value: string) => /^[A-Za-z][A-Za-z'-]*$/.test(value.trim());

async function writeTextToClipboard(text: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }
  } catch {
    // Fall back for denied permissions and browsers with partial Clipboard API support.
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "0";
  textarea.style.top = "0";
  textarea.style.width = "1px";
  textarea.style.height = "1px";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, text.length);

  try {
    if (
      typeof document.execCommand !== "function" ||
      !document.execCommand("copy")
    ) {
      throw new Error("Copy command failed");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}

const Detail = () => {
  const { t } = useTranslation();
  const [knowledgeDetail, setKnowledgeDetail] = useState<KnowledgeDetail>();

  const { knowledgeBaseId = "", knowledgeId = "" } = useParams();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [segmentDetail, setSegmentDetail] = useState<Segment>();
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const fileViewerRef = useRef<FileViewerRef>(null);
  const [canExportImagePdf, setCanExportImagePdf] = useState(false);
  const [exportingImagePdf, setExportingImagePdf] = useState(false);
  const [documentChatSelection, setDocumentChatSelection] =
    useState<DocumentChatSelection | null>(null);
  const [previewSideTab, setPreviewSideTab] = useState("chat");
  const [previewSideCollapsed, setPreviewSideCollapsed] = useState(false);
  const [segmentViewKey, setSegmentViewKey] = useState("");
  const [segmentViewOptions, setSegmentViewOptions] = useState<
    Array<{ label: ReactNode; value: string }>
  >([]);
  const [showSegmentSequence, setShowSegmentSequence] = useState(true);
  const [documentChatHistory, setDocumentChatHistory] = useState<Conversation[]>([]);
  const [selectedDocumentConversation, setSelectedDocumentConversation] = useState<string>();
  const [chatHistoryPopoverOpen, setChatHistoryPopoverOpen] = useState(false);
  const [translationRequest, setTranslationRequest] = useState<DocumentTranslationRequest | null>(null);
  const [translationSelection, setTranslationSelection] = useState<PdfTextSelection | null>(null);
  const [translationConfigured, setTranslationConfigured] = useState(false);
  const [translationLoading, setTranslationLoading] = useState(false);
  const [translationSource, setTranslationSource] = useState("");
  const [translationResult, setTranslationResult] = useState("");
  const [vocabularySelection, setVocabularySelection] = useState<PdfTextSelection | null>(null);
  const [vocabularyRefreshToken, setVocabularyRefreshToken] = useState(0);
  const [referenceImportSelection, setReferenceImportSelection] = useState<{ text: string; referenceId?: string; requestId: number }>();
  const [referenceActions, setReferenceActions] = useState<PdfReferenceAction[]>([]);
  const [referenceCount, setReferenceCount] = useState<number>();
  const referencePreanalysisDocument = useRef("");
  const [referencePreanalysisRetry, setReferencePreanalysisRetry] = useState(0);
  const [learningSelection,setLearningSelection]=useState<LearningSelection|null>(null);
  const [learningAnalysisSelection,setLearningAnalysisSelection]=useState<{selections:PdfTextSelection[];requestId:number}|undefined>();
  const [paragraphSelectionMode,setParagraphSelectionMode]=useState(false);
  const [quickReferenceOpen,setQuickReferenceOpen]=useState(false);
  const [learningCapabilities,setLearningCapabilities]=useState<LearningCapability[]>([]);
  const [learningLocalAvailable,setLearningLocalAvailable]=useState(false);
  const [processingLevel, setProcessingLevel] =
    useState<ProcessingLevel>("indexed");
  const [pdfCapabilities, setPdfCapabilities] = useState<PdfCapabilities>();
  const [originalPdfKind, setOriginalPdfKind] = useState<"image_only" | "native_text" | "mixed" | "unknown">("unknown");
  const [pdfSourceView, setPdfSourceView] = useState<"original" | "searchable" | "translation" | "comparison">("original");
  const [selectedPdfArtifact, setSelectedPdfArtifact] = useState<PdfArtifact>();
  const [pdfViewPosition, setPdfViewPosition] = useState<PdfViewPosition>({ page: 1, progress: 0 });
  const [pdfTask, setPdfTask] = useState<PdfRenderJob>();
  const [pdfTaskDetail, setPdfTaskDetail] = useState("");
  const [translationModalOpen, setTranslationModalOpen] = useState(false);
  const [translationMode, setTranslationMode] = useState<"api" | "llm">("api");
  const [translationTarget, setTranslationTarget] = useState("zh");
  const cancelledPdfJobsRef = useRef(new Set<string>());
  const canShowSegments =
    developerActive && processingLevelSupportsSegments(processingLevel);

  const refreshPdfCapabilities = useCallback(async () => {
    if (!knowledgeBaseId || !knowledgeId) return;
    try {
      const capabilities = await getPdfCapabilities(knowledgeBaseId, knowledgeId);
      setPdfCapabilities(capabilities);
      const latestJob = latestTranslationJob(capabilities.jobs);
      if (latestJob) {
        setPdfTask(latestJob);
        if (isActivePdfJob(latestJob)) {
          setPdfTaskDetail(latestJob.stage === "RENDERING" ? "正在恢复文档生成" : "正在恢复翻译任务");
        } else {
          setPdfTaskDetail("");
        }
      }
      if (capabilities.searchable_artifact && pdfSourceView === "original") {
        setSelectedPdfArtifact(capabilities.searchable_artifact);
        setPdfSourceView("searchable");
      }
    } catch {
      setPdfCapabilities(undefined);
    }
  }, [knowledgeBaseId, knowledgeId, pdfSourceView]);

  useEffect(() => { void refreshPdfCapabilities(); }, [refreshPdfCapabilities]);

  useEffect(() => {
    setOriginalPdfKind("unknown");
    cancelledPdfJobsRef.current.clear();
    try {
      const cached = sessionStorage.getItem(`lazymind:pdf-position:${knowledgeId}`);
      const position = cached ? JSON.parse(cached) as PdfViewPosition : undefined;
      setPdfViewPosition(position?.page ? position : { page: 1, progress: 0 });
    } catch {
      setPdfViewPosition({ page: 1, progress: 0 });
    }
  }, [knowledgeId]);

  const handlePdfViewPositionChange = useCallback((position: PdfViewPosition) => {
    setPdfViewPosition(position);
    try {
      sessionStorage.setItem(`lazymind:pdf-position:${knowledgeId}`, JSON.stringify(position));
    } catch {
      // Reading progress persistence is best effort.
    }
  }, [knowledgeId]);

  const handleOriginalPdfKindDetected = useCallback((kind: "image_only" | "native_text" | "mixed") => {
    setOriginalPdfKind(kind);
  }, []);

  useEffect(() => {
    getTranslationStatus().then(setTranslationConfigured).catch(() => setTranslationConfigured(false));
  }, []);

  useEffect(()=>{ if(!knowledgeBaseId)return; Promise.all([getLearningCatalog(),getKnowledgeBaseCapabilities(knowledgeBaseId)]).then(([catalog,configured])=>{const enabled=new Set(configured.filter(x=>x.enabled).map(x=>x.capability_key));setLearningCapabilities(catalog.capabilities.filter(x=>enabled.has(x.key)));setLearningLocalAvailable(catalog.local_available)}).catch(()=>setLearningCapabilities([])); },[knowledgeBaseId]);

  useEffect(()=>setLearningAnalysisSelection(undefined),[knowledgeId]);

  useEffect(() => {
    referencePreanalysisDocument.current = "";
    setReferencePreanalysisRetry(0);
    setReferenceImportSelection(undefined);
    setReferenceActions([]);
    setReferenceCount(undefined);
  }, [knowledgeId]);

  useEffect(() => {
    if (!canShowSegments && previewSideTab === "segments") {
      setPreviewSideTab("chat");
    }
  }, [canShowSegments, previewSideTab]);

  const translatePdfSelection = useCallback(async (selection: PdfTextSelection) => {
    setTranslationSelection(selection);
    setTranslationSource(selection.text);
    setTranslationResult("");
    setTranslationLoading(true);
    try {
      const result = await translateSelectionText(selection.text);
      setTranslationResult(result.translated_text);
    } catch (error) {
      message.error(error instanceof TranslationUnavailableError&&error.reason==="dictionary_not_found"?t("knowledge.dictionaryNotFound"):error instanceof TranslationUnavailableError?t("knowledge.translationConfigureTip"):t("knowledge.translationFailed"));
    } finally {
      setTranslationLoading(false);
    }
  }, [t]);

  const translateWithModel = useCallback(() => {
    if (!translationSelection) return;
    const selection: DocumentChatSelection = { source: "pdf", ...translationSelection };
    setDocumentChatSelection(selection);
    setTranslationRequest({ id: Date.now(), selection });
    setTranslationSource("");
    setTranslationResult("");
    setPreviewSideCollapsed(false);
    setPreviewSideTab("chat");
  }, [translationSelection]);

  const refreshDocumentChatHistory = useCallback(() => {
    if (!knowledgeId) return;
    ChatServiceApi().conversationServiceListConversations(
      { pageSize: 100 },
      {
        params: {
          include_ephemeral: true,
          source_type: "pdf_preview",
          source_document_id: knowledgeId,
          is_task_conv: false,
        },
        silentError: true,
      } as never,
    ).then((response) => {
      const conversations = response.data.conversations || [];
      setDocumentChatHistory(conversations);
      const cached = readCachedPdfChat(knowledgeId);
      if (cached && conversations.some((item) => item.conversation_id === cached.conversationId)) {
        setSelectedDocumentConversation((current) => current || cached.conversationId);
      }
    }).catch(() => {});
  }, [knowledgeId]);

  useEffect(() => {
    setSelectedDocumentConversation(undefined);
    refreshDocumentChatHistory();
  }, [refreshDocumentChatHistory]);

  const handleSegmentViewOptionsChange = useCallback(
    (options: Array<{ label: ReactNode; value: string }>) => {
      setSegmentViewOptions(options);
    },
    [],
  );

  const askPdfSelection = useCallback((selection: PdfTextSelection) => {
    setDocumentChatSelection({ source: "pdf", ...selection });
    setPreviewSideCollapsed(false);
    setPreviewSideTab("chat");
  }, []);

  const importReferenceSelection = useCallback((selection: PdfTextSelection) => {
    setReferenceImportSelection({ text: selection.text, referenceId: selection.referenceId, requestId: Date.now() });
    setPreviewSideCollapsed(false);
    setPreviewSideTab("references");
  }, []);

  const preanalyzeReferences = useCallback(() => {
    if (!knowledgeId || referencePreanalysisDocument.current === knowledgeId) return;
    referencePreanalysisDocument.current = knowledgeId;
    void listAcademicReferences(knowledgeId)
      .then((items) => items.length ? items : extractAcademicReferences(knowledgeId, true))
      .then((items) => {
        setReferenceCount(items.length);
        setReferenceActions(items.flatMap((item) => {
          const page = Number(item.Page ?? item.page ?? 0);
          const rawBBox = item.BBoxJSON ?? item.bbox_json ?? [];
          const regions = Array.isArray(rawBBox)
            ? (rawBBox.length === 4 ? [{ page, bbox: rawBBox }] : [])
            : (rawBBox.regions || []);
          const base = {
            referenceId: String(item.ID || item.id || ""),
            referenceKey: String(item.ReferenceKey || item.reference_key || ""),
            rawText: String(item.RawText || item.raw_text || ""),
            kind: "download" as const,
          };
          if (!regions.length) return [{ ...base, page: page > 0 ? page : undefined }];
          return regions.flatMap((region) => region.bbox.length === 4 ? [{
            ...base,
            page: Number(region.page),
            bbox: region.bbox.map(Number) as [number, number, number, number],
          }] : []);
        }).filter((item) => item.referenceId && item.rawText));
      })
      .catch((error) => {
        referencePreanalysisDocument.current = "";
        const detail = error instanceof Error ? error.message : "";
        if (/parsing|root nodes|解析/.test(detail)) {
          window.setTimeout(() => setReferencePreanalysisRetry((current) => current + 1), 3000);
        } else {
          setReferenceCount(0);
        }
      });
  }, [knowledgeId]);

  useEffect(() => { preanalyzeReferences(); }, [preanalyzeReferences, referencePreanalysisRetry]);

  useEffect(() => {
    if (referenceCount === 0 && previewSideTab === "references") {
      setPreviewSideTab("chat");
    }
  }, [previewSideTab, referenceCount]);

  const askSegment = useCallback((
    segment: Segment,
    selectedText?: string,
    segmentGroup?: string,
  ) => {
    let metadata: Record<string, unknown> = {};
    if (segment.meta) {
      try {
        metadata = JSON.parse(segment.meta) as Record<string, unknown>;
      } catch {
        metadata = {};
      }
    }
    const rawPage = Number(metadata.page);
    const rawBbox = metadata.bbox;
    setDocumentChatSelection({
      source: "segment",
      text: selectedText || segment.display_content || segment.content || "",
      page: Number.isFinite(rawPage) ? rawPage + 1 : undefined,
      bbox: Array.isArray(rawBbox) && rawBbox.length === 4
        ? rawBbox.map(Number) as [number, number, number, number]
        : undefined,
      segmentId: segment.segment_id,
      segmentNumber: segment.number,
      group: segmentGroup,
    });
    setPreviewSideCollapsed(false);
    setPreviewSideTab("chat");
  }, []);

  const {
    getDatasetDetail: getKbDetail,
    setCurrentDataset,
    clearDataset,
  } = useDatasetPermissionStore();
  const hasWritePermission = useDatasetPermissionStore((state) =>
    state.hasWritePermission(),
  );

  const group = useMemo(() => {
    return searchParams.get("group_name") || "";
  }, [searchParams]);

  const segmentId = useMemo(() => {
    return searchParams.get("segement_id") || "";
  }, [searchParams]);

  const getDetail = useCallback(() => {
    DocumentServiceApi()
      .documentServiceGetDocument({
        dataset: knowledgeBaseId,
        document: knowledgeId,
      })
      .then((res) => {
        setKnowledgeDetail(res.data);
      });
  }, [knowledgeBaseId, knowledgeId]);

  const getDatasetDetail = useCallback(() => {
    KnowledgeBaseServiceApi()
      .datasetServiceGetDataset({ dataset: knowledgeBaseId })
      .then((res) => {
        const dataset = res.data as unknown as KnowledgeDatasetWithProcessingLevel;
        setCurrentDataset(dataset);
        setProcessingLevel(dataset.processing_level || "indexed");
      });
  }, [knowledgeBaseId, setCurrentDataset]);

  useEffect(() => {
    getDetail();
    getDatasetDetail();

    return () => {
      clearDataset();
    };
  }, [getDetail, getDatasetDetail, clearDataset]);

  useEffect(() => {
    const syncDeveloperActive = () => {
      setDeveloperActive(isDeveloperModeActive());
    };

    const handleDeveloperActiveChange = (event: Event) => {
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail
        ?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(
      DEVELOPER_ACTIVE_EVENT,
      handleDeveloperActiveChange,
    );

    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(
        DEVELOPER_ACTIVE_EVENT,
        handleDeveloperActiveChange,
      );
    };
  }, []);

  const getSegmentDetail = useCallback(() => {
    if (group && segmentId) {
      SegmentServiceApi()
        .segmentServiceGetSegment({
          dataset: knowledgeBaseId,
          document: knowledgeId,
          segment: segmentId,
          group: group,
        })
        .then((res) => {
          setSegmentDetail(res.data);
        });
    }
  }, [group, segmentId, knowledgeBaseId, knowledgeId]);

  useEffect(() => {
    getSegmentDetail();
  }, [group, segmentId, getSegmentDetail]);

  const originalPreviewFile = useMemo(() => {
    const filePath = knowledgeDetail?.file_url;
    if (!filePath) {
      return "";
    }

    const fileUrl = `${window.location.origin}/api/core${filePath}`;
    return normalizeProxyableUrl(fileUrl);
  }, [knowledgeDetail?.download_file_url, knowledgeDetail?.file_url]);

  const previewFile = useMemo(() => {
    if (pdfSourceView !== "original" && pdfSourceView !== "comparison" && selectedPdfArtifact) {
      return pdfArtifactContentUrl(knowledgeBaseId, knowledgeId, selectedPdfArtifact.id);
    }
    if (pdfSourceView === "comparison" && pdfCapabilities?.searchable_artifact) {
      return pdfArtifactContentUrl(knowledgeBaseId, knowledgeId, pdfCapabilities.searchable_artifact.id);
    }
    return originalPreviewFile;
  }, [knowledgeBaseId, knowledgeId, originalPreviewFile, pdfCapabilities?.searchable_artifact, pdfSourceView, selectedPdfArtifact]);

  const translationPreviewFile = useMemo(() => selectedPdfArtifact
    ? pdfArtifactContentUrl(knowledgeBaseId, knowledgeId, selectedPdfArtifact.id)
    : "", [knowledgeBaseId, knowledgeId, selectedPdfArtifact]);

  const handleExportImagePdf = useCallback(async () => {
    if (!canExportImagePdf || exportingImagePdf) {
      return;
    }
    setExportingImagePdf(true);
    try {
      await fileViewerRef.current?.exportImagePdf();
      message.success("已导出图片 PDF");
    } catch {
      message.error(localizeErrorCode("2000509"));
    } finally {
      setExportingImagePdf(false);
    }
  }, [canExportImagePdf, exportingImagePdf]);

  const runSearchablePdf = useCallback(async (force = false): Promise<PdfArtifact | undefined> => {
    const created = await createSearchablePdfJob(knowledgeBaseId, knowledgeId, force);
    if (created.artifact) {
      setSelectedPdfArtifact(created.artifact);
      setPdfSourceView("searchable");
      return created.artifact;
    }
    const job = created.job;
    if (!job) return undefined;
    setPdfTask({ ...job, status: "RUNNING", stage: "PREPARING", progress: 2 });
    try {
      const source = fileViewerRef.current?.getPdfData();
      if (!source) throw new Error("PDF 尚未加载完成");
      let blocks: PdfLayoutBlock[] = [];
      try {
        blocks = await listPdfLayoutBlocks(knowledgeBaseId, knowledgeId);
      } catch { /* Reader has not produced layout nodes yet. */ }
      if (!blocks.length) {
        setPdfTask((current) => current ? { ...current, stage: "PARSING_READER", progress: 3 } : current);
        setPdfTaskDetail("尚未解析，正在通过文档 Reader 生成 parsed root nodes");
        for (let attempt = 0; attempt < 150; attempt++) {
          const parsed = await ensureDocumentParsed(knowledgeBaseId, knowledgeId);
          if (parsed.status === "failed") throw new Error("文档 Reader 解析失败");
          if (parsed.status === "parsed") {
            try { blocks = await listPdfLayoutBlocks(knowledgeBaseId, knowledgeId); } catch { blocks = []; }
            if (blocks.length) break;
          }
          setPdfTaskDetail("文档 Reader 正在解析");
          await wait(2000);
        }
        if (!blocks.length) throw new Error("文档解析超时，或 Reader 未产生区域布局节点");
      }
      await updatePdfRenderJob(knowledgeBaseId, knowledgeId, job.id, { status: "RUNNING", stage: "WRITING_TEXT_LAYER", progress: 5 });
      const blob = await buildSearchablePdf(source, blocks, ({ page, pages, progress }) => {
        const next = 5 + Math.round(progress * 0.85);
        setPdfTask((current) => current ? { ...current, status: "RUNNING", stage: "WRITING_TEXT_LAYER", progress: next } : current);
        setPdfTaskDetail(`${page} / ${pages} 页`);
      });
      setPdfTask((current) => current ? { ...current, stage: "VERIFYING", progress: 94 } : current);
      const filename = `${(knowledgeDetail?.display_name || "document").replace(/\.pdf$/i, "")}-searchable.pdf`;
      const artifact = await completePdfRenderJob(knowledgeBaseId, knowledgeId, job.id, blob, filename, 0, blocks);
      setPdfTask({ ...job, status: "READY", stage: "READY", progress: 100, artifact_id: artifact.id });
      setSelectedPdfArtifact(artifact);
      setPdfSourceView("searchable");
      await refreshPdfCapabilities();
      message.success("已生成并缓存可搜索 PDF");
      return artifact;
    } catch (error) {
      const rawDetail = error instanceof Error ? error.message : "";
      const detail = /status code 502|bad gateway/i.test(rawDetail)
        ? "读取 Reader 文本块失败，请确认文档已解析完成后重试"
        : rawDetail || "生成可搜索 PDF 失败";
      await updatePdfRenderJob(knowledgeBaseId, knowledgeId, job.id, { status: "FAILED", stage: "FAILED", progress: 0, error_message: detail }).catch(() => undefined);
      setPdfTask({ ...job, status: "FAILED", stage: "FAILED", progress: 0, error_message: detail });
      message.error(detail);
      return undefined;
    }
  }, [knowledgeBaseId, knowledgeDetail?.display_name, knowledgeId, refreshPdfCapabilities]);

  const runTranslationPdf = useCallback(async (force = false, basis?: PdfArtifact) => {
    setTranslationModalOpen(false);
    const filename = knowledgeDetail?.display_name || "document.pdf";
    const extension = filename.split(".").pop()?.toLowerCase() || "";
    let source: ArrayBuffer;
    const layoutSource = extension === "pdf" ? "backend-region-layout-v1" : `${extension}-structure-v1`;
    try {
      source = await getPdfData(originalPreviewFile);
      if (extension === "pdf") {
        setPdfTaskDetail("正在确认文档 parsed root nodes");
        for (let attempt = 0; attempt < 150; attempt++) {
          const parsed = await ensureDocumentParsed(knowledgeBaseId, knowledgeId);
          if (parsed.status === "failed") throw new Error("文档 Reader 解析失败");
          if (parsed.status === "parsed") break;
          if (attempt === 149) throw new Error("文档 Reader 解析超时");
          await wait(2000);
        }
      }
    } catch (error) {
      const detail = error instanceof Error ? error.message : "无法读取 PDF 文本层";
      message.error(detail);
      return;
    }
    const targetLanguage = basis?.target_language || translationTarget;
    const providerType = basis?.provider_type === "llm" ? "llm" : basis?.provider_type === "api" ? "api" : translationMode;
    const provider = basis?.provider || (providerType === "api" ? "Tencent Translation" : "LazyMind LLM");
    const created = await createTranslationPdfJob(knowledgeBaseId, knowledgeId, {
      target_language: targetLanguage,
      provider_type: providerType,
      provider,
      model: basis?.model || (providerType === "llm" ? "document-context" : undefined),
      options_hash: layoutSource,
      force,
      source: new Blob([source]),
      source_filename: filename,
    });
    if (created.artifact) {
      setSelectedPdfArtifact(created.artifact);
      setPdfSourceView("translation");
      message.success("已加载缓存译本");
      return;
    }
    const job = created.job;
    if (!job) return;
    cancelledPdfJobsRef.current.delete(job.id);
    setPdfTask({ ...job, status: "RUNNING", stage: "TRANSLATING", progress: 3 });
    setPdfTaskDetail("任务已提交到后端，可关闭或刷新页面");
    await refreshPdfCapabilities();
  }, [knowledgeBaseId, knowledgeDetail?.display_name, knowledgeId, originalPreviewFile, refreshPdfCapabilities, translationMode, translationTarget]);

  const cancelTranslationJob = useCallback(async () => {
    const job = pdfTask?.kind === "TRANSLATION_PDF" && isActivePdfJob(pdfTask)
      ? pdfTask
      : latestActiveTranslationJob(pdfCapabilities?.jobs || []);
    if (!job) return;
    cancelledPdfJobsRef.current.add(job.id);
    await updatePdfRenderJob(knowledgeBaseId, knowledgeId, job.id, {
      status: "CANCELLED",
      stage: "CANCELLED",
      progress: job.progress,
      error_message: "用户已取消翻译",
    });
    setPdfTask({ ...job, status: "CANCELLED", stage: "CANCELLED", error_message: "用户已取消翻译" });
    await refreshPdfCapabilities();
    message.success("翻译任务已取消");
  }, [knowledgeBaseId, knowledgeId, pdfCapabilities?.jobs, pdfTask, refreshPdfCapabilities]);

  useEffect(() => {
    const activeJob = latestActiveTranslationJob(pdfCapabilities?.jobs || []);
    if (!activeJob) return;
    setPdfTask(activeJob);
    setPdfTaskDetail("后端正在执行，刷新或关闭页面不会中断");
    const poll = async () => {
      try {
        const capabilities = await getPdfCapabilities(knowledgeBaseId, knowledgeId);
        setPdfCapabilities(capabilities);
        const updated = capabilities.jobs.find((job) => job.id === activeJob.id);
        if (!updated) return;
        setPdfTask(updated);
        if (updated.status === "READY") {
          const artifact = capabilities.translations.find((item) => item.id === updated.artifact_id);
          if (artifact) {
            setSelectedPdfArtifact(artifact);
            setPdfSourceView("translation");
          }
          message.success("翻译完成并已缓存");
        } else if (updated.status === "FAILED") {
          message.error(updated.error_message || "文档翻译失败");
        }
      } catch { /* Keep the persisted task running and retry on the next poll. */ }
    };
    const timer = window.setInterval(() => { void poll(); }, 1500);
    return () => window.clearInterval(timer);
  }, [knowledgeBaseId, knowledgeId, pdfCapabilities?.jobs]);

  const removePdfArtifact = useCallback((artifact: PdfArtifact) => {
    Modal.confirm({
      title: artifact.kind === "SEARCHABLE_PDF" ? "删除普通版 PDF？" : "删除这个翻译版本？",
      content: "删除后文件不可恢复，但可以重新生成。原始文档不受影响。",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await deletePdfArtifact(knowledgeBaseId, knowledgeId, artifact.id);
        if (artifact.kind === "SEARCHABLE_PDF") {
          setSelectedPdfArtifact(undefined);
          setPdfSourceView("original");
        } else if (selectedPdfArtifact?.id === artifact.id) {
          setSelectedPdfArtifact(pdfCapabilities?.searchable_artifact);
          setPdfSourceView(pdfCapabilities?.searchable_artifact ? "searchable" : "original");
        }
        await refreshPdfCapabilities();
        message.success("版本已删除");
      },
    });
  }, [knowledgeBaseId, knowledgeId, pdfCapabilities?.searchable_artifact, refreshPdfCapabilities, selectedPdfArtifact?.id]);

  const documentExtension = (knowledgeDetail?.display_name || "").split(".").pop()?.toLowerCase() || "";
  const isPdfDocument = documentExtension === "pdf";
  const canTranslateDocument = translatableDocumentExtensions.includes(documentExtension);
  const translationInProgress = Boolean(pdfTask?.kind === "TRANSLATION_PDF" && isActivePdfJob(pdfTask)) ||
    Boolean(latestActiveTranslationJob(pdfCapabilities?.jobs || []));

  const pageTitle = useMemo(() => {
    const displayName = knowledgeDetail?.display_name;
    if (!displayName) {
      return displayName;
    }
    if (!canTranslateDocument) {
      return displayName;
    }
    return (
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 8,
          minWidth: 0,
          maxWidth: "100%",
        }}
      >
        <Tooltip title={displayName}>
          <span className="detail-title-text">{displayName}</span>
        </Tooltip>
        <Segmented
          size="small"
          value={pdfSourceView === "comparison" ? "comparison" : pdfSourceView === "translation" ? "translation" : pdfCapabilities?.searchable_artifact ? "searchable" : "original"}
          options={[
            { label: isPdfDocument ? "普通版" : "原文", value: isPdfDocument && pdfCapabilities?.searchable_artifact ? "searchable" : "original" },
            ...(pdfCapabilities?.translations.length ? [{ label: "翻译版", value: "translation" }] : []),
            ...(isPdfDocument && pdfCapabilities?.translations.length ? [{ label: "对照版", value: "comparison" }] : []),
          ]}
          onChange={(value) => {
            const view = value as "original" | "searchable" | "translation" | "comparison";
            setPdfSourceView(view);
            if (view === "comparison") setPreviewSideCollapsed(true);
            if (view === "original") setSelectedPdfArtifact(undefined);
            if (view === "searchable") setSelectedPdfArtifact(pdfCapabilities?.searchable_artifact);
            if (view === "translation" || view === "comparison") {
              setSelectedPdfArtifact((current) => current?.kind === "TRANSLATION_PDF"
                ? current
                : pdfCapabilities?.translations.at(-1));
            }
          }}
        />
        {isPdfDocument && pdfCapabilities?.searchable_artifact ? (
          <Button
            type="text"
            size="small"
            onClick={() => {
              if (pdfSourceView === "original") {
                setSelectedPdfArtifact(pdfCapabilities.searchable_artifact);
                setPdfSourceView("searchable");
              } else {
                setSelectedPdfArtifact(undefined);
                setPdfSourceView("original");
              }
            }}
          >
            {pdfSourceView === "original" ? "返回普通 PDF" : "查看原始文件"}
          </Button>
        ) : null}
        {pdfCapabilities?.translations.length ? (
          <Select
            size="small"
            aria-label="选择缓存译本"
            placeholder="缓存译本"
            style={{ width: 150 }}
            value={pdfSourceView === "translation" || pdfSourceView === "comparison" ? selectedPdfArtifact?.id : undefined}
            options={pdfCapabilities.translations.map((artifact) => ({
              value: artifact.id,
              label: `${artifact.target_language === "en" ? "英文" : "中文"} · ${artifact.provider_type === "llm" ? "大模型" : "翻译 API"}`,
            }))}
            onChange={(id) => {
              setSelectedPdfArtifact(pdfCapabilities.translations.find((artifact) => artifact.id === id));
              if (pdfSourceView !== "comparison") setPdfSourceView("translation");
            }}
          />
        ) : null}
        {(isPdfDocument && pdfCapabilities?.searchable_artifact) || ((pdfSourceView === "translation" || pdfSourceView === "comparison") && selectedPdfArtifact) ? (
          <Popover
            trigger="click"
            content={<div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              {(pdfSourceView === "translation" || pdfSourceView === "comparison") && selectedPdfArtifact ? <>
                <Button type="text" size="small" onClick={() => void runTranslationPdf(true, selectedPdfArtifact)}>重新生成当前译本</Button>
                <Button type="text" danger size="small" onClick={() => removePdfArtifact(selectedPdfArtifact)}>删除当前译本</Button>
              </> : pdfCapabilities?.searchable_artifact ? <>
                <Button type="text" size="small" onClick={() => void runSearchablePdf(true)}>重新生成普通版</Button>
                <Button type="text" danger size="small" onClick={() => removePdfArtifact(pdfCapabilities.searchable_artifact!)}>删除普通版</Button>
              </> : null}
            </div>}
          >
            <Tooltip title="管理当前文档版本">
              <Button type="text" size="small" icon={<SettingOutlined />} aria-label="管理当前文档版本" />
            </Tooltip>
          </Popover>
        ) : null}
        {isPdfDocument && originalPdfKind === "image_only" && !pdfCapabilities?.searchable_artifact ? (
          <Button size="small" icon={<FileSearchOutlined />} onClick={() => void runSearchablePdf()}>
            转为普通 PDF
          </Button>
        ) : null}
        {!pdfCapabilities?.translations.length ? (
          <Button size="small" type="primary" loading={translationInProgress} disabled={translationInProgress} onClick={() => setTranslationModalOpen(true)}>
            {translationInProgress ? "翻译进行中" : "翻译文档"}
          </Button>
        ) : null}
        {learningCapabilities.length ? <Tooltip title={t("learning.quickReference")}>
          <Button
            type="text"
            size="small"
            icon={<SnippetsOutlined />}
            aria-label={t("learning.quickReference")}
            onClick={() => setQuickReferenceOpen(true)}
          />
        </Tooltip> : null}
        {isPdfDocument ? <Tooltip title="导出成图片pdf">
          <Button
            type="text"
            size="small"
            icon={<FileImageOutlined />}
            loading={exportingImagePdf}
            onClick={handleExportImagePdf}
            style={{ flexShrink: 0 }}
          />
        </Tooltip> : null}
      </span>
    );
  }, [
    canTranslateDocument,
    exportingImagePdf,
    handleExportImagePdf,
    learningCapabilities.length,
    knowledgeDetail?.display_name,
    isPdfDocument,
    originalPdfKind,
    pdfCapabilities,
    pdfSourceView,
    runSearchablePdf,
    runTranslationPdf,
    removePdfArtifact,
    selectedPdfArtifact?.id,
    t,
    translationInProgress,
  ]);

  return (
    <div className="knowledge-container !h-full !items-start">
      <DetailPageHeader
        breadcrumbs={[
          { title: t("layout.knowledgeBase"), href: "/lib/knowledge/list" },
          {
            title: getKbDetail()?.display_name || t("knowledge.detail"),
            href: `/lib/knowledge/detail/${getKbDetail()?.dataset_id}`,
          },
          { title: knowledgeDetail?.display_name },
        ]}
        title={pageTitle}
        onBack={() => {
          const bool = ["aiwrite", "aireview", "chat"].includes(
            searchParams.get("from") ?? "",
          );
          if (bool) {
            navigate(`/lib/knowledge/detail/${knowledgeBaseId}?from=aiwrite`);
          } else {
            navigate(-1);
          }
        }}
        titleExtra={
          developerActive ? (
            <div>
              <span
                style={{
                  marginRight: "4px",
                  color: "var(--color-text-description)",
                }}
              >
                ID: {knowledgeId}
              </span>
              <Tooltip title={t("common.copy")}>
                <Button
                  type="text"
                  size="small"
                  aria-label={t("common.copy")}
                  icon={<CopyOutlined />}
                  style={{ color: "var(--color-text-description)" }}
                  onClick={async () => {
                    try {
                      await writeTextToClipboard(knowledgeId);
                      message.success(t("knowledge.copySuccess"));
                    } catch {
                      message.error(t("knowledge.copyFailedManual"));
                    }
                  }}
                />
              </Tooltip>
            </div>
          ) : null
        }
        extraContent={[
          { label: t("knowledge.source"), value: t("knowledge.localFile") },
          {
            label: t("knowledge.createTime"),
            value: moment(knowledgeDetail?.create_time).format(TIME_FORMAT),
          },
          {
            label: t("knowledge.creator"),
            value: knowledgeDetail?.creator || "-",
          },
          {
            label: t("knowledge.originalFile"),
            value: (
              <a
                href={previewFile}
                rel="noreferrer noopener"
                target="_blank"
                title={knowledgeDetail?.display_name}
              >
                {knowledgeDetail?.display_name}
              </a>
            ),
            hidden: !hasWritePermission,
          },
          {
            label: t("knowledge.updateTime"),
            value: moment(knowledgeDetail?.update_time).format(TIME_FORMAT),
          },
          {
            label: t("knowledge.size"),
            value:
              FileUtils.formatFileSize(knowledgeDetail?.document_size) || "-",
          },
          {
            label: t("knowledge.tags"),
            value:
              knowledgeDetail?.tags && knowledgeDetail?.tags.length > 0
                ? knowledgeDetail.tags.map((tag) => (
                    <Tag style={{ marginLeft: "8px" }} key={tag}>
                      {tag}
                    </Tag>
                  ))
                : "-",
          },
        ]}
      />
      <Row gutter={[12, 12]} className="knowledge-preview-layout mt-6 min-h-0 w-full flex-1">
        <Col
          flex={previewSideCollapsed ? "auto" : "0 0 62.5%"}
          className="knowledge-preview-file-column min-h-0 min-w-0"
        >
          {pdfTask?.kind === "SEARCHABLE_PDF" && pdfTask.status !== "READY" ? (
            <div className={`pdf-processing-card is-${pdfTask.status.toLowerCase()}`}>
              <div><strong>{pdfTask.status === "FAILED" ? "转换失败" : "正在生成可搜索 PDF"}</strong><span>{pdfTaskDetail}</span></div>
              <Progress percent={pdfTask.progress} status={pdfTask.status === "FAILED" ? "exception" : "active"} size="small" />
              {pdfTask.error_message ? <div className="pdf-processing-card__error">{pdfTask.error_message}</div> : null}
            </div>
          ) : null}
          {pdfSourceView === "comparison" && translationPreviewFile ? (
            <div className="knowledge-pdf-comparison">
              <div className="knowledge-pdf-comparison__pane">
                <span className="knowledge-pdf-comparison__label">普通版</span>
                <FileViewer
                  ref={fileViewerRef}
                  file={previewFile}
                  fileName={knowledgeDetail?.display_name || ""}
                  onExportReadyChange={setCanExportImagePdf}
                  onPdfSelection={askPdfSelection}
                  onPdfTranslateSelection={translatePdfSelection}
                  translationConfigured={translationConfigured}
                  pdfViewPosition={pdfViewPosition}
                  onPdfViewPositionChange={handlePdfViewPositionChange}
                />
              </div>
              <div className="knowledge-pdf-comparison__pane">
                <span className="knowledge-pdf-comparison__label">翻译版</span>
                <FileViewer
                  file={translationPreviewFile}
                  fileName={knowledgeDetail?.display_name || ""}
                  onPdfSelection={askPdfSelection}
                  onPdfTranslateSelection={translatePdfSelection}
                  translationConfigured={translationConfigured}
                  pdfViewPosition={pdfViewPosition}
                  onPdfViewPositionChange={handlePdfViewPositionChange}
                />
              </div>
            </div>
          ) : (
            <FileViewer
              ref={fileViewerRef}
              file={previewFile}
              fileName={knowledgeDetail?.display_name || ""}
              segment={segmentDetail}
              onExportReadyChange={setCanExportImagePdf}
              onPdfKindDetected={pdfSourceView === "original" ? handleOriginalPdfKindDetected : undefined}
              onImportReferenceSelection={importReferenceSelection}
              referenceActions={referenceActions}
              onPdfSelection={askPdfSelection}
              onPdfTranslateSelection={translatePdfSelection}
              onAddVocabularySelection={isVocabularyEnabled() ? (selection) => setVocabularySelection(selection) : undefined}
              translationConfigured={translationConfigured}
              learningSelectionActions={capabilityFamilies(learningCapabilities).map(family=>({key:family,label:t(capabilityFamilyI18nKey(family)),languages:Array.from(new Set(learningCapabilities.filter(item=>item.key!=="pinyin"&&family===capabilityFamily(item.key)).flatMap(item=>item.languages))),subjectKinds:Array.from(new Set(learningCapabilities.filter(item=>family===capabilityFamily(item.key)).flatMap(item=>item.subject_kinds))),disabled:!learningLocalAvailable,disabledTip:t("vocabulary.localOnlyDesktop")}))}
              onLearningSelection={(family,selection)=>{const capability=chooseFamilyCapability(learningCapabilities,family as CapabilityFamily,selection.text);if(capability)setLearningSelection({capabilityKey:capability.key,selection})}}
              paragraphSelectionMode={paragraphSelectionMode}
              onParagraphSelectionCancel={()=>setParagraphSelectionMode(false)}
              onParagraphSelectionConfirm={selections=>{setParagraphSelectionMode(false);setLearningAnalysisSelection({selections,requestId:Date.now()});setQuickReferenceOpen(true)}}
              pdfViewPosition={pdfViewPosition}
              onPdfViewPositionChange={handlePdfViewPositionChange}
            />
          )}
        </Col>
        <Col
          flex={previewSideCollapsed ? "0 0 48px" : "0 0 37.5%"}
          className="knowledge-preview-panel-column min-h-0 min-w-0"
        >
          <div
            style={{
              height: "100%",
              display: "flex",
              flexDirection: "column",
              overflow: "hidden",
              paddingBottom: "4px",
            }}
          >
            {knowledgeDetail ? (
              <>
                {pdfTask?.kind === "TRANSLATION_PDF" && pdfTask.status !== "READY" && !previewSideCollapsed ? (
                  <div className={`pdf-processing-card pdf-processing-card--translation is-${pdfTask.status.toLowerCase()}`}>
                    <div>
                      <strong>{pdfTask.status === "FAILED" ? "翻译失败" : pdfTask.status === "CANCELLED" ? "翻译已取消" : "正在翻译文档"}</strong>
                      <span>{pdfTaskDetail}</span>
                      {isActivePdfJob(pdfTask) ? <Button type="link" danger size="small" onClick={() => void cancelTranslationJob()}>取消</Button> : null}
                    </div>
                    <Progress percent={pdfTask.progress} status={pdfTask.status === "FAILED" ? "exception" : "active"} size="small" />
                    <small>{translationMode === "llm" ? "LazyMind 大模型" : "腾讯机器翻译 API"}</small>
                    {pdfTask.error_message ? <div className="pdf-processing-card__error">{pdfTask.error_message}</div> : null}
                  </div>
                ) : null}
                <div className={`knowledge-preview-side${previewSideCollapsed ? " is-collapsed" : ""}`}>
                  <Tabs
                    className="knowledge-preview-mode-tabs"
                    activeKey={previewSideTab}
                    onChange={setPreviewSideTab}
                    tabBarExtraContent={(
                      <div className="knowledge-preview-toolbar">
                        {previewSideTab === "segments" ? (
                          <Popover
                            trigger="click"
                            placement="bottomRight"
                            content={<div className="knowledge-preview-options-popover">
                            <Select
                              className="knowledge-preview-segment-select"
                              value={segmentViewKey || undefined}
                              options={segmentViewOptions}
                              onChange={setSegmentViewKey}
                            />
                            <div className="knowledge-preview-sequence">
                              <span>{t("knowledge.sequence")}</span>
                              <Switch
                                size="small"
                                checked={showSegmentSequence}
                                onChange={setShowSegmentSequence}
                              />
                            </div>
                            </div>}
                          >
                            <Button type="text" icon={<SettingOutlined />} aria-label="切片显示选项" title="切片显示选项" />
                          </Popover>
                        ) : previewSideTab === "chat" ? (
                          <Popover
                            trigger="click"
                            placement="bottomRight"
                            open={chatHistoryPopoverOpen}
                            onOpenChange={setChatHistoryPopoverOpen}
                            content={<Select
                              allowClear
                              className="knowledge-preview-chat-history-select"
                              placeholder={t("knowledge.pdfChatHistoryPlaceholder")}
                              value={selectedDocumentConversation}
                              options={documentChatHistory.map((conversation) => ({
                                value: conversation.conversation_id || "",
                                label: `${conversation.display_name || t("knowledge.pdfChatPanelLabel")} · ${moment(conversation.update_time).format("MM-DD HH:mm")}`,
                              })).filter((option) => Boolean(option.value))}
                              onChange={(value: string | undefined) => {
                                setSelectedDocumentConversation(value || undefined);
                                if (value) touchCachedPdfChat(knowledgeId, value);
                                setChatHistoryPopoverOpen(false);
                              }}
                            />}
                          >
                            <Button type="text" icon={<HistoryOutlined />} aria-label="选择历史对话" title="选择历史对话" />
                          </Popover>
                        ) : null}
                        <Button
                          type="text"
                          icon={<DoubleRightOutlined />}
                          aria-label={t("common.collapse")}
                          title={t("common.collapse")}
                          onClick={() => setPreviewSideCollapsed(true)}
                        />
                      </div>
                    )}
                    items={[
                      {
                        key: "chat",
                        label: t("knowledge.pdfChatTab"),
                        children: (
                          <PdfTemporaryChat
                            datasetId={knowledgeBaseId}
                            documentId={knowledgeId}
                            fileName={knowledgeDetail.display_name || ""}
                            selection={documentChatSelection || undefined}
                            translationRequest={translationRequest}
                            conversationToLoad={selectedDocumentConversation}
                            onConversationChange={setSelectedDocumentConversation}
                            onHistoryChange={refreshDocumentChatHistory}
                            onClose={() => {
                              setDocumentChatSelection(null);
                              setPreviewSideTab(canShowSegments ? "segments" : "chat");
                            }}
                          />
                        ),
                      },
                      ...(canShowSegments ? [{
                        key: "segments",
                        label: t("knowledge.segmentPreviewTab"),
                        children: (
                          <KnowledgeTabs
                            knowledgeDetail={knowledgeDetail}
                            onGetItemInfo={(data) => setSegmentDetail(data)}
                            onAskSegment={askSegment}
                            activeKey={segmentViewKey}
                            onActiveKeyChange={setSegmentViewKey}
                            onOptionsChange={handleSegmentViewOptionsChange}
                            showSequence={showSegmentSequence}
                          />
                        ),
                      }] : []),
                      ...(isVocabularyEnabled() ? [{
                        key: "vocabulary",
                        label: "生词",
                        children: <DocumentVocabularyPanel documentId={knowledgeId} refreshToken={vocabularyRefreshToken} />,
                      }] : []),
                      ...(referenceCount && referenceCount > 0 ? [{
                        key: "references",
                        label: "参考文献",
                        children: <DocumentReferencesPanel documentId={knowledgeId} targetDatasetId={knowledgeBaseId} importSelection={referenceImportSelection} onActionsChange={setReferenceActions} onReferenceCountChange={setReferenceCount} />,
                      }] : []),
                    ]}
                  />
                </div>
                {previewSideCollapsed ? (
                  <div className="knowledge-preview-collapsed">
                    <Button
                      type="text"
                      icon={<DoubleLeftOutlined />}
                      aria-label={t("common.expand")}
                      title={t("common.expand")}
                      onClick={() => setPreviewSideCollapsed(false)}
                    />
                  </div>
                ) : null}
              </>
            ) : null}
          </div>
        </Col>
      </Row>
      <Modal
        open={translationModalOpen}
        title="翻译文档"
        okText="开始翻译"
        cancelText="取消"
        onCancel={() => setTranslationModalOpen(false)}
        onOk={() => void runTranslationPdf()}
      >
        <div className="pdf-translation-config">
          <div><label>翻译方式</label><Segmented block value={translationMode} onChange={(value) => setTranslationMode(value as "api" | "llm")} options={[{ label: "翻译 API", value: "api" }, { label: "LazyMind 大模型", value: "llm" }]} /></div>
          <div><label>目标语言</label><Select value={translationTarget} onChange={setTranslationTarget} options={[{ label: "简体中文", value: "zh" }, { label: "英文", value: "en" }]} /></div>
          <p>翻译会保留支持格式的文档结构、表格和图片，仅替换可翻译文字。若原件是图片 PDF，会先生成并缓存文本 PDF。</p>
        </div>
      </Modal>
      <Modal
        className="knowledge-quick-reference-modal"
        open={quickReferenceOpen}
        title={t("learning.quickReference")}
        width={960}
        footer={null}
        destroyOnHidden={false}
        onCancel={() => setQuickReferenceOpen(false)}
      >
        <DocumentLearningPanel
          datasetId={knowledgeBaseId}
          documentId={knowledgeId}
          revision={knowledgeDetail?.update_time?.toString()}
          capabilities={learningCapabilities}
          localAvailable={learningLocalAvailable}
          analysisSelection={learningAnalysisSelection}
          onRequestParagraphSelection={()=>{setQuickReferenceOpen(false);setParagraphSelectionMode(true)}}
        />
      </Modal>
      <Modal
        open={Boolean(translationSource)}
        title={t("knowledge.translationTitle")}
        footer={null}
        onCancel={() => {
          if (!translationLoading) {
            setTranslationSource("");
            setTranslationResult("");
          }
        }}
      >
        <div className="knowledge-translation-block">
          <div className="knowledge-translation-label">{t("knowledge.translationOriginal")}</div>
          <div className="knowledge-translation-text">{translationSource}</div>
        </div>
        <div className="knowledge-translation-block">
          <div className="knowledge-translation-label">{t("knowledge.translationResult")}</div>
          {translationLoading ? <Spin size="small" /> : <div className="knowledge-translation-text">{translationResult}</div>}
        </div>
        {!translationLoading && translationResult ? (
          <div className="knowledge-translation-model-action">
            {isVocabularyEnabled() && isSingleEnglishWord(translationSource) ? (
              <Button className="knowledge-translation-add-button" onClick={() => {
                setVocabularySelection({ ...(translationSelection || { page: 1 }), text: translationSource });
                setTranslationSource("");
                setTranslationResult("");
              }}>{t("learning.addToCollection")}</Button>
            ) : null}
            <Button onClick={translateWithModel}>{t("knowledge.translateWithModel")}</Button>
            <span>{t("knowledge.translateWithModelHint")}</span>
          </div>
        ) : null}
      </Modal>
      {isVocabularyEnabled() ? <AddVocabularyModal
        selection={vocabularySelection}
        datasetId={knowledgeBaseId}
        documentId={knowledgeId}
        segmentId={segmentDetail?.segment_id}
        context={vocabularySelection?.context || segmentDetail?.content || vocabularySelection?.text || undefined}
        onClose={() => setVocabularySelection(null)}
        onAdded={() => { setVocabularyRefreshToken((value) => value + 1); setPreviewSideTab("vocabulary"); setPreviewSideCollapsed(false); }}
      /> : null}
      <AddLearningContentModal value={learningSelection} datasetId={knowledgeBaseId} documentId={knowledgeId} segmentId={segmentDetail?.segment_id} context={learningSelection?.selection.context||segmentDetail?.content} onClose={()=>setLearningSelection(null)} onAdded={()=>{setVocabularyRefreshToken(v=>v+1);setPreviewSideCollapsed(false)}} />
    </div>
  );
};

export default Detail;
