import { createPdfScrollSync } from "@/components/ui/pdfScrollSync";
import FileUtils from "@/modules/knowledge/utils/file";
import { TIME_FORMAT } from "@/modules/knowledge/constants/common";
import { Button, Input, message, Modal, Popover, Segmented, Tag, Tooltip, Select, Switch, Spin } from "antd";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useCallback } from "react";
import type { ChangeEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import {
  ArrowLeftOutlined,
  InfoCircleOutlined,
  EditOutlined,
  TranslationOutlined,
  MoreOutlined,
  PlusOutlined,
  CopyOutlined,
  CloseOutlined,
  FileImageOutlined,
  HistoryOutlined,
  MenuOutlined,
  MessageOutlined,
  SnippetsOutlined,
} from "@ant-design/icons";
import { isPaperLibrary } from "../../paper/store";
import moment from "moment";
import { Doc } from "@/api/generated/core-client";
import type { Conversation } from "@/api/generated/chatbot-client";
import { Segment } from "@/api/generated/knowledge-client";

import type { Dataset as KnowledgeDataset } from "@/api/generated/knowledge-client";
import FileViewer, {
  type FileViewerRef,
} from "@/modules/knowledge/components/FileViewer";
import KnowledgeTabs from "./components/KnowledgeTabs";
import PaperReaderDetails from "./components/PaperReaderDetails";
import { PaperPaneDivider } from "./components/PaperPaneDivider";
import PaperPanelLayoutMenu, {type PaperPanelLayout} from "./components/PaperPanelLayoutMenu";
import { PaperToolDock } from "./components/PaperToolDock";
import { PaperOutlineRail } from "./components/PaperOutlineRail";
import { PaperReaderSidebar } from "./components/PaperReaderSidebar";
import type { PdfIRDocument } from "@/modules/knowledge/api/pdfIR";
import {
  DocumentServiceApi,
  SegmentServiceApi,
  normalizeProxyableUrl,
} from "@/modules/knowledge/utils/request";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";
import { type PdfReferenceAction, type PdfTextSelection, type PdfViewPosition } from "@/components/ui";
import type {PdfNoteColor,PdfNoteHighlight} from '@/components/ui/pdfNoteHighlights';
import type {PdfNoteSource} from '@/modules/knowledge/paper/libraryModel';
import {listPdfDocumentHighlights,savePdfSelectionNote} from '@/modules/knowledge/paper/selectionNotes';
import type { DocumentChatSelection } from "@/modules/knowledge/components/PdfTemporaryChat/types";
import PdfTemporaryChat from "@/modules/knowledge/components/PdfTemporaryChat";
import type { PdfTemporaryChatHandle } from "@/modules/knowledge/components/PdfTemporaryChat";
import { readCachedPdfChat, touchCachedPdfChat } from "@/modules/knowledge/components/PdfTemporaryChat/cache";
import { localizeErrorCode } from "@/components/request";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import { TranslationUnavailableError } from "@/modules/knowledge/api/translation";
import { translationProviderDisplayName } from "@/modules/modelProvider/translationProviderNames";
import { getTranslationProgressLabel, resolveDocumentTranslationProvider, useDocumentTranslationService, usePdfSelectionTranslation } from "./translationUiState";
import {
  ensureDocumentParsed,
  createTranslationPdfJob,
  getPdfCapabilities,
  getPdfData,
  getPdfTranslationDraft,
  isActivePdfJob,
  latestActiveTranslationJob,
  latestTranslationJob,
  matchPdfTranslationDraftBlock,
  retranslatePdfDraftBlock,
  revisePdfTranslation,
  updatePdfRenderJob,
  type PdfArtifact,
  type PdfCapabilities,
  type PdfRenderJob,
  type PdfTranslationDraftBlock,
} from "@/modules/knowledge/api/pdfArtifacts";
import { translatableDocumentExtensions } from "@/modules/knowledge/utils/documentTranslation";
import AddVocabularyModal from "@/modules/vocabulary/AddVocabularyModal";
import DocumentVocabularyPanel from "@/modules/vocabulary/DocumentVocabularyPanel";
import { modelProvidersApi } from "@/modules/modelProvider/api";
import { isVocabularyEnabled } from "@/runtime/mode";
import AddLearningContentModal, { type LearningSelection } from "@/modules/learning/AddLearningContentModal";
import { getKnowledgeBaseCapabilities, getLearningCatalog, type LearningCapability } from "@/modules/learning/api";
import DocumentLearningPanel from "@/modules/learning/DocumentLearningPanel";
import DocumentReferencesPanel from "@/modules/knowledge/components/DocumentReferencesPanel";
import { extractAcademicReferences, listAcademicReferences } from "@/modules/knowledge/api/academicReferences";
import { capabilityFamilies, capabilityFamily, capabilityFamilyI18nKey, chooseFamilyCapability, type CapabilityFamily } from "@/modules/learning/capabilityFamilies";
import { normalizePdfSelectionText } from "@/components/ui/pdfSelectionText";
import FullDocumentTranslationPanel from "./FullDocumentTranslationPanel";
import PaperTranslationPanel from "./PaperTranslationPanel";
import PdfTranslationNotice, { createPdfTranslationProgress } from "./PdfTranslationNotice";
import {
  processingLevelSupportsSegments,
  type ProcessingLevel,
} from "@/modules/knowledge/utils/processingLevel";
import "./index.scss";
import "./paperWorkspace.scss";

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

const KnowledgeDocumentContent = () => {
  const { t, i18n } = useTranslation();
  const displayTranslationName = (name: string) => translationProviderDisplayName(name, i18n.resolvedLanguage || i18n.language || "zh-CN");
  const [loadedDocument, setLoadedDocument] = useState<{key:string;detail:KnowledgeDetail}>();
  const [documentLoadFailed, setDocumentLoadFailed] = useState(false);
  const [documentLoadAttempt, setDocumentLoadAttempt] = useState(0);
  const { knowledgeBaseId = "", knowledgeId = "" } = useParams();
  const documentKey=`${knowledgeBaseId}:${knowledgeId}`;
  const knowledgeDetail=loadedDocument?.key===documentKey?loadedDocument.detail:undefined;
  const currentDataset=useDatasetPermissionStore(state=>state.currentDataset);
  const requestedDatasetId=useDatasetPermissionStore(state=>state.requestedDatasetId);
  const datasetError=useDatasetPermissionStore(state=>state.datasetError);
  const documentExtension = (knowledgeDetail?.display_name || "").split(".").pop()?.toLowerCase() || "";
  const isPdfDocument = documentExtension === "pdf";
  const datasetIdentified=currentDataset?.dataset_id===knowledgeBaseId;
  const datasetFailed=requestedDatasetId===knowledgeBaseId&&Boolean(datasetError);
  // Wait for the library scene so a paper PDF never flashes the generic workspace.
  const documentReady=Boolean(knowledgeDetail)&&(!isPdfDocument||datasetIdentified);
  const isPaperDocument=isPdfDocument&&datasetIdentified&&isPaperLibrary(currentDataset?.scene);
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [segmentDetail, setSegmentDetail] = useState<Segment>();
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const fileViewerRef = useRef<FileViewerRef>(null);
  const paperChatRef = useRef<PdfTemporaryChatHandle>(null);
  const [canExportImagePdf, setCanExportImagePdf] = useState(false);
  const [exportingImagePdf, setExportingImagePdf] = useState(false);
  const [documentChatSelection, setDocumentChatSelection] =
    useState<DocumentChatSelection | null>(null);
  const [previewSideTab, setPreviewSideTab] = useState("chat");
  const [translationPanelMode, setTranslationPanelMode] = useState<"selection" | "document">("selection");
  const [paperDetailsOpened, setPaperDetailsOpened] = useState(false);
  useEffect(() => {if(previewSideTab === "info" || previewSideTab === "notes")setPaperDetailsOpened(true);}, [previewSideTab]);
  const [paperNavTab, setPaperNavTab] = useState("outline");
  const [panelLayout,setPanelLayout]=useState<PaperPanelLayout>('right');
  const [floatingPosition,setFloatingPosition]=useState({x:32,y:56});
  const panelRef=useRef<HTMLDivElement>(null);
  const panelDrag=useRef<{x:number;y:number;left:number;top:number}>();
  const [notesRevision,setNotesRevision]=useState(0);
  const [selectedNoteId,setSelectedNoteId]=useState<string>();
  const [paperNotesTab,setPaperNotesTab]=useState<"notes"|"vocabulary">("notes");
  const paperNotesLabel=isVocabularyEnabled()?(i18n.language.startsWith("zh")?"笔记和生词":"Notes & vocabulary"):(i18n.language.startsWith("zh")?"笔记":"Notes");
  const [savedNotes,setSavedNotes]=useState<{key:string;highlights:PdfNoteHighlight[]}>();
  const [paperAssistantWidth, setPaperAssistantWidth] = useState(() => {
    try { const saved = Number(localStorage.getItem("paper-reader:assistant-width")); return Number.isFinite(saved) && saved >= 320 ? saved : 400; } catch { return 400; }
  });
  const resizePaperAssistant = (width: number) => {
    setPaperAssistantWidth(width);
    try { localStorage.setItem("paper-reader:assistant-width", String(width)); } catch { /* Reading remains usable without storage. */ }
  };
  const [paperIR, setPaperIR] = useState<PdfIRDocument>();
  const [pdfNavigateTo, setPdfNavigateTo] = useState<{id:string;requestId:number}>();
  const [pdfNavigateToPage, setPdfNavigateToPage] = useState<{page:number;requestId:number}>();
  const [pdfNavigateToPosition,setPdfNavigateToPosition]=useState<{page:number;progress:number;requestId:number}>();
  useEffect(()=>{
    if(!isPaperDocument)return;
    const key=`${knowledgeBaseId}/${knowledgeId}`;
    let cancelled=false;
    void listPdfDocumentHighlights(knowledgeBaseId,knowledgeId).then(highlights=>{
      if(!cancelled)setSavedNotes({key,highlights});
    }).catch(()=>{/* Notes remain usable while the library request is unavailable. */});
    return()=>{cancelled=true;};
  },[isPaperDocument,knowledgeBaseId,knowledgeId,notesRevision]);
  const [nativePdfOutline,setNativePdfOutline]=useState<import('@/modules/knowledge/api/pdfOutline').PdfNativeOutlineItem[]>([]);
  const workspaceRef = useRef<HTMLDivElement>(null);
  const previewPaperAssistantWidth = useCallback((width: number | undefined) => {
    const element = workspaceRef.current;
    if (!element) return;
    if (width === undefined) {
      element.removeAttribute("data-pane-resizing");
      element.style.removeProperty("--paper-assistant-preview-width");
      return;
    }
    // Update layout through CSS without rerendering the whole workspace.
    // PDF viewers reuse their canvas and throttle geometry updates separately.
    element.setAttribute("data-pane-resizing", "true");
    element.style.setProperty("--paper-assistant-preview-width", `${width}px`);
  }, []);
  const navigationSequence = useRef(0);
  const [workspaceWidth, setWorkspaceWidth] = useState(1440);
  const [previewSideCollapsed, setPreviewSideCollapsed] = useState(false);
  const [activatedDocumentChatKey,setActivatedDocumentChatKey] = useState<string>();
  useEffect(() => {
    const element=workspaceRef.current;if(!element)return;
    let previous=1440;
    const update=(width:number)=>{
      if(width<=0)return;
      setWorkspaceWidth(width);
      if(width<900&&previous>=900)setPreviewSideCollapsed(true);
      previous=width;
    };
    update(element.getBoundingClientRect().width);
    if(typeof ResizeObserver==='undefined')return;
    const observer=new ResizeObserver(entries=>update(entries[0]?.contentRect.width||0));observer.observe(element);
    return()=>observer.disconnect();
  }, [documentReady]);
  useEffect(()=>{setPaperDetailsOpened(false);setPanelLayout("right");setPreviewSideTab("chat");setPaperIR(undefined);setNativePdfOutline([]);setPdfNavigateTo(undefined);setPdfNavigateToPage(undefined);setPdfNavigateToPosition(undefined);setPaperNavTab("outline");},[knowledgeBaseId,knowledgeId]);
  const navigatePaperPage=useCallback((page:number)=>{setPdfNavigateToPage({page,requestId:++navigationSequence.current});},[]);
  const navigatePaperBlock=useCallback((id:string)=>{
    setPdfNavigateTo({id,requestId:++navigationSequence.current});
  },[]);
  const [segmentViewKey, setSegmentViewKey] = useState("");
  const [segmentViewOptions, setSegmentViewOptions] = useState<
    Array<{ label: ReactNode; value: string }>
  >([]);
  const [showSegmentSequence, setShowSegmentSequence] = useState(true);
  const [documentChatHistory, setDocumentChatHistory] = useState<Conversation[]>([]);
  const [selectedDocumentConversation, setSelectedDocumentConversation] = useState<string>();
  const [chatHistoryPopoverOpen, setChatHistoryPopoverOpen] = useState(false);
  const {
    configured: translationConfigured,
    provider: translationProvider,
    providerGroupId: translationProviderGroupId,
    defaultProviderGroupId: defaultTranslationProviderGroupId,
    setProviderGroupId: setTranslationProviderGroupId,
    documentProviders: translationDocumentProviders,
    documentSupported: translationDocumentSupported,
    mode: translationMode,
    setMode: setTranslationMode,
    modalOpen: translationModalOpen,
    refreshing: translationStatusRefreshing,
    setModalOpen: setTranslationModalOpen,
    openModal: openTranslationModal,
    refreshStatus: refreshTranslationStatus,
  } = useDocumentTranslationService();
  const [translationProviderSaving, setTranslationProviderSaving] = useState(false);
  const selectDefaultTranslationProvider = async (groupId: string) => {
    setTranslationProviderSaving(true);
    try {
      await modelProvidersApi.apiCoreModelProvidersSelectedProvidersPut({ setSelectedProviderOpenAPIRequest: { selections: [{ category: "translation", group_id: groupId }] } });
      setTranslationProviderGroupId(groupId);
      await refreshTranslationStatus();
      clearSelectedTranslation();
    } catch {
      message.error("保存默认机器翻译源失败，请重试");
    } finally {
      setTranslationProviderSaving(false);
    }
  };
  const [translationSource, setTranslationSource] = useState("");
  const [vocabularyPhrase, setVocabularyPhrase] = useState<PdfTextSelection | null>(null);
  const [vocabularyWord, setVocabularyWord] = useState<string>();
  const [regenerationArtifact, setRegenerationArtifact] = useState<PdfArtifact>();
  const [regenerationProviderGroupId, setRegenerationProviderGroupId] = useState<string>();
  const translationDialogMode = regenerationArtifact
    ? regenerationArtifact.provider_type === "llm" ? "llm" : "api"
    : translationMode;
  const onTranslationError = useCallback((error: unknown) => {
    message.error(error instanceof TranslationUnavailableError && error.reason === "dictionary_not_found"
      ? t("knowledge.dictionaryNotFound")
      : error instanceof TranslationUnavailableError
        ? t("knowledge.translationConfigureTip")
        : t("knowledge.translationFailed"));
  }, [t]);
  const {
    loading: translationLoading,
    result: translationResult,
    translate: translateSelectedText,
    translateWithLLM: retranslateSelectedTextWithLLM,
    clear: clearSelectedTranslation,
  } = usePdfSelectionTranslation(onTranslationError);
  const [vocabularySelection, setVocabularySelection] = useState<PdfTextSelection | null>(null);
  const [vocabularyRefreshToken, setVocabularyRefreshToken] = useState(0);
  const handleVocabularyAdded=()=>{
    setVocabularyRefreshToken(value=>value+1);
    if(isVocabularyEnabled()){
      if(isPaperDocument){setPaperNotesTab("vocabulary");setPreviewSideTab("notes");}
      else{setPaperNavTab("vocabulary");setPreviewSideTab("navigation");}
    }
    setPreviewSideCollapsed(false);
  };
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
  const [revisionArtifact, setRevisionArtifact] = useState<PdfArtifact>();
  const [selectedTranslationArtifact, setSelectedTranslationArtifact] = useState<PdfArtifact>();
  const selectTranslationArtifact = useCallback((artifact: PdfArtifact) => {
    setSelectedTranslationArtifact(artifact);
  }, []);
  const showFullTranslation = useCallback(() => {
    setTranslationPanelMode("document");
    setPreviewSideTab("translation");
    setPreviewSideCollapsed(false);
  }, []);
  const sidebarTranslation = selectedTranslationArtifact
    ?? pdfCapabilities?.translations[pdfCapabilities.translations.length - 1];
  const [pdfViewPosition, setPdfViewPosition] = useState<PdfViewPosition>({ page: 1, progress: 0 });
  const [pdfTask, setPdfTask] = useState<PdfRenderJob>();
  const [pdfProgress] = useState(createPdfTranslationProgress);
  const [scrollSync] = useState(createPdfScrollSync);
  const [syncScroll, setSyncScroll] = useState(false);
  const toggleSyncScroll = useCallback((enabled: boolean) => {
    scrollSync.setEnabled(enabled);
    setSyncScroll(enabled);
  }, [scrollSync]);
  const [pdfTaskDetail, setPdfTaskDetail] = useState("");
  const [translationPreparing, setTranslationPreparing] = useState(false);
  const [translationTarget, setTranslationTarget] = useState("zh");
  const [revisionBlock, setRevisionBlock] = useState<PdfTranslationDraftBlock>();
  const [revisionText, setRevisionText] = useState("");
  const [revisionProvider, setRevisionProvider] = useState<{ provider_type: "api" | "llm"; provider: string }>();
  const [revisionModalOpen, setRevisionModalOpen] = useState(false);
  const [revisionLoading, setRevisionLoading] = useState(false);
  const [revisionTranslatingWith, setRevisionTranslatingWith] = useState<"api" | "llm">();
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
    } catch {
      setPdfCapabilities(undefined);
    }
  }, [knowledgeBaseId, knowledgeId]);

  useEffect(() => { void refreshPdfCapabilities(); }, [refreshPdfCapabilities]);

  useEffect(() => {
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
    // The reader already owns pixel-accurate progress. Feeding every progress
    // update back through this large workspace rerenders the PDF and every
    // assistant pane. React state is only needed when the outline page changes;
    // the exact position is still persisted for a later reopen.
    setPdfViewPosition(current => current?.page === position.page ? current : position);
    try {
      sessionStorage.setItem(`lazymind:pdf-position:${knowledgeId}`, JSON.stringify(position));
    } catch {
      // Reading progress persistence is best effort.
    }
  }, [knowledgeId]);


  useEffect(()=>{ if(!knowledgeBaseId)return; Promise.all([getLearningCatalog(),getKnowledgeBaseCapabilities(knowledgeBaseId)]).then(([catalog,configured])=>{const enabled=new Set(configured.filter(x=>x.enabled).map(x=>x.capability_key));setLearningCapabilities(catalog.capabilities.filter(x=>enabled.has(x.key)));setLearningLocalAvailable(catalog.local_available)}).catch(()=>setLearningCapabilities([])); },[knowledgeBaseId]);

  useEffect(()=>setLearningAnalysisSelection(undefined),[knowledgeId]);

  useEffect(() => {
    setTranslationSource("");
    clearSelectedTranslation();
  }, [knowledgeId, clearSelectedTranslation]);

  useEffect(() => {
    referencePreanalysisDocument.current = "";
    setReferencePreanalysisRetry(0);
    setReferenceImportSelection(undefined);
    setReferenceActions([]);
    setReferenceCount(undefined);
  }, [knowledgeId]);


  const translatePdfSelection = useCallback(async (selection: PdfTextSelection) => {
    const text = normalizePdfSelectionText(selection.text);
    setTranslationSource(text);

    setTranslationPanelMode("selection");
    setPreviewSideTab("translation");
    setPreviewSideCollapsed(false);
    await translateSelectedText(text);
  }, [translateSelectedText]);

  const translateTypedText = useCallback((value: string) => {
    const text = value.trim();
    setTranslationSource(text);
    void translateSelectedText(text);
  }, [translateSelectedText]);

  const addVocabularySelection = useCallback((selection: PdfTextSelection) => {
    if (isSingleEnglishWord(selection.text)) {
      setVocabularySelection(selection);
      return;
    }
    const words = Array.from(new Set(selection.text.match(/[A-Za-z][A-Za-z'-]*/g) || []));
    if (!words.length) return;
    setVocabularyPhrase(selection);
    setVocabularyWord(words[0]);
  }, []);

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

  const askPdfSelection = useCallback((selection: PdfTextSelection & Pick<DocumentChatSelection, "quickExplain">) => {

    setActivatedDocumentChatKey(documentKey);
    setPreviewSideTab("chat");
    setDocumentChatSelection({ source: "pdf", ...selection });
    setPreviewSideCollapsed(false);
  }, [documentKey]);

  const notePdfSelection=useCallback(async(selection:PdfTextSelection,note:string,color:PdfNoteColor,id:string)=>{
    const source:Omit<PdfNoteSource,'attachmentId'>={page:selection.page,color,quote:selection.text,
      ...(selection.rects?.length?{rects:selection.rects}:{}),
      ...(selection.bbox?{bbox:selection.bbox}:{})};
    await savePdfSelectionNote(knowledgeBaseId,knowledgeId,note,source,id);
    const key=`${knowledgeBaseId}/${knowledgeId}`;
    if(selection.rects?.length)setSavedNotes(current=>({key,highlights:[...(current?.key===key?current.highlights.filter(item=>item.id!==id):[]),{id,page:selection.page,color,rects:selection.rects!}]}));
    setNotesRevision(value=>value+1);
    message.success(i18n.language.startsWith('zh')?'笔记已保存':'Note saved');
  },[knowledgeBaseId,knowledgeId,i18n.language]);
  const choosePanelLayout=(mode:PaperPanelLayout)=>{
    setPanelLayout(mode);setPreviewSideCollapsed(false);
    if(mode==='floating')setFloatingPosition({x:Math.max(8,workspaceWidth-Math.min(paperAssistantWidth,workspaceWidth-72)-72),y:56});
  };
  const moveFloatingPanel=(x:number,y:number)=>{
    const bounds=workspaceRef.current?.getBoundingClientRect();const panel=panelRef.current?.getBoundingClientRect();
    if(!bounds||!panel)return;
    setFloatingPosition({x:Math.min(Math.max(8,x),Math.max(8,bounds.width-panel.width-8)),y:Math.min(Math.max(8,y),Math.max(8,bounds.height-panel.height-8))});
  };

  const importReferenceSelection = useCallback((selection: PdfTextSelection) => {

    setReferenceImportSelection({ text: selection.text, referenceId: selection.referenceId, requestId: Date.now() });
    setPreviewSideCollapsed(false);
    setPreviewSideTab("navigation");
    setPaperNavTab("references");
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


  const handleSegmentViewOptionsChange = useCallback(
    (options: Array<{ label: ReactNode; value: string }>) => {
      setSegmentViewOptions(options);
    },
    [],
  );

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
    setPreviewSideTab("chat");
    setActivatedDocumentChatKey(documentKey);
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
  }, [documentKey]);

  const {
    getDatasetDetail: getKbDetail,
    loadDataset,
  } = useDatasetPermissionStore();


  const hasWritePermission = useDatasetPermissionStore((state) => state.hasWritePermission());

  const group = useMemo(() => {
    return searchParams.get("group_name") || "";
  }, [searchParams]);

  const segmentId = useMemo(() => {
    return searchParams.get("segement_id") || "";
  }, [searchParams]);

  useEffect(() => {
    let cancelled = false;
    setDocumentLoadFailed(false);
    DocumentServiceApi()
      .documentServiceGetDocument({dataset: knowledgeBaseId, document: knowledgeId})
      .then((res) => {
        if (!res.data) throw new Error("Missing document metadata");
        if (!cancelled) setLoadedDocument({key:documentKey,detail:res.data});
      })
      .catch(() => { if (!cancelled) setDocumentLoadFailed(true); });
    void loadDataset(knowledgeBaseId).then(dataset => {
      if (!cancelled) setProcessingLevel((dataset as KnowledgeDatasetWithProcessingLevel).processing_level || "indexed");
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [documentKey, documentLoadAttempt, knowledgeBaseId, knowledgeId, loadDataset]);

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
    const filePath = /\.pdf$/i.test(knowledgeDetail?.display_name || "")
      ? knowledgeDetail?.download_file_url || knowledgeDetail?.file_url
      : knowledgeDetail?.file_url;
    if (!filePath) {
      return "";
    }

    const fileUrl = `${window.location.origin}/api/core${filePath}`;
    return normalizeProxyableUrl(fileUrl);
  }, [knowledgeDetail?.display_name, knowledgeDetail?.download_file_url, knowledgeDetail?.file_url]);

  const previewFile = originalPreviewFile;

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

  const runTranslationPdf = useCallback(async (force = false, basis?: PdfArtifact, requestedGroupId?: string) => {
    if (translationPreparing) return;
    const providerType = basis ? basis.provider_type === "llm" ? "llm" : "api" : translationMode;
    const selectedAPI = providerType === "api"
      ? resolveDocumentTranslationProvider(translationDocumentProviders, basis ? requestedGroupId : translationProviderGroupId)
      : undefined;
    if (providerType === "api" && !selectedAPI) {
      message.error(basis
        ? "请先选择可用的翻译 API 来源，再重新生成译本"
        : "所选翻译 API 已不可用，请在默认模型中重新配置");
      return;
    }
    setTranslationModalOpen(false);
    setRegenerationArtifact(undefined);
    setTranslationPreparing(true);
    setPdfTaskDetail("正在准备翻译任务");
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
          if (parsed.status === "parsed") {
            setPdfTaskDetail("文档解析完成，正在创建翻译任务");
            break;
          }
          setPdfTaskDetail("文档解析中，完成后将自动继续翻译");
          if (attempt === 149) throw new Error("文档 Reader 解析超时");
          await wait(2000);
        }
      }
    } catch (error) {
      const detail = error instanceof Error ? error.message : "无法读取 PDF 文本层";
      setPdfTaskDetail("");
      setTranslationPreparing(false);
      message.error(detail);
      return;
    }
    const targetLanguage = basis?.target_language || translationTarget;
    const provider = providerType === "api"
      ? selectedAPI!.provider_name
      : basis?.provider || "LazyMind LLM";
    try {
      const created = await createTranslationPdfJob(knowledgeBaseId, knowledgeId, {
        target_language: targetLanguage,
        provider_type: providerType,
        provider,
        provider_group_id: selectedAPI?.group_id,
        model: basis?.model || (providerType === "llm" ? "document-context" : undefined),
        options_hash: layoutSource,
        force,
        source: new Blob([source]),
        source_filename: filename,
      });
      if (created.artifact) {
        selectTranslationArtifact(created.artifact);
        showFullTranslation();
        setPdfTaskDetail("");
        message.success("已加载缓存译本");
        return;
      }
      const job = created.job;
      if (!job) throw new Error("后端未返回翻译任务");
      cancelledPdfJobsRef.current.delete(job.id);
      setPdfTask({ ...job, status: "RUNNING", stage: "TRANSLATING", progress: 3 });
      setPdfTaskDetail("任务已提交到后端，可关闭或刷新页面");
      await refreshPdfCapabilities();
    } catch (error) {
      const detail = error instanceof Error ? error.message : "创建翻译任务失败";
      setPdfTaskDetail("");
      message.error(detail);
    } finally {
      setTranslationPreparing(false);
    }
  }, [knowledgeBaseId, knowledgeDetail?.display_name, knowledgeId, originalPreviewFile, refreshPdfCapabilities, showFullTranslation, selectTranslationArtifact, translationDocumentProviders, translationMode, translationPreparing, translationProvider, translationProviderGroupId, translationTarget]);

  const openTranslationRegeneration = useCallback((artifact: PdfArtifact) => {
    setRegenerationArtifact(artifact);
    setRegenerationProviderGroupId(undefined);
    setTranslationTarget(artifact.target_language || "zh");
    setTranslationMode(artifact.provider_type === "llm" ? "llm" : "api");
    openTranslationModal();
  }, [openTranslationModal, setTranslationMode]);

  const openNewTranslation = useCallback(() => {
    setRegenerationArtifact(undefined);
    setRegenerationProviderGroupId(undefined);
    openTranslationModal();
  }, [openTranslationModal]);

  const cancelTranslationJob = useCallback(async () => {
    const job = pdfTask?.kind === "TRANSLATION_PDF" && isActivePdfJob(pdfTask)
      ? pdfTask
      : latestActiveTranslationJob(pdfCapabilities?.jobs || []);
    if (!job || cancelledPdfJobsRef.current.has(job.id)) return;
    cancelledPdfJobsRef.current.add(job.id);
    try {
      await updatePdfRenderJob(knowledgeBaseId, knowledgeId, job.id, {
        status: "CANCELLED",
        stage: "CANCELLED",
        progress: job.progress,
        error_message: "用户已取消翻译",
      });
    } catch (error) {
      // A rejected cancellation must not suppress subsequent backend progress.
      cancelledPdfJobsRef.current.delete(job.id);
      message.error(error instanceof Error ? error.message : "取消翻译失败，请重试");
      return;
    }
    setPdfTask({ ...job, status: "CANCELLED", stage: "CANCELLED", error_message: "用户已取消翻译" });
    await refreshPdfCapabilities();
    message.success("翻译任务已取消");
  }, [knowledgeBaseId, knowledgeId, pdfCapabilities?.jobs, pdfTask, refreshPdfCapabilities]);

  const openTranslationRevision = useCallback(async (selection: PdfTextSelection, textKind: "source" | "translation" = "translation") => {
    const translationArtifact = sidebarTranslation;
    if (!translationArtifact?.has_draft) {
      message.warning("这个译本没有可编辑草稿，请重新生成译本后再试");
      return;
    }
    try {
      const draft = await getPdfTranslationDraft(knowledgeBaseId, knowledgeId, translationArtifact.id);
      const block = matchPdfTranslationDraftBlock(draft.blocks, selection, textKind);
      if (!block) {
        message.warning(`未能定位所选${textKind === "source" ? "原文" : "译文"}对应的翻译段落，请重新选择完整段落`);
        return;
      }
      setRevisionArtifact(translationArtifact);
      setRevisionBlock(block);
      setRevisionText(block.translated_text);
      setRevisionProvider(undefined);
      setRevisionModalOpen(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : "读取翻译草稿失败");
    }
  }, [knowledgeBaseId, knowledgeId, sidebarTranslation]);

  const retranslateRevisionBlock = useCallback(async (providerType: "api" | "llm") => {
    if (!revisionArtifact || !revisionBlock) return;
    setRevisionLoading(true);
    setRevisionTranslatingWith(providerType);
    try {
      const result = await retranslatePdfDraftBlock(knowledgeBaseId, knowledgeId, revisionArtifact.id, {
        block_id: revisionBlock.id,
        provider_type: providerType,
      });
      setRevisionText(result.translated_text);
      setRevisionProvider({ provider_type: result.provider_type, provider: result.provider });
    } catch (error) {
      message.error(error instanceof Error ? error.message : "重新翻译失败");
    } finally {
      setRevisionLoading(false);
      setRevisionTranslatingWith(undefined);
    }
  }, [knowledgeBaseId, knowledgeId, revisionBlock, revisionArtifact]);

  const confirmTranslationRevision = useCallback(async () => {
    if (!revisionArtifact || !revisionBlock || !revisionText.trim()) return;
    setRevisionLoading(true);
    try {
      const artifact = await revisePdfTranslation(knowledgeBaseId, knowledgeId, revisionArtifact.id, {
        [revisionBlock.id]: revisionText.trim(),
      }, revisionProvider);
      await refreshPdfCapabilities();
      selectTranslationArtifact(artifact);
      showFullTranslation();
      setRevisionModalOpen(false);
      message.success("已更新当前译本");
    } catch (error) {
      message.error(error instanceof Error ? error.message : "生成新版译本失败");
    } finally {
      setRevisionLoading(false);
    }
  }, [knowledgeBaseId, knowledgeId, refreshPdfCapabilities, revisionBlock, revisionProvider, revisionText, revisionArtifact, showFullTranslation, selectTranslationArtifact]);

  useEffect(() => {
    const activeJob = latestActiveTranslationJob(pdfCapabilities?.jobs || []);
    if (!activeJob) return;
    setPdfTask(activeJob);
    setPdfTaskDetail("后端正在执行，刷新或关闭页面不会中断");
    let disposed = false;
    let inFlight = false;
    const poll = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const capabilities = await getPdfCapabilities(knowledgeBaseId, knowledgeId);
        if (disposed || cancelledPdfJobsRef.current.has(activeJob.id)) return;
        const updated = capabilities.jobs.find((job) => job.id === activeJob.id);
        if (!updated) return;
        pdfProgress.update(updated);
        // Only terminal transitions change the workspace and selected artifact.
        if (isActivePdfJob(updated)) return;
        setPdfCapabilities(capabilities);
        setPdfTask(updated);
        if (updated.status === "READY") {
          const artifact = capabilities.translations.find((item) => item.id === updated.artifact_id);
          if (artifact) {
            selectTranslationArtifact(artifact);
            showFullTranslation();
          }
          message.success("翻译完成并已缓存");
        } else if (updated.status === "FAILED") {
          message.error(updated.error_message || "文档翻译失败");
        }
      } catch { /* Keep the persisted task running and retry on the next poll. */ }
      finally { inFlight = false; }
    };
    const timer = window.setInterval(() => { void poll(); }, 1500);
    return () => { disposed = true; window.clearInterval(timer); };
  }, [knowledgeBaseId, knowledgeId, pdfCapabilities?.jobs, pdfProgress, showFullTranslation, selectTranslationArtifact]);


  useEffect(() => { setPaperNavTab(isPaperDocument?"outline":"references");  }, [isPaperDocument, knowledgeId]);
  useLayoutEffect(() => {
    if (!documentReady) return;
    const width = workspaceRef.current?.getBoundingClientRect().width || 1440;
    setPreviewSideCollapsed(!isPaperDocument || width < 900);
  }, [documentKey,documentReady,isPaperDocument]);
  const canTranslateDocument = translatableDocumentExtensions.includes(documentExtension);
  const translationInProgress = translationPreparing || Boolean(pdfTask?.kind === "TRANSLATION_PDF" && isActivePdfJob(pdfTask)) ||
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
    t,
  ]);

  const documentMetadata = [
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
                href={isPdfDocument ? originalPreviewFile : previewFile}
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
        ];

  const readerBackButton = <button className="paper-library-back" type="button" onClick={()=>navigate(`/lib/knowledge/detail/${knowledgeBaseId}${["aiwrite","aireview","chat"].includes(searchParams.get("from")||"")?"?from=aiwrite":""}`)}><ArrowLeftOutlined/><span>{isPaperDocument ? (i18n.language.startsWith("zh")?"论文集":"Paper collection") : (getKbDetail()?.display_name || t("layout.knowledgeBase"))}</span></button>;
  const readerDocumentActions = (readingTools?: React.ReactNode) => <div className="paper-workspace-header-actions">
    <Popover trigger="click" placement="bottomRight" overlayClassName="paper-document-actions-popover" content={<div className="paper-document-actions">
      <strong>{knowledgeDetail?.display_name}</strong>
      <div>{pageTitle}</div>
      {readingTools}
      {!isPaperDocument && <dl className="paper-document-metadata">{documentMetadata.filter(item=>!item.hidden).map(item=><div key={item.label}><dt>{item.label}</dt><dd>{item.value}</dd></div>)}</dl>}
      {developerActive && <div><code>{knowledgeId}</code><Button type="text" icon={<CopyOutlined/>} aria-label={t("common.copy")} onClick={async()=>{try{await writeTextToClipboard(knowledgeId);message.success(t("knowledge.copySuccess"));}catch{message.error(t("knowledge.copyFailedManual"));}}}/></div>}
    </div>}><Button type="text" icon={<MoreOutlined/>} aria-label={i18n.language.startsWith("zh")?"文档操作":"Document actions"}/></Popover>
  </div>;

  // Unknown file types must not fall through to the legacy non-PDF layout.
  if (!documentReady) {
    const zh = i18n.language.startsWith("zh");
    return <div className="paper-document-loading">
      <div className="paper-document-loading-surface">
        <Button type="text" icon={<ArrowLeftOutlined/>} onClick={() => navigate(`/lib/knowledge/detail/${knowledgeBaseId}`)}>{zh ? "返回" : "Back"}</Button>
        <div className="paper-document-loading-status" role={documentLoadFailed || datasetFailed ? "alert" : "status"}>
          {documentLoadFailed || datasetFailed ? <>
            <span>{datasetFailed && !documentLoadFailed ? (zh ? "资料库加载失败，请重试" : "Could not load the library. Please retry.") : (zh ? "文档加载失败，请重试" : "Could not load the document. Please retry.")}</span>
            <Button onClick={() => setDocumentLoadAttempt(value => value + 1)}>{zh ? "重试" : "Retry"}</Button>
          </> : <><Spin/><span>{zh ? "正在加载文档…" : "Loading document…"}</span></>}
        </div>
      </div>
    </div>;
  }

  return (
    <div className={`knowledge-container is-document-reader !h-full !items-start${isPaperDocument ? " is-paper-document" : ""}`}>
      <div ref={workspaceRef} className="knowledge-preview-layout knowledge-paper-workspace" data-panel-layout={panelLayout} data-panel-open={!previewSideCollapsed} data-narrow={workspaceWidth<1100} data-compact={workspaceWidth<900} style={{"--paper-assistant-width": `${Math.min(paperAssistantWidth, Math.max(320, workspaceWidth * .5))}px`} as React.CSSProperties}>
        <div className="knowledge-preview-file-column paper-reader-pane">
          {!isPdfDocument && <header className="document-reader-header">{readerBackButton}<span title={knowledgeDetail?.display_name}>{knowledgeDetail?.display_name}</span>{readerDocumentActions()}</header>}
          {isPdfDocument && <PaperOutlineRail document={paperIR} nativeOutline={nativePdfOutline} currentPage={pdfViewPosition.page} onNavigate={navigatePaperBlock} onNavigatePage={navigatePaperPage}/> }
            <FileViewer
              ref={fileViewerRef}
              pdfScrollSync={scrollSync.source}
              paperMode={isPaperDocument}
              pdfHeaderStart={readerBackButton}
              pdfHeaderEnd={readerDocumentActions}
              pdfIRTarget={isPaperDocument ? {dataset: knowledgeBaseId, document: knowledgeId, canWrite: hasWritePermission} : undefined}
              onPdfIRDocumentChange={setPaperIR}
              onPdfOutlineChange={setNativePdfOutline}
              pdfNavigateTo={pdfNavigateTo}
              pdfNavigateToPage={pdfNavigateToPage}
              pdfNavigateToPosition={pdfNavigateToPosition}
              pdfNoteHighlights={savedNotes?.key===`${knowledgeBaseId}/${knowledgeId}`?savedNotes.highlights:[]}
              onPdfNoteHighlightClick={id=>{setPaperNotesTab('notes');setSelectedNoteId(id);setPreviewSideTab('notes');setPreviewSideCollapsed(false);}}
              file={previewFile}
              fileName={knowledgeDetail?.display_name || ""}
              segment={segmentDetail}
              onExportReadyChange={setCanExportImagePdf}
              onImportReferenceSelection={!isPaperDocument ? importReferenceSelection : undefined}
              referenceActions={referenceActions}
              onPdfNoteSelection={isPaperDocument && hasWritePermission ? notePdfSelection : undefined}
              onPdfSelection={askPdfSelection}
              onPdfTranslateSelection={translatePdfSelection}
              onPdfRetranslateSelection={pdfCapabilities?.translations.length
                ? (selection) => void openTranslationRevision(selection, "source")
                : undefined}
              onAddVocabularySelection={isVocabularyEnabled() ? addVocabularySelection : undefined}
              translationConfigured={translationConfigured}
              learningSelectionActions={[
                ...(isPaperDocument ? [{key:"explanation",label:t("knowledge.pdfQuickExplain")}] : []),
                ...capabilityFamilies(learningCapabilities)
                  .filter(family=>!isPaperDocument||family!=="explanation")
                  .map(family=>({key:family,label:t(capabilityFamilyI18nKey(family)),languages:Array.from(new Set(learningCapabilities.filter(item=>item.key!=="pinyin"&&family===capabilityFamily(item.key)).flatMap(item=>item.languages))),subjectKinds:Array.from(new Set(learningCapabilities.filter(item=>family===capabilityFamily(item.key)).flatMap(item=>item.subject_kinds))),disabled:!learningLocalAvailable,disabledTip:t("vocabulary.localOnlyDesktop")})),
              ]}
              onLearningSelection={(family,selection)=>{
                if(isPaperDocument&&family==="explanation"){
                  askPdfSelection({...selection,question:t("knowledge.pdfQuickExplainPrompt"),quickExplain:true});
                  return;
                }
                const capability=chooseFamilyCapability(learningCapabilities,family as CapabilityFamily,selection.text);
                if(capability)setLearningSelection({capabilityKey:capability.key,selection});
              }}
              paragraphSelectionMode={paragraphSelectionMode}
              onParagraphSelectionCancel={()=>setParagraphSelectionMode(false)}
              onParagraphSelectionConfirm={selections=>{setParagraphSelectionMode(false);setLearningAnalysisSelection({selections,requestId:Date.now()});setQuickReferenceOpen(true)}}
              pdfViewPosition={pdfViewPosition}
              onPdfViewPositionChange={handlePdfViewPositionChange}
            />

        </div>
        {!previewSideCollapsed && (panelLayout==="left" || panelLayout==="right") && workspaceWidth >= 900 && <PaperPaneDivider side={panelLayout==="left"?"left":"right"} width={Math.min(paperAssistantWidth, Math.max(320,workspaceWidth*.5))} maxWidth={Math.max(320,workspaceWidth*.5)} onChange={resizePaperAssistant} onPreview={previewPaperAssistantWidth} label={i18n.language.startsWith("zh") ? "调整助手宽度" : "Resize assistant"} />}
        <div ref={panelRef} style={panelLayout==="floating"?{"--paper-panel-x":`${floatingPosition.x}px`,"--paper-panel-y":`${floatingPosition.y}px`} as React.CSSProperties:undefined} className={`paper-chat-pane knowledge-preview-panel-column${previewSideCollapsed?" is-collapsed":""}`} aria-hidden={previewSideCollapsed}>
          {knowledgeDetail&&<>
            <div className="paper-chat-open-content">
              <div className="paper-chat-toolbar" tabIndex={panelLayout==='floating'?0:undefined} aria-label={panelLayout==='floating'?(i18n.language.startsWith("zh")?"移动浮动面板":"Move floating panel"):undefined}
                onPointerDown={event=>{if(panelLayout!=='floating'||event.button!==0||!event.currentTarget.contains(event.target as Node)||(event.target as HTMLElement).closest('button,input,select,.ant-segmented'))return;panelDrag.current={x:event.clientX,y:event.clientY,left:floatingPosition.x,top:floatingPosition.y};event.currentTarget.setPointerCapture(event.pointerId);}}
                onPointerMove={event=>{const start=panelDrag.current;if(start)moveFloatingPanel(start.left+event.clientX-start.x,start.top+event.clientY-start.y);}}
                onPointerUp={()=>{panelDrag.current=undefined;}} onPointerCancel={()=>{panelDrag.current=undefined;}} onLostPointerCapture={()=>{panelDrag.current=undefined;}}
                onKeyDown={event=>{if(panelLayout!=='floating'||event.target!==event.currentTarget)return;const offset=event.key==='ArrowLeft'?[-24,0]:event.key==='ArrowRight'?[24,0]:event.key==='ArrowUp'?[0,-24]:event.key==='ArrowDown'?[0,24]:undefined;if(offset){event.preventDefault();moveFloatingPanel(floatingPosition.x+offset[0],floatingPosition.y+offset[1]);}}}
              ><strong>{previewSideTab==="navigation"?<MenuOutlined/>:previewSideTab==="translation"?<TranslationOutlined/>:previewSideTab==="info"?<InfoCircleOutlined/>:previewSideTab==="notes"?<EditOutlined/>:<MessageOutlined/>} {previewSideTab==="navigation" ? t("paperShell.documentNavigation") : previewSideTab==="translation" ? (i18n.language.startsWith("zh") ? "翻译" : "Translation") : previewSideTab==="info" ? (i18n.language.startsWith("zh") ? "论文信息" : "Paper information") : previewSideTab==="notes" ? paperNotesLabel : isPaperDocument?t("paperShell.assistant",{defaultValue:"Paper assistant"}):t("paperShell.documentAssistant",{defaultValue:"Document assistant"})}</strong>{previewSideTab === "translation" && (<div className="knowledge-translation-modes knowledge-translation-modes--header"><Segmented size="small" value={translationPanelMode} options={[{label:"划词翻译",value:"selection"},{label:"全文翻译",value:"document"}]} onChange={(value: string | number) => {
                  if (value === "document") showFullTranslation();
                  else setTranslationPanelMode("selection");
                }} /></div>)}<div className="paper-chat-toolbar-actions">
                {previewSideTab === "chat" && <Button type="text" icon={<PlusOutlined/>} aria-label={t("knowledge.pdfChatNew")} onClick={()=>paperChatRef.current?.startNewConversation()}/>}
                {previewSideTab === "chat" && <Popover trigger="click" placement="bottomRight" open={chatHistoryPopoverOpen} onOpenChange={setChatHistoryPopoverOpen} content={<Select allowClear className="knowledge-preview-chat-history-select" placeholder={t("knowledge.pdfChatHistoryPlaceholder")} value={selectedDocumentConversation} options={documentChatHistory.map(conversation=>({value:conversation.conversation_id||"",label:`${conversation.display_name||t("knowledge.pdfChatPanelLabel")} · ${moment(conversation.update_time).format("MM-DD HH:mm")}`})).filter(option=>Boolean(option.value))} onChange={(value:string|undefined)=>{setSelectedDocumentConversation(value||undefined);if(value)touchCachedPdfChat(knowledgeId,value);setChatHistoryPopoverOpen(false);}}/>}>
                  <Button type="text" icon={<HistoryOutlined/>} aria-label={t("paperShell.history",{defaultValue:"Conversation history"})}/>
                </Popover>}
                <PaperPanelLayoutMenu value={panelLayout} onChange={choosePanelLayout}/><Button type="text" icon={<CloseOutlined/>} aria-label={isPaperDocument?t("paperShell.collapseChat",{defaultValue:"Collapse assistant"}):t("paperShell.collapseDocumentAssistant",{defaultValue:"Collapse document assistant"})} onClick={()=>setPreviewSideCollapsed(true)}/>
              </div></div>
              {!isPaperDocument && <div className="paper-assistant-pane document-navigation-pane" hidden={previewSideTab !== "navigation"}>
          <PaperReaderSidebar paperMode={datasetIdentified&&isPaperLibrary(currentDataset?.scene)} currentPage={pdfViewPosition.page} isPdf={isPdfDocument} document={paperIR} nativeOutline={nativePdfOutline} activeTab={paperNavTab} onTabChange={setPaperNavTab} onNavigate={navigatePaperBlock} onNavigatePage={navigatePaperPage} onCollapse={()=>setPreviewSideCollapsed(true)} referenceCount={referenceCount}
            references={<DocumentReferencesPanel documentId={knowledgeId} targetDatasetId={knowledgeBaseId} importSelection={referenceImportSelection} onActionsChange={setReferenceActions} onReferenceCountChange={setReferenceCount}/>}
            vocabulary={isVocabularyEnabled()?<DocumentVocabularyPanel documentId={knowledgeId} refreshToken={vocabularyRefreshToken}/>:undefined}
            segments={canShowSegments&&knowledgeDetail?<><div className="paper-segment-options"><Select value={segmentViewKey||undefined} options={segmentViewOptions} onChange={setSegmentViewKey}/><label>{t("knowledge.sequence")} <Switch size="small" checked={showSegmentSequence} onChange={setShowSegmentSequence}/></label></div><KnowledgeTabs knowledgeDetail={knowledgeDetail} onGetItemInfo={setSegmentDetail} onAskSegment={askSegment} activeKey={segmentViewKey} onActiveKeyChange={setSegmentViewKey} onOptionsChange={handleSegmentViewOptionsChange} showSequence={showSegmentSequence}/></>:undefined}
          />
              </div>}
              <div className="paper-assistant-pane" hidden={previewSideTab !== "translation"}>
                <div className="paper-assistant-pane" hidden={translationPanelMode !== "selection"}>
                <PaperTranslationPanel source={translationSource} result={translationResult} loading={translationLoading} providers={translationDocumentProviders} providerGroupId={defaultTranslationProviderGroupId} providerSaving={translationProviderSaving} providersLoading={translationStatusRefreshing} onProviderChange={(groupId) => void selectDefaultTranslationProvider(groupId)} onTranslate={translateTypedText} onRetranslateWithLLM={() => void retranslateSelectedTextWithLLM(translationSource)} />
                </div>
                {translationPanelMode === "document" && <FullDocumentTranslationPanel
                  datasetId={knowledgeBaseId} documentId={knowledgeId}
                  artifact={sidebarTranslation}
                  scrollSync={scrollSync.translation} syncScroll={syncScroll} onSyncScrollChange={toggleSyncScroll}
                  supported={canTranslateDocument} busy={translationInProgress}
                  canWrite={hasWritePermission}
                  progressNotice={<PdfTranslationNotice progress={pdfProgress} documentId={knowledgeId} job={pdfTask} preparing={translationPreparing} detail={pdfTaskDetail} providerLabel={displayTranslationName(getTranslationProgressLabel(pdfTask, translationPreparing, translationMode, translationProvider))} onCancel={() => void cancelTranslationJob()} />}
                  onTranslate={openNewTranslation}
                  onRetranslateSelection={selection => void openTranslationRevision(selection, "translation")}
                  onRegenerate={openTranslationRegeneration}
                  onHalfWidth={workspaceWidth >= 900 && (panelLayout === "left" || panelLayout === "right") ? () => resizePaperAssistant(workspaceWidth * .5) : undefined}
                />}
              </div>
              {paperDetailsOpened && <div className="paper-assistant-pane" hidden={previewSideTab !== "info" && previewSideTab !== "notes"}><PaperReaderDetails documentVocabulary={isVocabularyEnabled()?<DocumentVocabularyPanel documentId={knowledgeId} refreshToken={vocabularyRefreshToken}/>:undefined} datasetId={knowledgeBaseId} documentId={knowledgeId} fileName={knowledgeDetail.display_name||""} canWrite={hasWritePermission} refreshToken={notesRevision} selectedNoteId={selectedNoteId} initialTab={previewSideTab==="notes"?paperNotesTab:"info"} onTabChange={setPaperNotesTab} onNoteDeleted={id=>{setSelectedNoteId(current=>current===id?undefined:current);setSavedNotes(current=>current?{...current,highlights:current.highlights.filter(item=>item.id!==id)}:current);}} onNavigatePage={page=>{setPdfNavigateToPage({page,requestId:++navigationSequence.current});}} onNavigateNote={source=>{setPdfNavigateToPosition({page:source.page,progress:Math.max(0,(source.rects?.[0]?.[1]??0)-.12),requestId:++navigationSequence.current});}}/></div>}
              <div className="paper-assistant-pane" hidden={previewSideTab !== "chat"}>
              {(isPaperDocument || activatedDocumentChatKey===documentKey) && <PdfTemporaryChat newConversationInToolbar ref={paperChatRef} key={`${knowledgeBaseId}:${knowledgeId}`} paperMode={isPaperDocument} onClearSelection={()=>setDocumentChatSelection(null)} onNavigateSelection={selection=>{if(selection.page){handlePdfViewPositionChange({page:selection.page,progress:0});setPdfNavigateToPage({page:selection.page,requestId:++navigationSequence.current});}}} datasetId={knowledgeBaseId} documentId={knowledgeId} fileName={knowledgeDetail.display_name||""} selection={documentChatSelection||undefined} conversationToLoad={selectedDocumentConversation} onConversationChange={setSelectedDocumentConversation} onHistoryChange={refreshDocumentChatHistory} onClose={()=>{setDocumentChatSelection(null);setPreviewSideCollapsed(true);}}/>}
              </div>
            </div>

          </>}
        </div>
        <PaperToolDock label={i18n.language.startsWith("zh")?"阅读工具":"Reading tools"} activeTool={previewSideCollapsed ? undefined : previewSideTab}
          tools={[
            ...(isPaperDocument?[{id:"notes",label:paperNotesLabel,icon:<EditOutlined/>}]:[]),
            ...(!isPaperDocument?[{id:"navigation",label:t("paperShell.documentNavigation"),icon:<MenuOutlined/>}]:[]),
            {id:"translation",label:i18n.language.startsWith("zh")?"翻译":"Translation",icon:<TranslationOutlined/>},
            {id:"chat",label:t(isPaperDocument?"paperShell.assistant":"paperShell.documentAssistant"),icon:<MessageOutlined/>},
          ]}
          onChange={tool=>{if(!tool){setPreviewSideCollapsed(true);return;}setPreviewSideTab(tool);setPreviewSideCollapsed(false);if(tool==="chat")setActivatedDocumentChatKey(documentKey);}}
        >
        </PaperToolDock>
      </div>
      <Modal
        open={translationModalOpen}
        title={regenerationArtifact ? "重新生成当前译本" : "翻译文档"}
        okText={regenerationArtifact ? "重新生成" : "开始翻译"}
        cancelText="取消"
        confirmLoading={translationPreparing || translationStatusRefreshing}
        okButtonProps={{ disabled: translationStatusRefreshing || (translationDialogMode === "api" && !(regenerationArtifact ? regenerationProviderGroupId : translationProviderGroupId)) }}
        onCancel={() => { setTranslationModalOpen(false); setRegenerationArtifact(undefined); }}
        onOk={() => void runTranslationPdf(Boolean(regenerationArtifact), regenerationArtifact, regenerationProviderGroupId)}
      >
        <div className="pdf-translation-config">
          <div className="pdf-translation-config-row"><label>翻译方式</label><Segmented block disabled={Boolean(regenerationArtifact)} value={translationDialogMode} onChange={(value: string | number) => setTranslationMode(value as "api" | "llm")} options={[{ label: "翻译 API", value: "api", disabled: translationStatusRefreshing || !translationDocumentSupported }, { label: "LazyMind 大模型", value: "llm" }]} /></div>
          {translationDialogMode === "api" ? <div className="pdf-translation-config-row">
            <label htmlFor="pdf-translation-provider">翻译 API 来源</label>
            <Select
              id="pdf-translation-provider"
              aria-label="翻译 API 来源"
              value={regenerationArtifact ? regenerationProviderGroupId : translationProviderGroupId}
              onChange={regenerationArtifact ? setRegenerationProviderGroupId : setTranslationProviderGroupId}
              placeholder={regenerationArtifact ? "请选择用于重新生成的翻译 API" : "请选择翻译 API"}
              options={translationDocumentProviders.map((item) => {
                const name = displayTranslationName(item.provider_name);
                return {
                  value: item.group_id,
                  label: item.group_name && item.group_name !== item.provider_name
                    ? `${name} · ${item.group_name}`
                    : name,
                };
              })}
            />
          </div> : null}
          {!translationDocumentSupported && !translationStatusRefreshing ? <p>
            暂无可用于文档翻译的 API，可在 <a href="/settings?section=model_defaults&service=translation">默认模型</a> 中启用。
          </p> : null}
          <div className="pdf-translation-config-row"><label>目标语言</label><Select disabled={Boolean(regenerationArtifact)} value={translationTarget} onChange={setTranslationTarget} options={[{ label: "简体中文", value: "zh" }, { label: "英文", value: "en" }]} /></div>
          <p>优先保留文档版式；PDF 原文字框容纳不下译文时，将生成标明原文页码的重排译文，图表请对照原文阅读。若原件是图片 PDF，会先生成并缓存文本 PDF。</p>
        </div>
      </Modal>
      <Modal
        className="pdf-translation-revision-modal"
        open={revisionModalOpen}
        title="重新翻译此段"
        width={1080}
        okText="确认并生成新译本"
        cancelText="取消"
        confirmLoading={revisionLoading}
        okButtonProps={{ disabled: !revisionText.trim() || revisionText.trim() === revisionBlock?.translated_text.trim() }}
        onCancel={() => setRevisionModalOpen(false)}
        onOk={() => void confirmTranslationRevision()}
      >
        <div className="pdf-translation-revision">
          <section className="pdf-translation-revision__pane pdf-translation-revision__source">
            <header><span>原文</span><small>Original</small></header>
            <div className="pdf-translation-revision__source-text">{revisionBlock?.source_text}</div>
          </section>
          <section className="pdf-translation-revision__pane pdf-translation-revision__target">
            <header>
              <div><span>译文</span><small>Translation</small></div>
              <div className="pdf-translation-revision__actions">
                <Button
                  size="small"
                  loading={revisionTranslatingWith === "api"}
                  disabled={revisionLoading && revisionTranslatingWith !== "api"}
                  onClick={() => void retranslateRevisionBlock("api")}
                >默认翻译 API</Button>
                <Button
                  size="small"
                  loading={revisionTranslatingWith === "llm"}
                  disabled={revisionLoading && revisionTranslatingWith !== "llm"}
                  onClick={() => void retranslateRevisionBlock("llm")}
                >大模型</Button>
              </div>
            </header>
            <Input.TextArea
              className="pdf-translation-revision__editor"
              value={revisionText}
              onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setRevisionText(event.target.value)}
              autoSize={false}
              placeholder="选择翻译方式生成译文，或直接在这里编辑"
            />
          </section>
          <p className="pdf-translation-revision__hint">确认后会生成一个新的翻译版 PDF，其他段落和原译本保持不变。</p>
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

      <Modal title="选择要加入生词本的单词" open={Boolean(vocabularyPhrase)} okText="加入生词" okButtonProps={{ disabled: !vocabularyWord }} onCancel={() => setVocabularyPhrase(null)} onOk={() => {
        if (!vocabularyPhrase || !vocabularyWord) return;
        setVocabularySelection({ ...vocabularyPhrase, text: vocabularyWord, context: vocabularyPhrase.context || vocabularyPhrase.text });
        setVocabularyPhrase(null);
      }}>
        <p>从选中的段落中选择一个单词：</p>
        <Select aria-label="选择生词" showSearch optionFilterProp="label" style={{ width: "100%" }} value={vocabularyWord} options={Array.from(new Set(vocabularyPhrase?.text.match(/[A-Za-z][A-Za-z'-]*/g) || [])).map((word) => ({ value: word, label: word }))} onChange={setVocabularyWord} />
      </Modal>
      {isVocabularyEnabled() ? <AddVocabularyModal
        selection={vocabularySelection}
        datasetId={knowledgeBaseId}
        documentId={knowledgeId}
        segmentId={segmentDetail?.segment_id}
        context={vocabularySelection?.context || segmentDetail?.content || vocabularySelection?.text || undefined}
        onClose={() => setVocabularySelection(null)}
        onAdded={handleVocabularyAdded}
      /> : null}
      <AddLearningContentModal value={learningSelection} datasetId={knowledgeBaseId} documentId={knowledgeId} segmentId={segmentDetail?.segment_id} context={learningSelection?.selection.context||segmentDetail?.content} onClose={()=>setLearningSelection(null)} onAdded={handleVocabularyAdded} />
    </div>
  );
};

const Detail = () => {
  const {knowledgeBaseId, knowledgeId} = useParams();
  return <KnowledgeDocumentContent key={`${knowledgeBaseId}:${knowledgeId}`} />;
};

export default Detail;
