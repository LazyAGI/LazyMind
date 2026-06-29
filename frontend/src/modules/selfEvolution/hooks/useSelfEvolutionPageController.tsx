import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Dropdown,
  Modal,
  Table,
  Tag,
  Typography,
  type MenuProps,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  FileTextOutlined,
  DownloadOutlined,
  DownOutlined,
  ExperimentOutlined,
  LoadingOutlined,
  ReloadOutlined,
  DatabaseOutlined,
  MessageOutlined,
} from "@ant-design/icons";
import SendIcon from "@/modules/chat/assets/icons/send_icon.svg?react";
import type { Dataset } from "@/api/generated/core-client";
import { AgentAppsAuth } from "@/components/auth";
import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";
import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import type { AxiosError } from "axios";
import { type HistorySessionModalProps } from "../components/HistorySessions";
import { type SelfEvolutionHomeViewProps } from "../components/LaunchViews";
import { normalizeTraceObservation, TraceObservationView } from "../components/TraceObservationView";
import { AnalysisCategoryPieChart } from "../components/AnalysisCategoryPieChart";
import { type SelfEvolutionFinalResultSummary, type SelfEvolutionObservationKind, type SelfEvolutionWorkbenchViewProps } from "../components/WorkbenchView";
import { type SelfEvolutionWorkbenchTab } from "../components/types";
import "../index.scss";
import {
  EvolutionMode,
  ExtraEvalStrategy,
  WorkflowStep,
  StepStatus,
  ChatMessage,
  ChatSession,
  ThreadHistoryEntry,
  HistorySessionEntry,
  NewSessionDraft,
  SelfEvolutionPageView,
  SelfEvolutionRouteState,
  KnowledgeBaseOption,
  AgentThreadCreateResponse,
  ThreadRestorePayload,
  WorkflowRuntimeState,
  NormalizedThreadEvent,
  ChatStreamDeltaKind,
  CheckpointWaitPrompt,
  WorkflowResultKind,
  WorkflowResultsState,
  DiffArtifactContentState,
  AbComparisonRow,
  FIXED_EVAL_SET,
  FIXED_EXTRA_EVAL_STRATEGY,
  DEFAULT_EVAL_CASE_COUNT,
  AGENT_API_BASE,
  SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY,
  DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY,
  getWorkflowResultLabels,
  createCoreAgentApiClient,
  createCoreAgentGeneratedApiClient,
  DiffFileTreeNode,
  PxCategoryMetricAverage,
  AbCategoryComparison,
  AbSummaryMetricRow,
  AbTopDiffRow,
  AbSummaryReport,
  getPxMetricMeta,
  getKnowledgeBaseName,
  isCanceledRequest,
  getExistingEvalSetOptions,
  evalSetPreviewData,
  clampScore,
  formatPercent,
  buildPxCategoryMetricAveragesFromReport,
  getTimeLabel,
  createInitialWorkflowRuntimeState,
  createWorkflowRuntimeStateForMode,
  createThreadRestoreWorkflowRuntimeState,
  createCheckpointRestoreWorkflowRuntimeState,
  createInitialWorkflowResultsState,
  isRecord,
  getStringField,
  getNumberField,
  getResultItems,
  isEmptyResultPayload,
  stringifyResultPayload,
  getResultStringField,
  buildCoreDownloadUrl,
  getResultDownloadPath,
  getDiffArtifactFiles,
  normalizeFetchedDiffArtifact,
  getDownloadFileName,
  triggerBrowserDownload,
  getNestedStringField,
  getNestedRecordField,
  formatThreadTime,
  getThreadTimeSortValue,
  normalizeThreadListPayload,
  getDialogueEventAgentLabel,
  buildAutoInteractionMessagesFromEvents,
  normalizeThreadHistoryMessages,
  getStructuredArrayField,
  getStructuredRecordField,
  getDiffLineType,
  getShortLabel,
  parseUnifiedDiff,
  buildDiffFileTree,
  buildAbCategoryComparisons,
  formatMetricDelta,
  formatMetricSummary,
  formatAbMetricLabel,
  buildAbSummaryReports,
  formatMaybePValue,
  parseSSEFrame,
  getChatStreamDeltaKind,
  isInactiveTerminalThreadEvent,
  normalizeThreadEvent,
  compareNormalizedThreadEvents,
  dedupeNormalizedEvents,
  buildVisibleWorkflowSteps,
  buildEvoProcessDashboard,
  getPendingCheckpointWaitPrompt,
  isThreadEventAfter,
  reduceWorkflowRuntimeState,
  reduceWorkflowRuntimeStateFromEvents,
  getThreadTitleFromHistoryPayload,
  getThreadTitleFromPayload,
  getThreadKnowledgeBaseId,
  getThreadModeFromPayload,
  getTerminalFlowStepStatus,
  getCheckpointCommandText,
} from "../shared";
import {
  type DatasetCasePreviewRow,
  type AnalysisCasePreviewRow,
  type PxCaseDetailRow,
  type ArtifactPanelItem,
  type CaseArtifactState,
  type EvalReportBadCasesState,
  type ThreadStepSummary,
  type ThreadStepListState,
} from "./controller/types";
import {
  INITIAL_THREAD_STEP_ID,
  stageArtifactKindMap,
  artifactStepIdMap,
  workflowStepStageMap,
  EVAL_REPORT_BAD_CASES_PAGE_SIZE,
  legacyPlanningThinkingText,
} from "./controller/constants";
import {
  resolveCaseArtifactId,
  formatSignedFinalPercent,
  getFinalResultMetricLabel,
  humanizeFinalResultReason,
  normalizeThreadStepStatus,
  isThreadStepRunning,
  isThreadFlowRunning,
  getSilentRestoreRequestConfig,
  normalizeThreadStepListPayload,
  getDefaultThreadStep,
  resolveNextStepRunIdFromStepList,
  isCheckpointContinueCommand,
  getNextStepRunId,
  normalizeThreadRecordEvents,
  getEvalReportSourceRecord,
  getEvalReportId,
  getEvalReportBadCaseListRecords,
  getEvalReportBadCasesPayloadRecord,
  buildPxCaseDetailRows,
  buildAnalysisCategorySummaryRows,
} from "./controller/helpers";
import {
  buildDatasetCaseColumns,
  buildPxCaseDetailColumns,
  buildAnalysisCaseColumns,
  buildAbComparisonColumns,
} from "./controller/columns";

const { Paragraph, Text } = Typography;

export type SelfEvolutionPageRenderProps = {
  isWorkbenchVisible: boolean;
  homeViewProps: SelfEvolutionHomeViewProps;
  homeHistoryModalProps: HistorySessionModalProps;
  workbenchViewProps: SelfEvolutionWorkbenchViewProps;
};

export function SelfEvolutionPageController({
  view,
  children,
}: {
  view: SelfEvolutionPageView;
  children: (props: SelfEvolutionPageRenderProps) => ReactNode;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const { threadId: routeThreadId } = useParams<{ threadId?: string }>();
  const routeState = location.state as SelfEvolutionRouteState | null;
  const [mode, setMode] = useState<EvolutionMode>("interactive");
  const [selectedEvalSet, setSelectedEvalSet] = useState<string>(FIXED_EVAL_SET);
  const [extraEvalStrategy, setExtraEvalStrategy] = useState<ExtraEvalStrategy>(FIXED_EXTRA_EVAL_STRATEGY);
  const [selectedKb, setSelectedKb] = useState<string>();
  const [knowledgeBaseOptions, setKnowledgeBaseOptions] = useState<KnowledgeBaseOption[]>([]);
  const [isKnowledgeBaseLoading, setIsKnowledgeBaseLoading] = useState(true);
  const [knowledgeBaseError, setKnowledgeBaseError] = useState("");
  const [hasLaunchValidationTriggered, setHasLaunchValidationTriggered] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [isWorkbenchVisible, setIsWorkbenchVisible] = useState(
    view === "detail" || Boolean(routeState?.openWorkbench),
  );
  const [isStartingSession, setIsStartingSession] = useState(false);
  const [isConfirmingNewSession, setIsConfirmingNewSession] = useState(false);
  const [isSendingMessage, setIsSendingMessage] = useState(false);
  const [isPlanningNextStep, setIsPlanningNextStep] = useState(false);
  const [isRestoringThread, setIsRestoringThread] = useState(false);
  const [isHistorySessionModalOpen, setIsHistorySessionModalOpen] = useState(false);
  const [isLoadingThreadHistoryList, setIsLoadingThreadHistoryList] = useState(false);
  const [deletingHistoryKeys, setDeletingHistoryKeys] = useState<string[]>([]);
  const [threadHistoryListError, setThreadHistoryListError] = useState("");
  const [threadRestoreError, setThreadRestoreError] = useState("");
  const [isNewSessionConfigOpen, setIsNewSessionConfigOpen] = useState(false);
  const [hasNewSessionValidationTriggered, setHasNewSessionValidationTriggered] = useState(false);
  const [newSessionDraft, setNewSessionDraft] = useState<NewSessionDraft>({});
  const [activeWorkbenchTab, setActiveWorkbenchTab] = useState<SelfEvolutionWorkbenchTab | undefined>("messages");
  const [activeArtifactKind, setActiveArtifactKind] = useState<WorkflowResultKind>();
  const [isArtifactPanelOpen, setIsArtifactPanelOpen] = useState(false);
  const [caseArtifact, setCaseArtifact] = useState<CaseArtifactState>();
  const [previewHistoryKey, setPreviewHistoryKey] = useState<string>();
  const [historyPreviewTitle, setHistoryPreviewTitle] = useState("");
  const [historyPreviewMessages, setHistoryPreviewMessages] = useState<ChatMessage[]>([]);
  const [historyPreviewError, setHistoryPreviewError] = useState("");
  const [isLoadingHistoryPreview, setIsLoadingHistoryPreview] = useState(false);
  const [workflowRuntimeState, setWorkflowRuntimeState] = useState<WorkflowRuntimeState>(
    createInitialWorkflowRuntimeState,
  );
  const [workflowResults, setWorkflowResults] = useState<WorkflowResultsState>(
    createInitialWorkflowResultsState,
  );
  const [evalReportBadCases, setEvalReportBadCases] = useState<EvalReportBadCasesState>({
    loading: false,
    loaded: false,
  });
  const [liveCheckpointWaitPrompt, setLiveCheckpointWaitPrompt] = useState<CheckpointWaitPrompt>();
  const [terminalFlowStepStatus, setTerminalFlowStepStatus] = useState<StepStatus>();
  const [diffArtifactContent, setDiffArtifactContent] = useState<DiffArtifactContentState>({
    loading: false,
    key: "",
    content: "",
  });
  const [threadEvents, setThreadEvents] = useState<NormalizedThreadEvent[]>([]);
  const [threadStepList, setThreadStepList] = useState<ThreadStepListState>({ steps: [] });
  const [selectedViewStage, setSelectedViewStage] = useState<string>();
  const [selectedThreadStepId, setSelectedThreadStepId] = useState<string>();
  const [loadingThreadStepId, setLoadingThreadStepId] = useState<string>();
  const threadEventsRef = useRef<NormalizedThreadEvent[]>([]);
  const [remoteThreadHistory, setRemoteThreadHistory] = useState<ThreadHistoryEntry[]>([]);
  const isThreadHistoryListFetchingRef = useRef(false);
  const historyPreviewRequestIdRef = useRef(0);
  const [chatSessions, setChatSessions] = useState<ChatSession[]>(() => [
    {
      id: "session-1",
      title: t("selfEvolutionRun.currentSession"),
      updatedAt: t("selfEvolutionRun.justNow"),
      messages: [],
    },
  ]);
  const [activeSessionId, setActiveSessionId] = useState("session-1");
  const chatStreamRef = useRef<HTMLDivElement | null>(null);
  const threadEventsAbortRef = useRef<{ threadId: string; stepId: string; controller: AbortController } | null>(null);
  const processedThreadEventIdsRef = useRef<Set<string>>(new Set());
  const processedWorkflowEventKeysRef = useRef<Set<string>>(new Set());
  const pendingNextStepRunIdRef = useRef<string>();
  const loadingThreadStepIdRef = useRef<string>();
  const restoreRequestIdRef = useRef(0);
  const [activeDiffFileId, setActiveDiffFileId] = useState("");
  const [collapsedDiffDirs, setCollapsedDiffDirs] = useState<Record<string, boolean>>({});

  useEffect(() => {
    window.localStorage.removeItem(DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY);
  }, []);

  const fetchKnowledgeBaseOptions = useCallback((signal?: AbortSignal) => {
    setIsKnowledgeBaseLoading(true);
    setKnowledgeBaseError("");

    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({ pageSize: 1000 }, { signal })
      .then((res) => {
        const nextOptions = (res.data.datasets || [])
          .filter((dataset): dataset is Dataset => Boolean(dataset.dataset_id))
          .map((dataset) => ({
            label: getKnowledgeBaseName(dataset),
            value: dataset.dataset_id,
          }));

        setKnowledgeBaseOptions(nextOptions);
        setSelectedKb((prev) =>
          prev && nextOptions.some((item) => item.value === prev) ? prev : undefined,
        );
        setNewSessionDraft((prev) =>
          prev.selectedKb && !nextOptions.some((item) => item.value === prev.selectedKb)
            ? { ...prev, selectedKb: undefined }
            : prev,
        );
      })
      .catch((error) => {
        if (isCanceledRequest(error)) {
          return;
        }

        const fallback = t("selfEvolutionRun.error.knowledgeBaseLoadFailed");
        setKnowledgeBaseError(getLocalizedErrorMessage(error, fallback) || fallback);
      })
      .finally(() => {
        if (!signal?.aborted) {
          setIsKnowledgeBaseLoading(false);
        }
      });
  }, [t]);
  const selectedKnowledgeBaseLabel = knowledgeBaseOptions.find((item) => item.value === selectedKb)?.label;
  const knowledgeBasePlaceholder = knowledgeBaseError
    ? t("selfEvolutionRun.knowledgeBaseLoadFailed")
    : isKnowledgeBaseLoading
      ? t("selfEvolutionRun.knowledgeBaseLoading")
      : knowledgeBaseOptions.length === 0
        ? t("selfEvolutionRun.noKnowledgeBase")
        : t("selfEvolutionRun.knowledgeBase");
  const selectedKnowledgeBase = selectedKnowledgeBaseLabel || knowledgeBasePlaceholder;
  const knowledgeBaseLaunchLabel =
    selectedKnowledgeBaseLabel ||
    (knowledgeBaseError || isKnowledgeBaseLoading || knowledgeBaseOptions.length === 0
      ? knowledgeBasePlaceholder
      : t("selfEvolutionRun.knowledgeBaseNotSelected"));
  const getExistingEvalSetLabel = useCallback(
    (value?: string) => {
      const option = getExistingEvalSetOptions().find((item) => item.value === value);
      if (option?.value === FIXED_EVAL_SET) {
        return t("selfEvolutionRun.noExistingEvalSet");
      }
      return option?.label || t("selfEvolutionRun.noExistingEvalSet");
    },
    [t],
  );
  const selectedEvalSetLabel = getExistingEvalSetLabel(selectedEvalSet);
  const isExtraEvalRequired = selectedEvalSet === "__none__";
  const extraEvalLabel = extraEvalStrategy === "generate" ? t("selfEvolutionRun.extraEvalGenerate") : t("selfEvolutionRun.extraEvalSkip");
  const interventionLabel = mode === "interactive" ? t("selfEvolutionRun.interventionManual") : t("selfEvolutionRun.interventionAuto");
  const isAutoMode = mode === "auto";
  const modeLabel = isAutoMode ? t("selfEvolutionRun.modeAuto") : t("selfEvolutionRun.modeInteractive");
  const isKnowledgeBaseRequired = !selectedKb;
  const isLaunchConfigComplete = Boolean(selectedKb && selectedEvalSet && extraEvalStrategy && mode);
  const isLaunchConfigValid =
    isLaunchConfigComplete && (!isExtraEvalRequired || extraEvalStrategy === "generate");
  const draftSelectedKnowledgeBaseLabel = knowledgeBaseOptions.find(
    (item) => item.value === newSessionDraft.selectedKb,
  )?.label;
  const draftKnowledgeBaseLaunchLabel =
    draftSelectedKnowledgeBaseLabel ||
    (knowledgeBaseError || isKnowledgeBaseLoading || knowledgeBaseOptions.length === 0
      ? knowledgeBasePlaceholder
      : t("selfEvolutionRun.selectKnowledgeBase"));
  const draftSelectedEvalSetLabel = newSessionDraft.selectedEvalSet
    ? getExistingEvalSetLabel(newSessionDraft.selectedEvalSet)
    : undefined;
  const draftEvalSetLabel = draftSelectedEvalSetLabel || t("selfEvolutionRun.selectEvalSet");
  const isDraftExtraEvalRequired = newSessionDraft.selectedEvalSet === "__none__";
  const draftExtraEvalLabel =
    newSessionDraft.extraEvalStrategy === "generate"
      ? t("selfEvolutionRun.extraEvalGenerate")
      : newSessionDraft.extraEvalStrategy === "skip"
        ? t("selfEvolutionRun.extraEvalSkip")
        : t("selfEvolutionRun.selectExtraEvalStrategy");
  const draftInterventionLabel =
    newSessionDraft.mode === "interactive"
      ? t("selfEvolutionRun.interventionManual")
      : newSessionDraft.mode === "auto"
        ? t("selfEvolutionRun.interventionAuto")
        : t("selfEvolutionRun.selectInterventionMode");
  const isNewSessionDraftComplete = Boolean(
    newSessionDraft.selectedKb &&
      newSessionDraft.selectedEvalSet &&
      newSessionDraft.extraEvalStrategy &&
      newSessionDraft.mode,
  );
  const isNewSessionDraftValid =
    isNewSessionDraftComplete &&
    (!isDraftExtraEvalRequired || newSessionDraft.extraEvalStrategy === "generate");
  const isNewSessionStepOneDone = Boolean(newSessionDraft.selectedKb);
  const isNewSessionStepTwoDone = Boolean(newSessionDraft.selectedEvalSet);
  const isNewSessionStepThreeDone = Boolean(newSessionDraft.extraEvalStrategy);
  const isNewSessionStepFourDone = Boolean(newSessionDraft.mode);
  const workflowSteps = useMemo<WorkflowStep[]>(
    () => buildVisibleWorkflowSteps(
      threadEvents,
      workflowRuntimeState,
      isWorkbenchVisible,
      terminalFlowStepStatus,
    ),
    [isWorkbenchVisible, terminalFlowStepStatus, threadEvents, workflowRuntimeState],
  );
  const processDashboard = useMemo(
    () => buildEvoProcessDashboard(
      threadEvents,
      workflowRuntimeState,
      isWorkbenchVisible,
      terminalFlowStepStatus,
    ),
    [isWorkbenchVisible, terminalFlowStepStatus, threadEvents, workflowRuntimeState],
  );
  const pendingCheckpointWaitPrompt = useMemo(
    () => {
      if (terminalFlowStepStatus || threadEvents.some(isInactiveTerminalThreadEvent)) {
        return undefined;
      }
      return liveCheckpointWaitPrompt || getPendingCheckpointWaitPrompt(threadEvents);
    },
    [liveCheckpointWaitPrompt, terminalFlowStepStatus, threadEvents],
  );
  const isSendDisabled = !prompt.trim() || isSendingMessage;
  const activeStepText = useMemo(() => {
    const activeStep = processDashboard.activeStep;
    return activeStep?.title || t("selfEvolutionRun.workflowCompleted");
  }, [processDashboard.activeStep, t]);
  const activeStageArtifactKind = processDashboard.activeStage
    ? stageArtifactKindMap[processDashboard.activeStage]
    : undefined;
  useEffect(() => {
    if (activeStageArtifactKind) {
      setActiveArtifactKind(activeStageArtifactKind);
    }
  }, [activeStageArtifactKind]);
  const datasetDownloadFileName = useMemo(() => {
    const normalizedEvalName =
      evalSetPreviewData.eval_name.replace(/[\\/:*?"<>|]+/g, "_").trim() || "eval-dataset";
    return `${normalizedEvalName}-${evalSetPreviewData.eval_set_id}.json`;
  }, []);
  const datasetDownloadUrl = useMemo(() => {
    if (typeof window === "undefined") {
      return "";
    }
    const datasetBlob = new Blob([JSON.stringify(evalSetPreviewData, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    return URL.createObjectURL(datasetBlob);
  }, []);
  const fetchedPxCategoryMetricAverages = useMemo<PxCategoryMetricAverage[]>(
    () => buildPxCategoryMetricAveragesFromReport(workflowResults["eval-reports"].data),
    [workflowResults["eval-reports"].data],
  );
  const evalTraceObservation = useMemo(
    () => normalizeTraceObservation(workflowResults["eval-reports"].data),
    [workflowResults["eval-reports"].data],
  );
  const datasetArtifactData = useMemo(() => {
    const items = getResultItems(workflowResults.datasets.data).filter(isRecord);
    const row = items.find((item) => getResultStringField(item, ["artifact_id"]) === "eval_dataset") || items[0];
    return getStructuredRecordField(row, ["data"]) || getNestedRecordField(row, ["data"]) || row;
  }, [workflowResults.datasets.data]);
  const datasetCaseRows = useMemo<DatasetCasePreviewRow[]>(() => {
    const caseRecords = (getStructuredArrayField(datasetArtifactData, ["cases"]) ||
      getStructuredArrayField(datasetArtifactData, ["preview"]) || []).filter(isRecord);
    return caseRecords.map((item, index) => {
      const references = [
        ...(getStructuredArrayField(item, ["reference_doc"]) || []),
        ...(getStructuredArrayField(item, ["reference_doc_ids"]) || []),
      ]
        .map((value) => String(value || "").trim())
        .filter(Boolean);
      const caseId = getStringField(item, ["id", "case_id"]) || `case_${String(index + 1).padStart(4, "0")}`;
      return {
        key: caseId,
        caseId,
        question: getStringField(item, ["question"]) || "-",
        answer: getStringField(item, ["answer", "ground_truth"]) || "-",
        questionType: getStringField(item, ["question_type", "question_type_name"]) || "-",
        difficulty: getStringField(item, ["difficulty"]) || "-",
        references: references.slice(0, 2).join(" / ") || "-",
      };
    });
  }, [datasetArtifactData]);
  const datasetCaseColumns = useMemo<ColumnsType<DatasetCasePreviewRow>>(
    () => buildDatasetCaseColumns(t),
    [t],
  );
  const pxReportCategoryMetrics = fetchedPxCategoryMetricAverages;
  const isSinglePxCategory = pxReportCategoryMetrics.length === 1;
  const evalReportSourceRecord = useMemo(
    () => getEvalReportSourceRecord(workflowResults["eval-reports"].data),
    [workflowResults["eval-reports"].data],
  );
  const evalReportId = useMemo(
    () => getEvalReportId(workflowResults["eval-reports"].data),
    [workflowResults["eval-reports"].data],
  );
  const pxReportTotalCases = useMemo(() => {
    const caseDetailSummary =
      getStructuredRecordField(evalReportSourceRecord, ["case_details_summary"]) ||
      getNestedRecordField(evalReportSourceRecord, ["case_details_summary"]);

    return (
      getNumberField(caseDetailSummary, ["total_count"]) ||
      getNumberField(evalReportSourceRecord, ["total_cases", "case_count"]) ||
      pxReportCategoryMetrics.reduce((total, item) => total + item.caseCount, 0)
    );
  }, [evalReportSourceRecord, pxReportCategoryMetrics]);
  const pxCaseDetailRows = useMemo<PxCaseDetailRow[]>(
    () => buildPxCaseDetailRows(getEvalReportBadCaseListRecords(evalReportBadCases.data)),
    [evalReportBadCases.data],
  );
  const pxCaseDetailCount =
    evalReportBadCases.loaded && typeof evalReportBadCases.totalSize === "number"
      ? evalReportBadCases.totalSize
      : pxCaseDetailRows.length;
  const pxCaseDetailPage = evalReportBadCases.page || 1;
  const pxCaseDetailPageSize = evalReportBadCases.pageSize || EVAL_REPORT_BAD_CASES_PAGE_SIZE;
  const isPxCaseDetailPending = Boolean(
    evalReportId &&
      evalReportBadCases.reportId !== evalReportId &&
      !evalReportBadCases.error,
  );
  const pxCaseDetailColumns = useMemo<ColumnsType<PxCaseDetailRow>>(
    () => buildPxCaseDetailColumns(t),
    [t],
  );
  const analysisArtifactItems = useMemo(
    () => {
      const items = getResultItems(workflowResults["analysis-reports"].data).filter(isRecord);
      if (items.length > 0 || !isRecord(workflowResults["analysis-reports"].data)) {
        return items;
      }

      const directReport =
        getStructuredRecordField(workflowResults["analysis-reports"].data, ["data"]) ||
        getNestedRecordField(workflowResults["analysis-reports"].data, ["data"]) ||
        workflowResults["analysis-reports"].data;
      return isRecord(directReport) ? [directReport] : [];
    },
    [workflowResults["analysis-reports"].data],
  );
  const analysisReportData = useMemo(() => {
    const row = analysisArtifactItems.find((item) => getResultStringField(item, ["artifact_id"]) === "classification_report");
    return getStructuredRecordField(row, ["data"]) || getNestedRecordField(row, ["data"]) || row;
  }, [analysisArtifactItems]);
  const analysisSummaryData = useMemo(
    () => getStructuredRecordField(analysisReportData, ["summary"]) || getNestedRecordField(analysisReportData, ["summary"]),
    [analysisReportData],
  );
  const analysisCaseRows = useMemo<AnalysisCasePreviewRow[]>(() => (
    (getStructuredArrayField(analysisReportData, ["cases"]) || [])
      .filter(isRecord)
      .map((item, index) => ({
        key: getStringField(item, ["case_id", "id"]) || `analysis-case-${index + 1}`,
        caseId: getStringField(item, ["case_id", "id"]) || `case_${index + 1}`,
        coarseCategory: getStringField(item, ["coarse_category"]) || "-",
        fineCategory: getStringField(item, ["fine_category"]) || "-",
        confidence: getStringField(item, ["confidence"]) || "-",
        lossScore: String(getNumberField(item, ["loss_score", "priority_score"]) ?? "-"),
        quality: getStringField(item, ["quality", "quality_label"]) || "-",
      }))
  ), [analysisReportData]);
  const analysisCategoryRows = useMemo(
    () => buildAnalysisCategorySummaryRows(analysisSummaryData, t("selfEvolutionRun.uncategorized")),
    [analysisSummaryData],
  );
  const [highlightedAnalysisCategory, setHighlightedAnalysisCategory] = useState<string | null>(null);
  const hasAnalysisStructuredReport =
    analysisCategoryRows.length > 0 ||
    analysisCaseRows.length > 0;
  const analysisCaseColumns = useMemo<ColumnsType<AnalysisCasePreviewRow>>(
    () => buildAnalysisCaseColumns(t),
    [t],
  );
  const abSummaryReports = useMemo<AbSummaryReport[]>(
    () => buildAbSummaryReports(workflowResults.abtests.data),
    [workflowResults.abtests.data],
  );
  const abTraceObservation = useMemo(
    () => normalizeTraceObservation(workflowResults.abtests.data),
    [workflowResults.abtests.data],
  );
  const abCategoryComparisons = useMemo<AbCategoryComparison[]>(
    () => buildAbCategoryComparisons(abSummaryReports),
    [abSummaryReports],
  );
  const isSingleAbCategory = abCategoryComparisons.length <= 1;
  const abComparisonRows = useMemo<AbComparisonRow[]>(
    () =>
      abCategoryComparisons.map((item) => ({
        key: item.category,
        category: item.category,
        baselineSummary: formatMetricSummary(item.baseline),
        experimentSummary: formatMetricSummary(item.experiment),
        deltaSummary: [
          t("selfEvolutionRun.deltaCorrectness", { delta: formatMetricDelta(item.delta.answer_correctness) }),
          t("selfEvolutionRun.deltaScore", { delta: formatMetricDelta(item.delta.answer_score) }),
          t("selfEvolutionRun.deltaChunkRecall", { delta: formatMetricDelta(item.delta.chunk_recall) }),
          t("selfEvolutionRun.deltaDocRecall", { delta: formatMetricDelta(item.delta.doc_recall) }),
        ].join(" / "),
      })),
    [abCategoryComparisons],
  );
  const finalResultSummary = useMemo<SelfEvolutionFinalResultSummary | undefined>(() => {
    const report = abSummaryReports[0];
    if (!report) {
      return undefined;
    }
    const verdictText = (report.verdict || "").toLowerCase();
    const verdict: SelfEvolutionFinalResultSummary["verdict"] =
      verdictText.includes("reject") || verdictText.includes("fail")
        ? "reject"
        : verdictText.includes("accept") || verdictText.includes("pass")
          ? "accept"
          : "done";
    const primaryRow = report.metricRows.find((row) => row.metric === report.primaryMetric) || report.metricRows[0];
    const primaryMetricLabel = getFinalResultMetricLabel(t, report.primaryMetric || primaryRow?.metric, primaryRow?.metricLabel);
    const metricRows: SelfEvolutionFinalResultSummary["metrics"] = primaryRow
      ? [
        {
          label: t("selfEvolutionRun.abSummaryPrimaryMetric", { metric: primaryMetricLabel }),
          value: formatSignedFinalPercent(primaryRow.deltaMean),
          tone: primaryRow.deltaMean > 0 ? "good" : primaryRow.deltaMean < 0 ? "bad" : "neutral",
        },
        {
          label: t("selfEvolutionRun.candidateWinRate"),
          value: formatPercent(primaryRow.winRateB),
          tone: primaryRow.winRateB >= 0.5 ? "good" : "bad",
        },
      ]
      : [];
    const guardRow = report.metricRows.find((row) => row.metric !== primaryRow?.metric && Math.abs(row.deltaMean) > 0);
    if (guardRow) {
      metricRows.push({
        label: getFinalResultMetricLabel(t, guardRow.metric, guardRow.metricLabel),
        value: formatSignedFinalPercent(guardRow.deltaMean),
        tone: guardRow.deltaMean > 0 ? "good" : guardRow.deltaMean < 0 ? "bad" : "neutral",
      });
    }
    const reasons = Array.from(new Set(report.reasons.map((reason) => humanizeFinalResultReason(t, reason, primaryMetricLabel))));
    const isCutoverDone = processDashboard.cutoverCompleted;
    return {
      verdict,
      title: verdict === "reject"
        ? t("selfEvolutionRun.finalResultReject")
        : verdict === "accept" && !isCutoverDone
          ? t("selfEvolutionRun.finalResultAcceptPending")
          : verdict === "accept"
            ? t("selfEvolutionRun.finalResultAcceptDone")
            : t("selfEvolutionRun.workflowCompleted"),
      desc: verdict === "reject"
        ? t("selfEvolutionRun.finalResultRejectDesc")
        : isCutoverDone
          ? t("selfEvolutionRun.finalResultCutoverDoneDesc")
          : t("selfEvolutionRun.finalResultDoneDesc"),
      metrics: metricRows,
      reasons: reasons.slice(0, 3),
    };
  }, [abSummaryReports, processDashboard.cutoverCompleted, t]);
  const abComparisonColumns = useMemo<ColumnsType<AbComparisonRow>>(
    () => buildAbComparisonColumns(t),
    [t],
  );
  const abComparisonDownloadUrl = useMemo(() => {
    if (typeof window === "undefined") {
      return "";
    }
    const abBlob = new Blob([JSON.stringify(abCategoryComparisons, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    return URL.createObjectURL(abBlob);
  }, [abCategoryComparisons]);
  const directFetchedDiffText = useMemo(
    () => getResultStringField(workflowResults.diffs.data, ["diff", "patch", "content", "text"]),
    [workflowResults.diffs.data],
  );
  const diffArtifactFiles = useMemo(
    () => getDiffArtifactFiles(workflowResults.diffs.data),
    [workflowResults.diffs.data],
  );
  const diffArtifactKey = useMemo(
    () => diffArtifactFiles.map((file) => `${file.path}:${file.diffPath}`).join("|"),
    [diffArtifactFiles],
  );
  const fetchedDiffText = directFetchedDiffText || diffArtifactContent.content;
  const fetchedAnalysisReportMarkdown = useMemo(
    () =>
      getResultStringField(workflowResults["analysis-reports"].data, [
        "markdown",
        "report",
        "content",
        "text",
        "summary",
      ]),
    [workflowResults],
  );
  const parsedDiffFiles = useMemo(
    () => parseUnifiedDiff(fetchedDiffText),
    [fetchedDiffText],
  );
  const diffFileTree = useMemo(() => buildDiffFileTree(parsedDiffFiles), [parsedDiffFiles]);
  const activeDiffFile = parsedDiffFiles.find((item) => item.id === activeDiffFileId) || parsedDiffFiles[0];
  const activeSession = chatSessions.find((item) => item.id === activeSessionId) || chatSessions[0];
  const activeMessages = activeSession?.messages ?? [];
  const activeThreadId = activeSession?.threadId || routeThreadId;
  const activeRemoteThreadTitle = useMemo(
    () => remoteThreadHistory.find((item) => item.threadId === activeThreadId)?.title,
    [activeThreadId, remoteThreadHistory],
  );
  const isAutoInteractionActive = mode === "auto" && Boolean(activeThreadId);
  const threadDialogueMessages = useMemo(
    () => {
      if (mode !== "auto") {
        return [];
      }

      return buildAutoInteractionMessagesFromEvents(threadEvents).map((item) => ({
        ...item,
        agentLabel: item.agentLabel,
      }));
    },
    [mode, threadEvents],
  );
  const displayedMessages = useMemo(() => {
    if (previewHistoryKey) {
      if (isLoadingHistoryPreview) {
        return [
          {
            id: `history-preview-loading-${previewHistoryKey}`,
            role: "assistant" as const,
            content: t("selfEvolutionRun.previewingHistory", { title: historyPreviewTitle || previewHistoryKey }),
            time: getTimeLabel(),
          },
        ];
      }
      if (historyPreviewError) {
        return [
          {
            id: `history-preview-error-${previewHistoryKey}`,
            role: "assistant" as const,
            content: historyPreviewError,
            time: getTimeLabel(),
          },
        ];
      }
      return historyPreviewMessages;
    }

    const seen = new Set<string>();
    return [...activeMessages, ...threadDialogueMessages]
      .filter((item) => {
        const key = `${item.role}:${item.content}:${item.sortTime ?? item.time}`;
        if (seen.has(key)) {
          return false;
        }
        seen.add(key);
        return true;
      })
      .sort((a, b) => {
        if (typeof a.sortTime === "number" && typeof b.sortTime === "number" && a.sortTime !== b.sortTime) {
          return a.sortTime - b.sortTime;
        }
        return 0;
      });
  }, [
    activeMessages,
    historyPreviewError,
    historyPreviewMessages,
    historyPreviewTitle,
    isLoadingHistoryPreview,
    previewHistoryKey,
    threadDialogueMessages,
  ]);
  const shouldShowCheckpointPrompt =
    !isAutoInteractionActive ||
    pendingCheckpointWaitPrompt?.kind === "failure" ||
    pendingCheckpointWaitPrompt?.command === "确认切流";
  const displayedCheckpointWaitPrompt = shouldShowCheckpointPrompt ? pendingCheckpointWaitPrompt : undefined;
  const evalReportDownloadUrl = useMemo(
    () => buildCoreDownloadUrl(getResultDownloadPath(workflowResults["eval-reports"].data)),
    [workflowResults["eval-reports"].data],
  );
  const diffResultDownloadUrl = useMemo(() => {
    if (typeof window === "undefined" || !fetchedDiffText) {
      return "";
    }
    const diffBlob = new Blob([fetchedDiffText], {
      type: "text/x-diff;charset=utf-8",
    });
    return URL.createObjectURL(diffBlob);
  }, [fetchedDiffText]);
  const abtestResultDownloadUrl = useMemo(
    () => buildCoreDownloadUrl(getResultDownloadPath(workflowResults.abtests.data)),
    [workflowResults.abtests.data],
  );
  const fetchDiffDownloadText = useCallback(
    async (resultData: unknown, signal?: AbortSignal) => {
      const directDiffText = getResultStringField(resultData, ["diff", "patch", "content", "text"]);
      if (directDiffText) {
        return directDiffText;
      }

      const diffFiles = getDiffArtifactFiles(resultData);
      if (diffFiles.length === 0) {
        return "";
      }

      const contents = await Promise.all(
        diffFiles.map(async (file) => {
          const response = await axiosInstance.post(
            `${AGENT_API_BASE}/files:content`,
            { path: file.diffPath },
            { signal },
          );
          const responseData = response.data;
          const content =
            typeof responseData === "string"
              ? responseData
              : getResultStringField(responseData, ["content", "diff", "patch", "text"]) ||
                stringifyResultPayload(responseData);
          return normalizeFetchedDiffArtifact(file, content);
        }),
      );

      return contents.filter(Boolean).join("\n\n");
    },
    [],
  );

  useEffect(() => {
    if (directFetchedDiffText) {
      setDiffArtifactContent({ loading: false, key: "", content: "" });
      return;
    }

    if (diffArtifactFiles.length === 0) {
      setDiffArtifactContent({ loading: false, key: "", content: "" });
      return;
    }

    const controller = new AbortController();
    setDiffArtifactContent((prev) => ({
      loading: true,
      key: diffArtifactKey,
      content: prev.key === diffArtifactKey ? prev.content : "",
      error: undefined,
    }));

    fetchDiffDownloadText(workflowResults.diffs.data, controller.signal)
      .then((content) => {
        if (controller.signal.aborted) {
          return;
        }

        setDiffArtifactContent({
          loading: false,
          key: diffArtifactKey,
          content,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }

        setDiffArtifactContent({
          loading: false,
          key: diffArtifactKey,
          content: "",
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.diffLoadFailed")),
        });
      });

    return () => {
      controller.abort();
    };
  }, [diffArtifactFiles, diffArtifactKey, directFetchedDiffText, fetchDiffDownloadText, workflowResults.diffs.data]);

  const historySessionEntries = useMemo<HistorySessionEntry[]>(() => {
    const currentRemoteThread = activeThreadId
      ? remoteThreadHistory.find((item) => item.threadId === activeThreadId)
      : undefined;
    const currentThreadSession = activeThreadId
      ? chatSessions.find((session) => session.threadId === activeThreadId) ||
        chatSessions.find((session) => session.id === activeSessionId)
      : undefined;
    const currentThreadEntry: HistorySessionEntry[] = activeThreadId
      ? [
          {
            key: activeThreadId,
            sessionId: currentThreadSession?.id,
            threadId: activeThreadId,
            title:
              currentRemoteThread?.title ||
              currentThreadSession?.title ||
              `${t("selfEvolutionRun.selfEvolutionDetail")} ${activeThreadId.slice(0, 8)}`,
            updatedAt: currentRemoteThread?.updatedAt || currentThreadSession?.updatedAt || getTimeLabel(),
            messageCount: currentThreadSession?.messages.length,
            status: currentRemoteThread?.status,
            source: "thread",
            isCurrent: true,
            isPreviewing: activeThreadId === previewHistoryKey,
          },
        ]
      : [];
    const sessionEntries = chatSessions
      .filter(
        (session) =>
          session.id !== activeSessionId &&
          (!activeThreadId || session.threadId !== activeThreadId),
      )
      .map<HistorySessionEntry>((session) => ({
        key: session.threadId || session.id,
        sessionId: session.id,
        threadId: session.threadId,
        title: session.title,
        updatedAt: session.updatedAt,
        messageCount: session.messages.length,
        source: session.threadId ? "thread" : "local",
        isCurrent: false,
        isPreviewing: (session.threadId || session.id) === previewHistoryKey,
      }));
    const mergedEntries = [
      ...currentThreadEntry,
      ...sessionEntries,
      ...remoteThreadHistory
        .filter((item) => !activeThreadId || item.threadId !== activeThreadId)
        .filter((item) => !sessionEntries.some((session) => session.threadId === item.threadId))
        .map<HistorySessionEntry>((item) => ({
          key: item.threadId,
          sessionId: undefined,
          threadId: item.threadId,
          title: item.title,
          updatedAt: item.updatedAt,
          messageCount: undefined,
          status: item.status,
          source: "thread" as const,
          isCurrent: false,
          isPreviewing: item.threadId === previewHistoryKey,
        })),
    ];

    return mergedEntries.sort((a, b) => {
      if (a.isCurrent !== b.isCurrent) {
        return a.isCurrent ? -1 : 1;
      }
      return b.updatedAt.localeCompare(a.updatedAt, "zh-CN", { numeric: true });
    });
  }, [activeSessionId, activeThreadId, chatSessions, previewHistoryKey, remoteThreadHistory]);
  const isRuntimeConfigLocked = isWorkbenchVisible || Boolean(activeSession?.threadId);
  const getWorkflowResultUrl = useCallback(
    (kind: WorkflowResultKind) => {
      if (!activeThreadId) {
        return "";
      }
      return `${AGENT_API_BASE}/threads/${encodeURIComponent(activeThreadId)}/results/${kind}`;
    },
    [activeThreadId],
  );
  const fetchEvalReportBadCases = useCallback(
    async (resultData: unknown, options?: { force?: boolean; page?: number }) => {
      const reportId = getEvalReportId(resultData);
      if (!activeThreadId || !reportId) {
        setEvalReportBadCases({ loading: false, loaded: false });
        return undefined;
      }

      const pageSize = EVAL_REPORT_BAD_CASES_PAGE_SIZE;
      const page = Math.max(
        1,
        options?.page ?? (evalReportBadCases.reportId === reportId ? evalReportBadCases.page || 1 : 1),
      );
      const pageToken = page > 1 ? String((page - 1) * pageSize) : undefined;

      if (
        !options?.force &&
        evalReportBadCases.reportId === reportId &&
        (evalReportBadCases.page || 1) === page &&
        (evalReportBadCases.loading || evalReportBadCases.loaded)
      ) {
        return evalReportBadCases.data;
      }

      setEvalReportBadCases((prev) => ({
        ...prev,
        reportId,
        page,
        pageSize,
        pageToken,
        loading: true,
        loaded: prev.reportId === reportId && prev.page === page ? prev.loaded : false,
        data: prev.reportId === reportId && prev.page === page ? prev.data : undefined,
        error: undefined,
        nextPageToken: undefined,
        totalSize: prev.reportId === reportId ? prev.totalSize : undefined,
      }));

      try {
        const response = await createCoreAgentGeneratedApiClient()
          .apiCoreAgentThreadsThreadIdResultsEvalReportsReportIdBadCasesGet({
            threadId: activeThreadId,
            reportId,
            pageToken,
            pageSize,
          });
        const responseRecord = getEvalReportBadCasesPayloadRecord(response.data);
        const totalSize =
          getNumberField(responseRecord, ["total_size", "total_count", "total"]) ??
          getEvalReportBadCaseListRecords(response.data).length;
        const nextPageToken = getStringField(responseRecord, ["next_page_token", "nextPageToken"]);

        setEvalReportBadCases({
          reportId,
          page,
          pageSize,
          pageToken,
          nextPageToken,
          loading: false,
          loaded: true,
          data: response.data,
          totalSize,
        });
        return response.data;
      } catch (error) {
        setEvalReportBadCases((prev) => ({
          ...prev,
          reportId,
          page,
          pageSize,
          pageToken,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.dataListLoadFailed")),
        }));
        return undefined;
      }
    },
    [
      activeThreadId,
      evalReportBadCases.data,
      evalReportBadCases.loaded,
      evalReportBadCases.loading,
      evalReportBadCases.page,
      evalReportBadCases.reportId,
    ],
  );
  const fetchWorkflowResult = useCallback(
    async (kind: WorkflowResultKind, options?: { force?: boolean }) => {
      if (!activeThreadId) {
        message.warning(t("selfEvolutionRun.noAvailableThreadId"), 2);
        return undefined;
      }

      const currentState = workflowResults[kind];
      if (!options?.force && (currentState.loading || currentState.loaded)) {
        if (kind === "eval-reports" && currentState.loaded) {
          void fetchEvalReportBadCases(currentState.data);
        }
        return currentState.data;
      }

      setWorkflowResults((prev) => ({
        ...prev,
        [kind]: { ...prev[kind], loading: true, error: undefined },
      }));

      try {
        const response = await axiosInstance.get(getWorkflowResultUrl(kind));
        setWorkflowResults((prev) => ({
          ...prev,
          [kind]: {
            loading: false,
            loaded: true,
            data: response.data,
          },
        }));
        if (kind === "eval-reports") {
          void fetchEvalReportBadCases(response.data, { force: options?.force });
        }
        return response.data;
      } catch (error) {
        setWorkflowResults((prev) => ({
          ...prev,
          [kind]: {
            ...prev[kind],
            loading: false,
            loaded: true,
            error: getLocalizedErrorMessage(error, t("selfEvolutionRun.workflowResultLoadFailed", { label: getWorkflowResultLabels()[kind] })),
          },
        }));
        return undefined;
      }
    },
    [activeThreadId, fetchEvalReportBadCases, getWorkflowResultUrl, workflowResults],
  );
  const handleWorkflowDownload = useCallback(
    async (
      kind: WorkflowResultKind,
      fallbackUrl: string,
      fallbackFileName: string,
      event?: MouseEvent<HTMLElement>,
    ) => {
      event?.preventDefault();
      event?.stopPropagation();

      const currentData = workflowResults[kind].data;
      const nextData = currentData ?? (await fetchWorkflowResult(kind));
      let downloadUrl = buildCoreDownloadUrl(getResultDownloadPath(nextData)) || fallbackUrl;
      let temporaryDownloadUrl = "";

      if (kind === "diffs" && !downloadUrl) {
        const diffText = await fetchDiffDownloadText(nextData);
        if (diffText && typeof window !== "undefined") {
          temporaryDownloadUrl = URL.createObjectURL(
            new Blob([diffText], {
              type: "text/x-diff;charset=utf-8",
            }),
          );
          downloadUrl = temporaryDownloadUrl;
        }
      }

      if (!downloadUrl && nextData !== undefined && !isEmptyResultPayload(nextData) && typeof window !== "undefined") {
        temporaryDownloadUrl = URL.createObjectURL(
          new Blob([typeof nextData === "string" ? nextData : stringifyResultPayload(nextData)], {
            type: "application/json;charset=utf-8",
          }),
        );
        downloadUrl = temporaryDownloadUrl;
      }

      if (!downloadUrl) {
        message.warning(t("selfEvolutionRun.downloadNotAvailable", { label: getWorkflowResultLabels()[kind] }), 1.5);
        return;
      }

      triggerBrowserDownload(downloadUrl, getDownloadFileName(downloadUrl, fallbackFileName));

      if (temporaryDownloadUrl) {
        window.setTimeout(() => {
          URL.revokeObjectURL(temporaryDownloadUrl);
        }, 0);
      }
    },
    [fetchDiffDownloadText, fetchWorkflowResult, workflowResults],
  );
  const openWorkflowArtifact = useCallback(
    (kind: WorkflowResultKind) => {
      const step = workflowSteps.find((candidate) => candidate.id === artifactStepIdMap[kind]);
      const resultState = workflowResults[kind];
      const hasLoadedArtifact = resultState.loaded && !isEmptyResultPayload(resultState.data);
      const isObservationKind = kind === "eval-reports" || kind === "abtests";
      if (step && step.status !== "done" && !hasLoadedArtifact && !isObservationKind) {
        message.info(t("selfEvolutionRun.stepStillRunning", { title: step.title }), 2);
        return;
      }
      setCaseArtifact(undefined);
      setActiveWorkbenchTab("processes");
      setActiveArtifactKind(kind);
      setIsArtifactPanelOpen(true);
      setPreviewHistoryKey(undefined);
      setHistoryPreviewTitle("");
      setHistoryPreviewMessages([]);
      setHistoryPreviewError("");
      void fetchWorkflowResult(kind, { force: true });
    },
    [fetchWorkflowResult, workflowResults, workflowSteps],
  );

  const openObservationPage = useCallback(
    (kind: SelfEvolutionObservationKind) => {
      if (!activeThreadId) {
        message.warning(t("selfEvolutionRun.artifactNotReadyForObservation"), 2);
        return;
      }
      navigate(`/self-evolution/detail/${encodeURIComponent(activeThreadId)}/observation/${kind}`);
    },
    [activeThreadId, navigate],
  );

  const openCaseArtifact = useCallback(
    async (kind: WorkflowResultKind, artifactId: string, title: string, caseId?: string) => {
      if (!activeThreadId) {
        message.warning(t("selfEvolutionRun.noThreadForCase"), 2);
        return;
      }
      const resolvedArtifactId = resolveCaseArtifactId(artifactId, caseId);
      setActiveWorkbenchTab("processes");
      setActiveArtifactKind(kind);
      setIsArtifactPanelOpen(true);
      setPreviewHistoryKey(undefined);
      setHistoryPreviewTitle("");
      setHistoryPreviewMessages([]);
      setHistoryPreviewError("");
      setCaseArtifact({ kind, artifactId: resolvedArtifactId, caseId, title, loading: true });
      try {
        const response = await axiosInstance.get(`${AGENT_API_BASE}/threads/${encodeURIComponent(activeThreadId)}/artifacts/${encodeURIComponent(resolvedArtifactId)}`);
        setCaseArtifact({ kind, artifactId: resolvedArtifactId, caseId, title, loading: false, data: response.data });
      } catch (error) {
        setCaseArtifact({ kind, artifactId: resolvedArtifactId, caseId, title, loading: false, error: getLocalizedErrorMessage(error, t("selfEvolutionRun.caseArtifactLoadFailed", { title })) });
      }
    },
    [activeThreadId],
  );

  const closeArtifactPanel = useCallback(() => {
    setIsArtifactPanelOpen(false);
  }, []);

  const handleWorkbenchTabChange = (tab?: SelfEvolutionWorkbenchTab) => {
    setActiveWorkbenchTab(tab);
    if (tab !== "artifacts") {
      setActiveArtifactKind(undefined);
      setIsArtifactPanelOpen(false);
      setCaseArtifact(undefined);
    }
    if (tab === "messages" || !tab) {
      setPreviewHistoryKey(undefined);
      setHistoryPreviewTitle("");
      setHistoryPreviewMessages([]);
      setHistoryPreviewError("");
    }
  };

  useEffect(() => {
    if (activeWorkbenchTab === "artifacts" && activeArtifactKind) {
      void fetchWorkflowResult(activeArtifactKind);
    }
  }, [activeArtifactKind, activeWorkbenchTab, fetchWorkflowResult]);

  useEffect(() => {
    if (isWorkbenchVisible && activeStageArtifactKind) {
      void fetchWorkflowResult(activeStageArtifactKind);
    }
  }, [activeStageArtifactKind, fetchWorkflowResult, isWorkbenchVisible]);

  useEffect(() => {
    const resultState = workflowResults["eval-reports"];
    if (
      !resultState.loaded ||
      resultState.loading ||
      resultState.error ||
      isEmptyResultPayload(resultState.data)
    ) {
      return;
    }

    void fetchEvalReportBadCases(resultState.data);
  }, [
    fetchEvalReportBadCases,
    workflowResults["eval-reports"].data,
    workflowResults["eval-reports"].error,
    workflowResults["eval-reports"].loaded,
    workflowResults["eval-reports"].loading,
  ]);

  useEffect(() => {
    if (view === "detail" && routeThreadId && !isNewSessionConfigOpen) {
      setIsKnowledgeBaseLoading(false);
      return;
    }
    const controller = new AbortController();
    fetchKnowledgeBaseOptions(controller.signal);

    return () => {
      controller.abort();
    };
  }, [fetchKnowledgeBaseOptions, isNewSessionConfigOpen, routeThreadId, view]);

  useEffect(() => {
    setWorkflowResults(createInitialWorkflowResultsState());
    setEvalReportBadCases({ loading: false, loaded: false });
    setActiveArtifactKind(undefined);
    setIsArtifactPanelOpen(false);
    setCaseArtifact(undefined);
  }, [activeThreadId]);

  useEffect(() => {
    if (!activeThreadId || !activeRemoteThreadTitle) {
      return;
    }

    setChatSessions((prev) => {
      let hasChanged = false;
      const nextSessions = prev.map((session) => {
        if (session.threadId === activeThreadId && session.title !== activeRemoteThreadTitle) {
          hasChanged = true;
          return { ...session, title: activeRemoteThreadTitle };
        }
        return session;
      });
      return hasChanged ? nextSessions : prev;
    });
  }, [activeRemoteThreadTitle, activeThreadId]);

  useEffect(() => {
    const chatStream = chatStreamRef.current;
    if (!chatStream) {
      return;
    }
    chatStream.scrollTo({
      top: chatStream.scrollHeight,
      behavior: "auto",
    });
  }, [activeSessionId, displayedMessages.length]);

  useEffect(
    () => () => {
      if (datasetDownloadUrl) {
        URL.revokeObjectURL(datasetDownloadUrl);
      }
    },
    [datasetDownloadUrl],
  );

  useEffect(
    () => () => {
      threadEventsAbortRef.current?.controller.abort();
      threadEventsAbortRef.current = null;
    },
    [],
  );

  const knowledgeBaseMenuItems = useMemo<MenuProps["items"]>(() => {
    if (isKnowledgeBaseLoading) {
      return [
        {
          key: "__loading__",
          label: t("selfEvolutionRun.knowledgeBaseLoadingEllipsis"),
          disabled: true,
          icon: <LoadingOutlined spin />,
        },
      ];
    }

    if (knowledgeBaseError) {
      return [
        {
          key: "__retry__",
          label: t("selfEvolutionRun.knowledgeBaseRetryLabel", { error: knowledgeBaseError }),
          icon: <ReloadOutlined />,
        },
      ];
    }

    if (knowledgeBaseOptions.length === 0) {
      return [
        {
          key: "__empty__",
          label: t("selfEvolutionRun.noKnowledgeBase"),
          disabled: true,
        },
      ];
    }

    return knowledgeBaseOptions.map((item) => ({
      key: item.value,
      label: (
        <span className="self-evolution-knowledge-option" title={item.label}>
          {item.label}
        </span>
      ),
    }));
  }, [isKnowledgeBaseLoading, knowledgeBaseError, knowledgeBaseOptions, t]);

  const onKnowledgeBaseMenuClick = (
    key: string,
    onSelect: (nextKnowledgeBase: string) => void,
  ) => {
    if (key === "__retry__") {
      fetchKnowledgeBaseOptions();
      return;
    }
    if (key.startsWith("__")) {
      return;
    }

    onSelect(key);
  };

  const modeMenuItems: MenuProps["items"] = [
    { key: "auto", label: t("selfEvolutionRun.modeAuto") },
    { key: "interactive", label: t("selfEvolutionRun.modeInteractive") },
  ];

  const existingEvalSetMenuItems: MenuProps["items"] = [
    ...getExistingEvalSetOptions().map((item) => ({
      key: item.value,
      label: getExistingEvalSetLabel(item.value),
    })),
  ];
  const extraEvalStrategyMenuItems: MenuProps["items"] = [
    { key: FIXED_EXTRA_EVAL_STRATEGY, label: t("selfEvolutionRun.extraEvalGenerateWithModel") },
  ];
  const newSessionExtraEvalStrategyMenuItems: MenuProps["items"] = [
    { key: FIXED_EXTRA_EVAL_STRATEGY, label: t("selfEvolutionRun.extraEvalGenerateWithModel") },
  ];

  const localizedGetStepStatusLabel = useCallback(
    (status: WorkflowStep["status"]) => {
      const statusKeyMap: Record<WorkflowStep["status"], string> = {
        running: "selfEvolutionRun.status.running",
        pending: "selfEvolutionRun.status.pending",
        done: "selfEvolutionRun.status.done",
        paused: "selfEvolutionRun.status.paused",
        canceled: "selfEvolutionRun.status.canceled",
        failed: "selfEvolutionRun.status.failed",
      };
      return t(statusKeyMap[status]);
    },
    [t],
  );

  const buildSessionIntroContent = (
    targetKnowledgeBase: string,
    targetEvalSetLabel: string,
    targetExtraEvalLabel: string,
    targetInterventionLabel: string,
  ) =>
    t("selfEvolutionRun.sessionIntro", {
      knowledgeBase: targetKnowledgeBase,
      evalSet: targetEvalSetLabel,
      extraEval: targetExtraEvalLabel,
      intervention: targetInterventionLabel,
    });

  const extractThreadId = (response: AgentThreadCreateResponse) =>
    response.id ||
    response.thread_id ||
    response.data?.upstream?.id ||
    response.data?.upstream?.thread_id ||
    response.data?.thread?.thread_id ||
    response.data?.thread?.id;

  const showLocalErrorWhenNotHandledByAxios = (error: unknown, fallback: string) => {
    if ((error as { isAxiosError?: boolean })?.isAxiosError) {
      return;
    }
    message.error(getLocalizedErrorMessage(error, fallback) || fallback, 2);
  };

  const createAndStartThread = async (config?: {
    mode: EvolutionMode;
    selectedKb: string;
    selectedKnowledgeBase: string;
    selectedEvalSet: string;
  }) => {
    const targetMode = config?.mode || mode;
    const targetSelectedKb = config?.selectedKb || selectedKb;
    const targetKnowledgeBase = config?.selectedKnowledgeBase || selectedKnowledgeBase;
    const targetEvalSet = config?.selectedEvalSet || selectedEvalSet;
    const evalName =
      targetEvalSet && targetEvalSet !== FIXED_EVAL_SET
        ? targetEvalSet
        : `eval_${new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14)}`;

    const createResponse = await axiosInstance.post<AgentThreadCreateResponse>(`${AGENT_API_BASE}/threads`, {
      mode: targetMode,
      title: targetKnowledgeBase || "self evolution test",
      inputs: {
        kb_id: targetSelectedKb,
        algo_id: "general_algo",
        eval_name: evalName,
        num_cases: DEFAULT_EVAL_CASE_COUNT,
      },
    });
    const threadId = extractThreadId(createResponse.data);
    if (!threadId) {
      throw new Error(t("selfEvolutionRun.createThreadMissingId"));
    }

    await axiosInstance.post(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}:start`, {});
    return threadId;
  };

  const appendMessageToSession = (
    sessionId: string,
    nextMessage: ChatMessage,
    options?: { title?: string; dedupeLast?: boolean },
  ) => {
    setChatSessions((prev) =>
      prev.map((session) => {
        if (session.id !== sessionId) {
          return session;
        }

        const lastMessage = session.messages[session.messages.length - 1];
        if (
          options?.dedupeLast &&
          lastMessage?.role === nextMessage.role &&
          lastMessage.content === nextMessage.content
        ) {
          return {
            ...session,
            updatedAt: nextMessage.time,
          };
        }

        return {
          ...session,
          title: options?.title || session.title,
          updatedAt: nextMessage.time,
          messages: [...session.messages, nextMessage],
        };
      }),
    );
  };

  const appendSystemMessage = (content: string, sessionId = activeSessionId) => {
    const nowLabel = getTimeLabel();
    appendMessageToSession(sessionId, {
      id: `assistant-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      role: "assistant",
      content,
      time: nowLabel,
    });
  };

  const replaceThreadEvents = (events: NormalizedThreadEvent[]) => {
    threadEventsRef.current = events;
    setTerminalFlowStepStatus(undefined);
    setLiveCheckpointWaitPrompt(undefined);
    setThreadEvents(events);
  };

  const resetThreadStepViewSelection = () => {
    setSelectedViewStage(undefined);
    setSelectedThreadStepId(undefined);
    setLoadingThreadStepId(undefined);
    loadingThreadStepIdRef.current = undefined;
  };

  const mergeThreadEvents = (events: NormalizedThreadEvent[]) => {
    const mergedEvents = dedupeNormalizedEvents([...threadEventsRef.current, ...events]);
    threadEventsRef.current = mergedEvents;
    setThreadEvents(mergedEvents);
    return mergedEvents;
  };

  const appendStreamDeltaToSession = (
    sessionId: string,
    kind: ChatStreamDeltaKind,
    delta: string | undefined,
    streamId = "default",
  ) => {
    if (!delta || delta === legacyPlanningThinkingText) {
      return;
    }

    const nowLabel = getTimeLabel();
    const streamMessageId = `${sessionId}-assistant-stream-${streamId}`;
    const thinkingPrefix = t("selfEvolutionRun.thinkingPrefix");
    const initialContent = kind === "thinking" ? `${thinkingPrefix}${delta}` : delta;
    const getNextContent = (currentMessage: ChatMessage) => {
      if (kind === "thinking") {
        return `${currentMessage.content}${delta}`;
      }
      const needsAnswerSeparator =
        currentMessage.content.startsWith(thinkingPrefix) && !currentMessage.streamAnswerStarted;
      return `${currentMessage.content}${needsAnswerSeparator ? "\n\n" : ""}${delta}`;
    };

    setChatSessions((prev) =>
      prev.map((session) => {
        if (session.id !== sessionId) {
          return session;
        }

        const existingIndex = session.messages.findIndex((item) => item.id === streamMessageId);
        if (existingIndex >= 0) {
          const messages = [...session.messages];
          const current = messages[existingIndex];
          messages[existingIndex] = {
            ...current,
            content: getNextContent(current),
            time: nowLabel,
            streamAnswerStarted: kind === "answer" ? true : current.streamAnswerStarted,
          };
          return {
            ...session,
            updatedAt: nowLabel,
            messages,
          };
        }

        return {
          ...session,
          updatedAt: nowLabel,
          messages: [
            ...session.messages,
            {
              id: streamMessageId,
              role: "assistant",
              content: initialContent,
              time: nowLabel,
              streamAnswerStarted: kind === "answer",
            },
          ],
        };
      }),
    );
  };

  const applyWorkflowEvent = (
    event: NormalizedThreadEvent,
    sessionId = activeSessionId,
    options?: { appendChat?: boolean },
  ) => {
    const isNewEvent = !processedWorkflowEventKeysRef.current.has(event.key);
    if (!isNewEvent) {
      return;
    }

    processedWorkflowEventKeysRef.current.add(event.key);
    mergeThreadEvents([event]);
    if (event.checkpointWait) {
      setLiveCheckpointWaitPrompt(event.checkpointWait);
    } else {
      setLiveCheckpointWaitPrompt((prev) => {
        if (!prev) {
          return prev;
        }
        if (
          prev.kind === "failure" &&
          (event.type === "message.user" ||
            event.type === "message.assistant" ||
            event.type === "intent.reply" ||
            event.type === "intent.thought")
        ) {
          return undefined;
        }
        if (
          event.type === "checkpoint.continue" ||
          event.type === "checkpoint.rewind" ||
          event.type === "checkpoint.cancel" ||
          isInactiveTerminalThreadEvent(event)
        ) {
          return undefined;
        }
        if (event.type.startsWith("autooperator.")) {
          return prev;
        }
        if (prev.nextStage && event.stage === prev.nextStage) {
          return undefined;
        }
        const checkpointEvents = threadEventsRef.current
          .filter((item) => item.type === "checkpoint.wait" && item.checkpointWait)
          .sort(compareNormalizedThreadEvents);
        const latestCheckpointEvent = checkpointEvents.length
          ? checkpointEvents[checkpointEvents.length - 1]
          : undefined;
        if (latestCheckpointEvent && event.stage && isThreadEventAfter(latestCheckpointEvent, event)) {
          return undefined;
        }
        return prev;
      });
    }

    const shouldAppendChat = options?.appendChat ?? true;
    if (shouldAppendChat) {
      const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
      if (chatStreamDeltaKind) {
        const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
        appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
      }
      const dialogueAgentLabel = getDialogueEventAgentLabel(event);
      if (event.role && event.content && dialogueAgentLabel) {
        appendMessageToSession(
          sessionId,
          {
            id: `event-chat-${event.key}`,
            role: event.role,
            content: event.content,
            time: formatThreadTime(event.timestamp),
            sortTime:
              getThreadTimeSortValue(event.timestamp) ||
              (typeof event.sequence === "number" ? event.sequence : undefined),
            agentLabel: mode === "auto" ? dialogueAgentLabel : undefined,
          },
          { dedupeLast: true },
        );
      }
    }
    if (!event.stage) {
      return;
    }
    setWorkflowRuntimeState((prev) => reduceWorkflowRuntimeState(prev, event));
  };

  const syncPlanningStateFromMessageEvent = (event: NormalizedThreadEvent) => {
    if (event.type === "intent_start") {
      setIsPlanningNextStep(true);
    }
    if (["answer_delta", "plan_ready", "action", "done", "error"].includes(event.type)) {
      setIsPlanningNextStep(false);
    }
  };

  const rememberNextStepRunId = (event: NormalizedThreadEvent) => {
    const nextStepRunId = getNextStepRunId(event);
    if (nextStepRunId) {
      pendingNextStepRunIdRef.current = nextStepRunId;
    }
    return nextStepRunId;
  };

  const subscribeNextStepWithEventsFirst = async (
    threadId: string,
    nextStepRunId: string,
    sessionId: string,
  ) => {
    await waitForStepEventsStreamConnected(threadId, nextStepRunId, sessionId);
    await refreshThreadStepList(threadId).catch(() => undefined);
  };

  const subscribePendingNextStepRun = (threadId: string | undefined, sessionId: string) => {
    const nextStepRunId = pendingNextStepRunIdRef.current;
    if (!threadId || !nextStepRunId) {
      return false;
    }

    pendingNextStepRunIdRef.current = undefined;
    void subscribeNextStepWithEventsFirst(threadId, nextStepRunId, sessionId);
    return true;
  };

  const subscribePendingNextStepRunOrRestoreLatest = async (
    threadId: string,
    sessionId: string,
  ) => {
    if (subscribePendingNextStepRun(threadId, sessionId)) {
      return;
    }

    const cachedNextStepRunId = resolveNextStepRunIdFromStepList(threadStepList);
    if (cachedNextStepRunId) {
      pendingNextStepRunIdRef.current = cachedNextStepRunId;
      if (subscribePendingNextStepRun(threadId, sessionId)) {
        return;
      }
    }

    try {
      const stepList = await refreshThreadStepList(threadId);
      const nextStepRunId = resolveNextStepRunIdFromStepList(stepList);
      if (nextStepRunId) {
        pendingNextStepRunIdRef.current = nextStepRunId;
        if (subscribePendingNextStepRun(threadId, sessionId)) {
          return;
        }
      }
    } catch {
      // Fall back to restoring the latest completed step records.
    }

    void restoreLatestThreadStep(threadId, sessionId);
  };

  const subscribeNextStepRunAfterContinue = async (
    threadId: string,
    sessionId: string,
  ) => {
    const cachedNextStepRunId =
      pendingNextStepRunIdRef.current ||
      resolveNextStepRunIdFromStepList(threadStepList);

    setLiveCheckpointWaitPrompt(undefined);
    pendingNextStepRunIdRef.current = undefined;

    if (cachedNextStepRunId) {
      await subscribeNextStepWithEventsFirst(threadId, cachedNextStepRunId, sessionId);
      return;
    }

    const stepList = await refreshThreadStepList(threadId);
    const nextStepRunId = resolveNextStepRunIdFromStepList(stepList);
    if (nextStepRunId) {
      await subscribeNextStepWithEventsFirst(threadId, nextStepRunId, sessionId);
      return;
    }

    void restoreLatestThreadStep(threadId, sessionId, undefined, stepList);
  };

  const consumeThreadMessageStream = async (
    response: Response,
    sessionId: string,
    signal?: AbortSignal,
  ): Promise<void> => {
    if (!response.body) {
      return;
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";

    while (true) {
      const { value, done } = await reader.read();
      if (done || signal?.aborted) {
        break;
      }

      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split(/\r?\n\r?\n/);
      buffer = frames.pop() || "";

      for (const rawFrame of frames) {
        const frame = parseSSEFrame(rawFrame.trim());
        if (!frame) {
          continue;
        }

        const event = normalizeThreadEvent(frame);
        rememberNextStepRunId(event);
        syncPlanningStateFromMessageEvent(event);
        const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
        if (chatStreamDeltaKind) {
          const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
          appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
        }
        if (!chatStreamDeltaKind && event.role && event.content) {
          appendMessageToSession(sessionId, {
            id: `event-chat-${event.key}`,
            role: event.role,
            content: event.content,
            time: formatThreadTime(event.timestamp),
          }, { dedupeLast: true });
        }
        if (event.type === "done") {
          await reader.cancel().catch(() => undefined);
          return;
        }
      }
    }

    const trailingText = buffer.trim();
    if (trailingText) {
      const frame = parseSSEFrame(trailingText);
      if (frame) {
        const event = normalizeThreadEvent(frame);
        rememberNextStepRunId(event);
        syncPlanningStateFromMessageEvent(event);
        const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
        if (chatStreamDeltaKind) {
          const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
          appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
        }
        if (!chatStreamDeltaKind && event.role && event.content) {
          appendMessageToSession(sessionId, {
            id: `event-chat-${event.key}`,
            role: event.role,
            content: event.content,
            time: formatThreadTime(event.timestamp),
          }, { dedupeLast: true });
        }
      }
    }
  };

  const openStepEventsResponse = async (
    threadId: string,
    stepId: string,
    signal: AbortSignal,
    allowRefresh = true,
  ): Promise<Response> => {
    const response = await fetch(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/events/${encodeURIComponent(stepId)}`, {
      method: "GET",
      headers: {
        Accept: "text/event-stream",
        ...AgentAppsAuth.getAuthHeaders(),
      },
      signal,
    });

    if (response.status === 401 && allowRefresh && !signal.aborted) {
      await AgentAppsAuth.refreshAccessToken();
      return openStepEventsResponse(threadId, stepId, signal, false);
    }

    return response;
  };

  const fetchThreadStepList = async (
    threadId: string,
    signal?: AbortSignal,
    silentError = false,
  ) => {
    const response = await axiosInstance.get(
      `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/steps`,
      silentError ? getSilentRestoreRequestConfig(signal) : { signal },
    );
    return normalizeThreadStepListPayload(response.data as ThreadRestorePayload);
  };

  const refreshThreadStepList = async (
    threadId: string,
    signal?: AbortSignal,
  ) => {
    const stepList = await fetchThreadStepList(threadId, signal);
    if (!signal?.aborted) {
      setThreadStepList(stepList);
      const nextStepRunId = resolveNextStepRunIdFromStepList(stepList);
      if (nextStepRunId) {
        pendingNextStepRunIdRef.current = nextStepRunId;
      }
    }
    return stepList;
  };

  const restoreThreadStepRecords = async (
    threadId: string,
    stepId: string,
    signal?: AbortSignal,
  ) => {
    const restoredEvents = await fetchThreadStepRecords(threadId, stepId, signal);
    if (signal?.aborted) {
      return [];
    }

    const pendingEvents = restoredEvents.filter((event) => !processedWorkflowEventKeysRef.current.has(event.key));
    if (pendingEvents.length === 0) {
      return [];
    }

    pendingEvents.forEach((event) => processedWorkflowEventKeysRef.current.add(event.key));
    const mergedEvents = mergeThreadEvents(pendingEvents);
    setWorkflowRuntimeState((prev) => reduceWorkflowRuntimeStateFromEvents(prev, pendingEvents));
    setLiveCheckpointWaitPrompt(getPendingCheckpointWaitPrompt(mergedEvents));
    return pendingEvents;
  };

  const fetchThreadStepRecords = async (
    threadId: string,
    stepId: string,
    signal?: AbortSignal,
  ) => {
    const response = await axiosInstance.get(
      `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/steps/${encodeURIComponent(stepId)}/records`,
      { signal },
    );
    if (signal?.aborted) {
      return [];
    }
    return normalizeThreadRecordEvents(response.data as ThreadRestorePayload);
  };

  const applyThreadStepRecordsToView = (
    restoredEvents: NormalizedThreadEvent[],
    viewStage?: string,
  ) => {
    if (viewStage) {
      setSelectedViewStage(viewStage);
    } else {
      const lastStageEvent = [...restoredEvents].reverse().find((event) => event.stage);
      if (lastStageEvent?.stage) {
        setSelectedViewStage(lastStageEvent.stage);
      }
    }

    if (restoredEvents.length === 0) {
      return;
    }

    restoredEvents.forEach((event) => {
      processedWorkflowEventKeysRef.current.add(event.key);
    });
    const mergedEvents = mergeThreadEvents(restoredEvents);
    setWorkflowRuntimeState(
      reduceWorkflowRuntimeStateFromEvents(
        createThreadRestoreWorkflowRuntimeState(),
        mergedEvents,
      ),
    );
    setLiveCheckpointWaitPrompt(getPendingCheckpointWaitPrompt(mergedEvents));
  };

  const onSelectThreadStep = async (
    step: ThreadStepSummary,
    workflowStepId?: WorkflowStep["id"],
  ) => {
    const activeThreadId = activeSession?.threadId || routeThreadId;
    if (!activeThreadId || !step.stepId) {
      return;
    }
    if (loadingThreadStepIdRef.current === step.stepId) {
      return;
    }

    loadingThreadStepIdRef.current = step.stepId;
    setLoadingThreadStepId(step.stepId);
    setSelectedThreadStepId(step.stepId);

    const viewStage = workflowStepId ? workflowStepStageMap[workflowStepId] : undefined;

    try {
      const restoredEvents = await fetchThreadStepRecords(activeThreadId, step.stepId);
      applyThreadStepRecordsToView(restoredEvents, viewStage);

      const artifactKind = viewStage ? stageArtifactKindMap[viewStage] : undefined;
      if (artifactKind) {
        openWorkflowArtifact(artifactKind);
      }

      if (isThreadStepRunning(step)) {
        void subscribeThreadEvents(activeThreadId, step.stepId, activeSessionId);
      }
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("selfEvolutionRun.stepRecordsLoadFailed")) ||
          t("selfEvolutionRun.stepRecordsLoadFailed"),
        2,
      );
    } finally {
      if (loadingThreadStepIdRef.current === step.stepId) {
        loadingThreadStepIdRef.current = undefined;
        setLoadingThreadStepId(undefined);
      }
    }
  };

  const restoreLatestThreadStep = async (
    threadId: string,
    sessionId = activeSessionId,
    signal?: AbortSignal,
    preloadedStepList?: ThreadStepListState,
    shouldSubscribeInitialStepIfEmpty = false,
  ) => {
    const stepList = preloadedStepList || await refreshThreadStepList(threadId, signal);
    if (signal?.aborted) {
      return;
    }
    if (preloadedStepList) {
      setThreadStepList(preloadedStepList);
    }

    const latestStep = getDefaultThreadStep(stepList);
    if (!latestStep) {
      if (shouldSubscribeInitialStepIfEmpty) {
        void subscribeThreadEvents(threadId, INITIAL_THREAD_STEP_ID, sessionId);
      }
      return;
    }

    if (isThreadStepRunning(latestStep)) {
      void subscribeThreadEvents(threadId, latestStep.stepId, sessionId);
      return;
    }

    await restoreThreadStepRecords(threadId, latestStep.stepId, signal);
  };

  const waitForStepEventsStreamConnected = (
    threadId: string,
    stepId: string,
    sessionId: string,
  ) => new Promise<void>((resolve, reject) => {
    let settled = false;
    const settleResolve = () => {
      if (!settled) {
        settled = true;
        resolve();
      }
    };
    const settleReject = (error: unknown) => {
      if (!settled) {
        settled = true;
        reject(error);
      }
    };
    void subscribeThreadEvents(threadId, stepId, sessionId, {
      onStreamConnected: settleResolve,
    }).catch(settleReject);
  });

  const subscribeThreadEvents = async (
    threadId: string,
    stepId: string,
    sessionId = activeSessionId,
    options?: { onStreamConnected?: () => void },
  ) => {
    const activeSubscription = threadEventsAbortRef.current;
    if (
      activeSubscription?.threadId === threadId &&
      activeSubscription.stepId === stepId &&
      !activeSubscription.controller.signal.aborted
    ) {
      options?.onStreamConnected?.();
      return;
    }

    if (activeSubscription && !activeSubscription.controller.signal.aborted) {
      activeSubscription.controller.abort();
    }
    processedThreadEventIdsRef.current = new Set();

    const controller = new AbortController();
    const subscription = { threadId, stepId, controller };
    threadEventsAbortRef.current = subscription;
    const shouldAppendEventChat = mode === "auto";

    try {
      const response = await openStepEventsResponse(threadId, stepId, controller.signal);

      if (!response.ok) {
        throw new Error(`SSE connection failed: HTTP ${response.status}`);
      }
      if (!response.body) {
        throw new Error("SSE connection failed: no readable stream returned by browser");
      }

      options?.onStreamConnected?.();

      const reader = response.body.getReader();
      const decoder = new TextDecoder("utf-8");
      let buffer = "";

      while (true) {
        const { value, done } = await reader.read();
        if (done || controller.signal.aborted) {
          break;
        }

        buffer += decoder.decode(value, { stream: true });
        const frames = buffer.split(/\r?\n\r?\n/);
        buffer = frames.pop() || "";

        for (const rawFrame of frames) {
          const frame = parseSSEFrame(rawFrame.trim());
          if (!frame) {
            continue;
          }

          if (frame.id) {
            if (processedThreadEventIdsRef.current.has(frame.id)) {
              continue;
            }
            processedThreadEventIdsRef.current.add(frame.id);
          }

          const event = normalizeThreadEvent(frame);
          rememberNextStepRunId(event);
          applyWorkflowEvent(event, sessionId, { appendChat: shouldAppendEventChat });
          if (event.type === "done") {
            await reader.cancel().catch(() => undefined);
            controller.abort();
            return;
          }
        }
      }

      const trailingText = buffer.trim();
      if (!controller.signal.aborted && trailingText) {
        const frame = parseSSEFrame(trailingText);
        if (frame) {
          const event = normalizeThreadEvent(frame);
          rememberNextStepRunId(event);
          applyWorkflowEvent(event, sessionId, { appendChat: shouldAppendEventChat });
          if (event.type === "done") {
            controller.abort();
          }
        }
      }
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      message.error(getLocalizedErrorMessage(error, t("selfEvolutionRun.sseConnectionError")), 2);
    } finally {
      if (threadEventsAbortRef.current === subscription) {
        threadEventsAbortRef.current = null;
      }
    }
  };

  const restoreThreadDetail = async (threadId: string, signal?: AbortSignal) => {
    const requestId = restoreRequestIdRef.current + 1;
    restoreRequestIdRef.current = requestId;
    setIsRestoringThread(true);
    setThreadRestoreError("");
    setIsWorkbenchVisible(true);
    setWorkflowRuntimeState(createThreadRestoreWorkflowRuntimeState());
    replaceThreadEvents([]);
    setThreadStepList({ steps: [] });
    resetThreadStepViewSelection();
    processedWorkflowEventKeysRef.current = new Set();
    pendingNextStepRunIdRef.current = undefined;
    setLiveCheckpointWaitPrompt(undefined);
    if (threadEventsAbortRef.current && !threadEventsAbortRef.current.controller.signal.aborted) {
      threadEventsAbortRef.current.controller.abort();
    }
    threadEventsAbortRef.current = null;

    const restoredSessionId = `thread-${threadId}`;
    setActiveSessionId(restoredSessionId);
    setChatSessions([
      {
        id: restoredSessionId,
        title: t("selfEvolutionRun.restoringThreadTitle"),
        updatedAt: getTimeLabel(),
        threadId,
        messages: [
          {
            id: `${threadId}-restore-loading`,
            role: "assistant",
            content: t("selfEvolutionRun.restoringThreadContent", { id: threadId }),
            time: getTimeLabel(),
          },
        ],
      },
    ]);

    try {
      const encodedThreadId = encodeURIComponent(threadId);
      const restoredStepList = await fetchThreadStepList(threadId, signal, true);
      if (signal?.aborted || restoreRequestIdRef.current !== requestId) {
        return;
      }
      setThreadStepList(restoredStepList);
      const restoredNextStepRunId = resolveNextStepRunIdFromStepList(restoredStepList);
      if (restoredNextStepRunId) {
        pendingNextStepRunIdRef.current = restoredNextStepRunId;
      }

      let historyTitle: string | undefined;
      let historyMessages: ChatMessage[] = [];

      try {
        const historyPayload = (
          await axiosInstance.get(
            `${AGENT_API_BASE}/threads/${encodedThreadId}/history`,
            getSilentRestoreRequestConfig(signal),
          )
        ).data as ThreadRestorePayload;
        historyTitle = getThreadTitleFromHistoryPayload(historyPayload);
        historyMessages = normalizeThreadHistoryMessages(historyPayload);
      } catch (error) {
        if (signal?.aborted || isCanceledRequest(error)) {
          return;
        }
      }

      if (signal?.aborted || restoreRequestIdRef.current !== requestId) {
        return;
      }

      const applySessionRestore = (title?: string, forceUseHistoryMessages = false) => {
        const nowLabel = getTimeLabel();
        setChatSessions((prev) =>
          prev.map((session) =>
            session.id === restoredSessionId
              ? {
                  ...session,
                  title: title || session.title,
                  updatedAt: nowLabel,
                  threadId,
                  messages:
                    historyMessages.length > 0
                      ? historyMessages
                      : forceUseHistoryMessages &&
                          session.messages.length === 1 &&
                          session.messages[0]?.id === `${threadId}-restore-loading`
                        ? []
                        : session.messages,
                }
              : session,
          ),
        );
      };

      const titleFromHistory =
        historyTitle ||
        remoteThreadHistory.find((item) => item.threadId === threadId)?.title ||
        `${t("selfEvolutionRun.selfEvolutionDetail")} ${threadId.slice(0, 8)}`;
      applySessionRestore(titleFromHistory, true);
      setActiveSessionId(restoredSessionId);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);

      const threadResult = await axiosInstance.get(`${AGENT_API_BASE}/threads/${encodedThreadId}`, getSilentRestoreRequestConfig(signal));
      if (signal?.aborted || restoreRequestIdRef.current !== requestId) {
        return;
      }

      const threadPayload = threadResult.data as ThreadRestorePayload;
      const detailTitle = getThreadTitleFromPayload(threadPayload);
      const knowledgeBaseId = getThreadKnowledgeBaseId(threadPayload);
      if (knowledgeBaseId) {
        setSelectedKb(knowledgeBaseId);
      }
      const restoredMode = getThreadModeFromPayload(threadPayload);
      if (restoredMode) {
        setMode(restoredMode);
      }
      if (!historyTitle && detailTitle) {
        applySessionRestore(detailTitle);
      }
      let restoredFlowStatus = isRecord(threadPayload)
        ? getNestedStringField(threadPayload, ["status", "state"])
        : undefined;
      let flowPendingCheckpoint: Record<string, unknown> | undefined;
      try {
        const flowStatusResult = await axiosInstance.get(`${AGENT_API_BASE}/threads/${encodedThreadId}/flow-status`, getSilentRestoreRequestConfig(signal));
        const flowStatusPayload = flowStatusResult.data;
        restoredFlowStatus = isRecord(flowStatusPayload)
          ? getStringField(flowStatusPayload, ["status", "state"]) || restoredFlowStatus
          : restoredFlowStatus;
        flowPendingCheckpoint = isRecord(flowStatusPayload)
          ? getNestedRecordField(flowStatusPayload, ["pending_checkpoint", "pendingCheckpoint"])
          : undefined;
      } catch (error) {
        if (signal?.aborted || isCanceledRequest(error)) {
          return;
        }
      }
      const pendingCheckpoint = flowPendingCheckpoint || (isRecord(threadPayload)
        ? getNestedRecordField(threadPayload, ["pending_checkpoint", "pendingCheckpoint"])
        : undefined);
      const nextTerminalFlowStepStatus = getTerminalFlowStepStatus(restoredFlowStatus);
      setTerminalFlowStepStatus(nextTerminalFlowStepStatus);
      if (nextTerminalFlowStepStatus) {
        setLiveCheckpointWaitPrompt(undefined);
      }
      if (!nextTerminalFlowStepStatus && pendingCheckpoint) {
        const checkpointEvent = normalizeThreadEvent({
          id: `restore-checkpoint-${threadId}-${getStringField(pendingCheckpoint, ["checkpoint_id", "id"]) || "latest"}`,
          eventName: "checkpoint.wait",
          data: JSON.stringify({
            type: "checkpoint.wait",
            ...pendingCheckpoint,
          }),
        });
        if (checkpointEvent.checkpointWait) {
          processedWorkflowEventKeysRef.current.add(checkpointEvent.key);
          mergeThreadEvents([checkpointEvent]);
          setLiveCheckpointWaitPrompt(checkpointEvent.checkpointWait);
          setWorkflowRuntimeState(createCheckpointRestoreWorkflowRuntimeState(checkpointEvent.checkpointWait));
        }
      }
      await restoreLatestThreadStep(
        threadId,
        restoredSessionId,
        signal,
        restoredStepList,
        isThreadFlowRunning(restoredFlowStatus) && !pendingCheckpoint,
      );
    } catch (error) {
      if (signal?.aborted || isCanceledRequest(error)) {
        return;
      }
      const responseStatus = (error as AxiosError | undefined)?.response?.status;
      const errorTextRaw = getLocalizedErrorMessage(error, t("selfEvolutionRun.threadDetailRestoreFailed")) || "";
      const isThreadNotFound = responseStatus === 404 && errorTextRaw.toLowerCase().includes("thread not found");
      if (isThreadNotFound) {
        setWorkflowRuntimeState(createThreadRestoreWorkflowRuntimeState());
        setWorkflowResults(createInitialWorkflowResultsState());
        setCaseArtifact(undefined);
      }
      const errorText =
        isThreadNotFound ? t("selfEvolutionRun.threadNotFoundFull") :
        errorTextRaw ||
        t("selfEvolutionRun.threadDetailRestoreFailed");
      setThreadRestoreError(errorText);
      setChatSessions([
        {
          id: restoredSessionId,
          title: `${t("selfEvolutionRun.selfEvolutionDetail")} ${threadId.slice(0, 8)}`,
          updatedAt: getTimeLabel(),
          threadId,
          messages: [
            {
              id: `${threadId}-restore-error`,
              role: "assistant",
              content: errorText,
              time: getTimeLabel(),
            },
          ],
        },
      ]);
    } finally {
      if (!signal?.aborted && restoreRequestIdRef.current === requestId) {
        setIsRestoringThread(false);
      }
    }
  };

  useEffect(() => {
    if (!routeThreadId) {
      return;
    }

    const controller = new AbortController();
    void restoreThreadDetail(routeThreadId, controller.signal);

    return () => {
      controller.abort();
    };
  }, [routeThreadId]);

  useEffect(() => {
    resetThreadStepViewSelection();
  }, [activeSessionId]);

  const onSend = async (command?: string) => {
    const trimmedPrompt = (command ?? prompt).trim();
    const activeThreadId = activeSession?.threadId || routeThreadId;
    if (isKnowledgeBaseRequired && !activeThreadId) {
      setHasLaunchValidationTriggered(true);
      message.warning(t("selfEvolutionRun.message.selectKnowledgeBaseBeforeStart"), 1.2);
      return;
    }
    if (!trimmedPrompt) {
      return;
    }

    if (
      activeThreadId &&
      pendingCheckpointWaitPrompt?.kind === "checkpoint" &&
      pendingCheckpointWaitPrompt.checkpointKind !== "intent_confirmation" &&
      pendingCheckpointWaitPrompt.checkpointKind !== "manual_cutover" &&
      isCheckpointContinueCommand(
        trimmedPrompt,
        pendingCheckpointWaitPrompt,
        t("selfEvolutionRun.continueExecution"),
        getCheckpointCommandText(),
      )
    ) {
      setPrompt("");
      void onContinueCheckpoint(trimmedPrompt);
      return;
    }

    const nowLabel = getTimeLabel();
    appendMessageToSession(
      activeSessionId,
      {
        id: `user-${Date.now()}`,
        role: "user",
        content: trimmedPrompt,
        time: nowLabel,
      },
    );
    setPrompt("");

    if (activeThreadId) {
      setIsSendingMessage(true);
      setIsPlanningNextStep(true);
      const controller = new AbortController();
      try {
        const response = await fetch(`${AGENT_API_BASE}/threads/${encodeURIComponent(activeThreadId)}:messages`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "text/event-stream",
            ...AgentAppsAuth.getAuthHeaders(),
          },
          body: JSON.stringify({
            type: "message.user",
            role: "user",
            message: trimmedPrompt,
            content: trimmedPrompt,
          }),
          signal: controller.signal,
        });

        if (!response.ok) {
          throw new Error(`Message send failed: HTTP ${response.status}`);
        }

        const contentType = response.headers.get("content-type") || "";
        if (contentType.includes("text/event-stream")) {
          await consumeThreadMessageStream(
            response,
            activeSessionId,
            controller.signal,
          );
          void subscribePendingNextStepRunOrRestoreLatest(activeThreadId, activeSessionId);
          return;
        }

        const responseData = await response.json().catch(() => undefined);
        const responsePayload = isRecord(responseData) ? responseData : undefined;
        const responseText = getNestedStringField(responsePayload, ["message", "content", "text", "reply"]);
        if (responseText) {
          appendMessageToSession(
            activeSessionId,
            {
              id: `assistant-${Date.now()}-${Math.random().toString(16).slice(2)}`,
              role: "assistant",
              content: responseText,
              time: getTimeLabel(),
            },
            { dedupeLast: true },
          );
        }
        void subscribePendingNextStepRunOrRestoreLatest(activeThreadId, activeSessionId);
      } catch (error) {
        appendSystemMessage(
          getLocalizedErrorMessage(error, t("selfEvolutionRun.messageSendFailed")) ||
            t("selfEvolutionRun.messageSendFailed"),
          activeSessionId,
        );
      } finally {
        setIsSendingMessage(false);
        setIsPlanningNextStep(false);
      }
      return;
    }

    appendSystemMessage(t("selfEvolutionRun.startFlowBeforeMessage"), activeSessionId);
  };

  const onContinueCheckpoint = async (command = t("selfEvolutionRun.continueExecution")) => {
    const activeThreadId = activeSession?.threadId || routeThreadId;
    if (!activeThreadId) {
      appendSystemMessage(t("selfEvolutionRun.startFlowBeforeContinue"), activeSessionId);
      return;
    }

    appendMessageToSession(
      activeSessionId,
      {
        id: `user-continue-checkpoint-${Date.now()}`,
        role: "user",
        content: command,
        time: getTimeLabel(),
      },
    );
    setIsSendingMessage(true);
    setIsPlanningNextStep(true);
    try {
      const response = await axiosInstance.post(
        `${AGENT_API_BASE}/threads/${encodeURIComponent(activeThreadId)}:continue`,
        {},
      );
      const responsePayload = isRecord(response.data) ? response.data : {};
      if (responsePayload.resumed === false) {
        const blockReason = getStringField(responsePayload, ["block_reason", "blockReason"]);
        throw new Error(
          blockReason === "flow_busy"
            ? t("selfEvolutionRun.flowBusyError")
            : t("selfEvolutionRun.continueNotEffectiveError"),
        );
      }
      appendMessageToSession(
        activeSessionId,
        {
          id: `assistant-continue-checkpoint-${Date.now()}`,
          role: "assistant",
          content: t("selfEvolutionRun.continueConfirmedMessage"),
          time: getTimeLabel(),
        },
        { dedupeLast: true },
      );
      await subscribeNextStepRunAfterContinue(activeThreadId, activeSessionId);
    } catch (error) {
      appendSystemMessage(
        getLocalizedErrorMessage(error, t("selfEvolutionRun.continueFailed")) ||
          t("selfEvolutionRun.continueFailed"),
        activeSessionId,
      );
    } finally {
      setIsSendingMessage(false);
      setIsPlanningNextStep(false);
    }
  };

  const onConfirmIntentCheckpoint = () => {
    void onSend(t("selfEvolutionRun.confirmExecution"));
  };

  const onStartSession = async () => {
    if (isStartingSession) {
      return;
    }
    if (!isLaunchConfigValid) {
      setHasLaunchValidationTriggered(true);
      if (!selectedKb) {
        message.warning(t("selfEvolutionRun.message.selectKnowledgeBaseBeforeStart"), 1.2);
        return;
      }
      if (!selectedEvalSet) {
        message.warning(t("selfEvolutionRun.message.selectExistingEvalSetStrategy"), 1.2);
        return;
      }
      if (!extraEvalStrategy) {
        message.warning(t("selfEvolutionRun.message.selectExtraEvalStrategy"), 1.2);
        return;
      }
      if (!mode) {
        message.warning(t("selfEvolutionRun.message.selectInterventionMode"), 1.2);
        return;
      }
      message.warning(t("selfEvolutionRun.message.completeFirstFourSteps"), 1.2);
      return;
    }

    setIsStartingSession(true);
    try {
      const threadId = await createAndStartThread();
      setWorkflowRuntimeState(createWorkflowRuntimeStateForMode(mode));
      replaceThreadEvents([]);
      setThreadStepList({ steps: [] });
      resetThreadStepViewSelection();
      processedWorkflowEventKeysRef.current = new Set();
      pendingNextStepRunIdRef.current = undefined;
      setIsWorkbenchVisible(true);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);
      const nowLabel = getTimeLabel();
      setChatSessions((prev) =>
        prev.map((session) =>
          session.id === activeSessionId
            ? {
                ...session,
                threadId,
                title: session.title === t("selfEvolutionRun.currentSession") ? selectedKnowledgeBase : session.title,
                updatedAt: nowLabel,
                messages:
                  session.messages.length === 0
                    ? [
                        {
                          id: `assistant-${Date.now()}`,
                          role: "assistant",
                          content: `${buildSessionIntroContent(
                            selectedKnowledgeBase,
                            selectedEvalSetLabel,
                            extraEvalLabel,
                            interventionLabel,
                          )}\n\n${t("selfEvolutionRun.threadIdLabel", { id: threadId })}`,
                          time: nowLabel,
                        },
                      ]
                    : session.messages,
              }
            : session,
        ),
      );
      void subscribeThreadEvents(threadId, INITIAL_THREAD_STEP_ID, activeSessionId);
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      message.success(t("selfEvolutionRun.flowStartedSuccess"), 1.2);
    } catch (error) {
      showLocalErrorWhenNotHandledByAxios(error, t("selfEvolutionRun.flowStartFailed"));
    } finally {
      setIsStartingSession(false);
    }
  };

  const onCreateSession = () => {
    setNewSessionDraft({
      selectedEvalSet: FIXED_EVAL_SET,
      extraEvalStrategy: FIXED_EXTRA_EVAL_STRATEGY,
    });
    setHasNewSessionValidationTriggered(false);
    setIsNewSessionConfigOpen(true);
  };

  const onCancelCreateSession = () => {
    setIsNewSessionConfigOpen(false);
    setHasNewSessionValidationTriggered(false);
  };

  const onConfirmCreateSession = async () => {
    if (isConfirmingNewSession) {
      return;
    }
    if (!isNewSessionDraftValid) {
      setHasNewSessionValidationTriggered(true);
      if (!newSessionDraft.selectedKb) {
        message.warning(t("selfEvolutionRun.message.selectKnowledgeBaseBeforeNewSession"), 1.2);
        return;
      }
      if (!newSessionDraft.selectedEvalSet) {
        message.warning(t("selfEvolutionRun.message.selectExistingEvalSetStrategy"), 1.2);
        return;
      }
      if (!newSessionDraft.extraEvalStrategy) {
        message.warning(t("selfEvolutionRun.message.selectExtraEvalStrategy"), 1.2);
        return;
      }
      if (!newSessionDraft.mode) {
        message.warning(t("selfEvolutionRun.message.selectInterventionMode"), 1.2);
        return;
      }
      message.warning(t("selfEvolutionRun.message.checkFirstFourSteps"), 1.2);
      return;
    }

    const nextMode = newSessionDraft.mode as EvolutionMode;
    const nextKnowledgeBase = newSessionDraft.selectedKb as string;
    const nextEvalSet = newSessionDraft.selectedEvalSet as string;
    const nextExtraEvalStrategy = newSessionDraft.extraEvalStrategy as ExtraEvalStrategy;
    const nextKnowledgeBaseLabel =
      knowledgeBaseOptions.find((item) => item.value === nextKnowledgeBase)?.label || t("selfEvolutionRun.knowledgeBase");
    const nextEvalSetLabel = getExistingEvalSetLabel(nextEvalSet);
    const nextExtraEvalLabel = nextExtraEvalStrategy === "generate"
      ? t("selfEvolutionRun.extraEvalGenerate")
      : t("selfEvolutionRun.extraEvalSkip");
    const nextInterventionLabel = nextMode === "interactive"
      ? t("selfEvolutionRun.interventionManual")
      : t("selfEvolutionRun.interventionAuto");
    const nowLabel = getTimeLabel();
    const nextIndex = chatSessions.length + 1;
    const newSessionId = `session-${Date.now()}`;

    setIsConfirmingNewSession(true);
    try {
      const threadId = await createAndStartThread({
        mode: nextMode,
        selectedKb: nextKnowledgeBase,
        selectedKnowledgeBase: nextKnowledgeBaseLabel,
        selectedEvalSet: nextEvalSet,
      });
      const newSession: ChatSession = {
        id: newSessionId,
        title: t("selfEvolutionRun.newSessionLabel", { index: nextIndex }),
        updatedAt: nowLabel,
        threadId,
        messages: [
          {
            id: `assistant-${Date.now() + 2}`,
            role: "assistant",
            content: `${buildSessionIntroContent(
              nextKnowledgeBaseLabel,
              nextEvalSetLabel,
              nextExtraEvalLabel,
              nextInterventionLabel,
            )}\n\n${t("selfEvolutionRun.threadIdLabel", { id: threadId })}`,
            time: nowLabel,
          },
        ],
      };

      setSelectedKb(nextKnowledgeBase);
      setSelectedEvalSet(nextEvalSet);
      setExtraEvalStrategy(nextExtraEvalStrategy);
      setMode(nextMode);
      setHasLaunchValidationTriggered(false);
      setWorkflowRuntimeState(createWorkflowRuntimeStateForMode(nextMode));
      replaceThreadEvents([]);
      setThreadStepList({ steps: [] });
      resetThreadStepViewSelection();
      processedWorkflowEventKeysRef.current = new Set();
      pendingNextStepRunIdRef.current = undefined;
      setChatSessions((prev) => [...prev, newSession]);
      setActiveSessionId(newSessionId);
      setPrompt("");
      setIsWorkbenchVisible(true);
      setIsNewSessionConfigOpen(false);
      setHasNewSessionValidationTriggered(false);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);
      void subscribeThreadEvents(threadId, INITIAL_THREAD_STEP_ID, newSessionId);
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      message.success(t("selfEvolutionRun.newSessionStartedSuccess"), 1.2);
    } catch (error) {
      showLocalErrorWhenNotHandledByAxios(error, t("selfEvolutionRun.newSessionStartFailed"));
    } finally {
      setIsConfirmingNewSession(false);
    }
  };

  const onCloseSession = (sessionId: string) => {
    if (chatSessions.length <= 1) {
      message.info(t("selfEvolutionRun.keepAtLeastOneSession"), 1);
      return;
    }
    const nextSessions = chatSessions.filter((item) => item.id !== sessionId);
    setChatSessions(nextSessions);
    if (activeSessionId === sessionId) {
      setActiveSessionId(nextSessions[0].id);
    }
  };

  const fetchThreadHistoryList = useCallback(async (options?: { showEmptyMessage?: boolean }) => {
    if (isThreadHistoryListFetchingRef.current) {
      return;
    }

    isThreadHistoryListFetchingRef.current = true;
    setIsLoadingThreadHistoryList(true);
    setThreadHistoryListError("");
    try {
      const response = await axiosInstance.get(`${AGENT_API_BASE}/threads`, {
        params: { page_size: 50 },
      });
      const nextRemoteThreads = normalizeThreadListPayload(response.data);
      setRemoteThreadHistory(nextRemoteThreads);
      if (options?.showEmptyMessage !== false && nextRemoteThreads.length === 0) {
        message.info(t("selfEvolutionRun.noHistorySessions"), 1.2);
      }
    } catch (error) {
      const errorText =
        getLocalizedErrorMessage(error, t("selfEvolutionRun.historyListLoadFailed")) ||
        t("selfEvolutionRun.historyListLoadFailed");
      setThreadHistoryListError(errorText);
      message.error(errorText, 2);
    } finally {
      isThreadHistoryListFetchingRef.current = false;
      setIsLoadingThreadHistoryList(false);
    }
  }, []);

  const onOpenHistorySessionModal = () => {
    setIsHistorySessionModalOpen(true);
    void fetchThreadHistoryList({ showEmptyMessage: true });
  };

  const enterHistorySession = (entry: HistorySessionEntry) => {
    if (entry.threadId) {
      const matchedSession = chatSessions.find((session) => session.threadId === entry.threadId);
      if (matchedSession) {
        if (entry.title && matchedSession.title !== entry.title) {
          setChatSessions((prev) =>
            prev.map((session) =>
              session.id === matchedSession.id
                ? { ...session, title: entry.title || session.title }
                : session,
            ),
          );
        }
        setActiveSessionId(matchedSession.id);
      }
      setIsHistorySessionModalOpen(false);
      if (entry.threadId !== routeThreadId) {
        navigate(`/self-evolution/detail/${encodeURIComponent(entry.threadId)}`);
      }
      return;
    }

    if (entry.sessionId) {
      setActiveSessionId(entry.sessionId);
    }
    setIsHistorySessionModalOpen(false);
  };

  const onSelectHistorySession = (entry: HistorySessionEntry) => {
    const nextSessionKey = entry.threadId || entry.sessionId || entry.key;
    if (!nextSessionKey) {
      return;
    }

    historyPreviewRequestIdRef.current += 1;
    setPreviewHistoryKey(undefined);
    setHistoryPreviewTitle("");
    setHistoryPreviewMessages([]);
    setHistoryPreviewError("");
    setIsLoadingHistoryPreview(false);
    enterHistorySession(entry);
  };

  const resetToEmptySession = () => {
    const nowLabel = getTimeLabel();
    const nextSessionId = `session-${Date.now()}`;
    setChatSessions([
      {
        id: nextSessionId,
        title: t("selfEvolutionRun.currentSession"),
        updatedAt: nowLabel,
        messages: [],
      },
    ]);
    setActiveSessionId(nextSessionId);
    setIsWorkbenchVisible(false);
    setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
    setWorkflowResults(createInitialWorkflowResultsState());
    setCaseArtifact(undefined);
    replaceThreadEvents([]);
    setThreadStepList({ steps: [] });
    resetThreadStepViewSelection();
    processedWorkflowEventKeysRef.current = new Set();
    pendingNextStepRunIdRef.current = undefined;
    setThreadRestoreError("");
    setPrompt("");
    navigate("/self-evolution");
  };

  const deleteHistorySession = async (entry: HistorySessionEntry) => {
    if (deletingHistoryKeys.includes(entry.key)) {
      return;
    }

    setDeletingHistoryKeys((prev) => [...prev, entry.key]);
    try {
      if (entry.threadId) {
        await createCoreAgentApiClient().apiCoreAgentThreadsThreadIdHistoryDelete({
          threadId: entry.threadId,
        });
        setRemoteThreadHistory((prev) => prev.filter((item) => item.threadId !== entry.threadId));
        setChatSessions((prev) => prev.filter((session) => session.threadId !== entry.threadId));

        if (window.localStorage.getItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY) === entry.threadId) {
          window.localStorage.removeItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY);
        }
        if (entry.threadId === activeThreadId || entry.threadId === routeThreadId) {
          threadEventsAbortRef.current?.controller.abort();
          threadEventsAbortRef.current = null;
          resetToEmptySession();
        }
      } else if (entry.sessionId) {
        setChatSessions((prev) => prev.filter((session) => session.id !== entry.sessionId));
        if (entry.sessionId === activeSessionId) {
          resetToEmptySession();
        }
      }

      message.success(t("selfEvolutionRun.message.historyDeleted"), 1.2);
    } catch (error) {
      const fallback = t("selfEvolutionRun.error.deleteHistoryFailed");
      message.error(
        getLocalizedErrorMessage(error, fallback) ||
          fallback,
        2,
      );
    } finally {
      setDeletingHistoryKeys((prev) => prev.filter((key) => key !== entry.key));
    }
  };

  const onDeleteHistorySession = (entry: HistorySessionEntry, event: MouseEvent<HTMLElement>) => {
    event.stopPropagation();
    Modal.confirm({
      title: t("selfEvolutionRun.deleteHistoryTitle"),
      content: entry.threadId
        ? t("selfEvolutionRun.deleteThreadHistoryContent")
        : t("selfEvolutionRun.deleteLocalHistoryContent"),
      okText: t("common.delete"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      centered: true,
      onOk: () => deleteHistorySession(entry),
    });
  };

  const renderKnowledgeBaseButton = (extraClassName = "", isLocked = false) => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      disabled={isLocked}
      menu={{
        items: knowledgeBaseMenuItems,
        selectable: true,
        selectedKeys: selectedKb ? [selectedKb] : [],
        onClick: ({ key }) => {
          if (isLocked) {
            return;
          }
          onKnowledgeBaseMenuClick(String(key), (nextKnowledgeBase) => {
            setSelectedKb(nextKnowledgeBase);
            setHasLaunchValidationTriggered(false);
          });
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}${isLocked ? " is-disabled" : ""}`.trim()}
        disabled={isLocked}
        aria-busy={isKnowledgeBaseLoading}
        aria-label={isLocked ? t("selfEvolutionRun.knowledgeBaseLockedAria", { name: selectedKnowledgeBase }) : t("selfEvolutionRun.selectKnowledgeBaseAria", { name: selectedKnowledgeBase })}
        title={isLocked ? t("selfEvolutionRun.knowledgeBaseLockedTitle") : undefined}
      >
        <DatabaseOutlined />
        <span>{selectedKnowledgeBase}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderModeButton = (extraClassName = "", isLocked = false) => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      disabled={isLocked}
      menu={{
        items: modeMenuItems,
        selectable: true,
        selectedKeys: [mode],
        onClick: ({ key }) => {
          if (isLocked) {
            return;
          }
          setMode(key as EvolutionMode);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}${isLocked ? " is-disabled" : ""}`.trim()}
        disabled={isLocked}
        aria-label={isLocked ? t("selfEvolutionRun.modeLockedAria", { name: modeLabel }) : t("selfEvolutionRun.selectModeAria", { name: modeLabel })}
        title={isLocked ? t("selfEvolutionRun.modeLockedTitle") : undefined}
      >
        <MessageOutlined />
        <span>{modeLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderExistingEvalSetButton = (extraClassName = "") => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: existingEvalSetMenuItems,
        selectable: true,
        selectedKeys: [selectedEvalSet],
        onClick: ({ key }) => {
          const nextEvalSet = String(key);
          setSelectedEvalSet(nextEvalSet);
          if (nextEvalSet === "__none__") {
            setExtraEvalStrategy("generate");
          }
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}`.trim()}
      >
        <FileTextOutlined />
        <span>{selectedEvalSetLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderExtraEvalStrategyButton = (extraClassName = "") => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: extraEvalStrategyMenuItems,
        selectable: true,
        selectedKeys: [extraEvalStrategy],
        onClick: ({ key }) => {
          const nextStrategy = key as ExtraEvalStrategy;
          if (isExtraEvalRequired && nextStrategy === "skip") {
            setExtraEvalStrategy("generate");
            message.warning(t("selfEvolutionRun.message.extraEvalRequired"), 1.2);
            return;
          }
          setExtraEvalStrategy(nextStrategy);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}`.trim()}
      >
        <ExperimentOutlined />
        <span>{extraEvalLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderInterventionButton = (extraClassName = "") => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: modeMenuItems,
        selectable: true,
        selectedKeys: [mode],
        onClick: ({ key }) => {
          setMode(key as EvolutionMode);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}`.trim()}
      >
        <MessageOutlined />
        <span>{interventionLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionKnowledgeBaseButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: knowledgeBaseMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.selectedKb ? [newSessionDraft.selectedKb] : [],
        onClick: ({ key }) => {
          onKnowledgeBaseMenuClick(String(key), (nextKnowledgeBase) => {
            setNewSessionDraft((prev) => ({
              ...prev,
              selectedKb: nextKnowledgeBase,
            }));
            setHasNewSessionValidationTriggered(false);
          });
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.selectedKb ? " is-warning" : ""
        }`}
        aria-busy={isKnowledgeBaseLoading}
        aria-label={t("selfEvolutionRun.selectNewSessionKnowledgeBaseAria", { name: draftKnowledgeBaseLaunchLabel })}
      >
        <DatabaseOutlined />
        <span>{draftKnowledgeBaseLaunchLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionEvalSetButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: existingEvalSetMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.selectedEvalSet ? [newSessionDraft.selectedEvalSet] : [],
        onClick: ({ key }) => {
          const nextEvalSet = String(key);
          setNewSessionDraft((prev) => ({
            ...prev,
            selectedEvalSet: nextEvalSet,
            extraEvalStrategy:
              nextEvalSet === "__none__" && prev.extraEvalStrategy === "skip"
                ? "generate"
                : prev.extraEvalStrategy,
          }));
          setHasNewSessionValidationTriggered(false);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.selectedEvalSet ? " is-warning" : ""
        }`}
      >
        <FileTextOutlined />
        <span>{draftEvalSetLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionExtraEvalStrategyButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: newSessionExtraEvalStrategyMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.extraEvalStrategy ? [newSessionDraft.extraEvalStrategy] : [],
        onClick: ({ key }) => {
          const nextStrategy = key as ExtraEvalStrategy;
          if (isDraftExtraEvalRequired && nextStrategy === "skip") {
            message.warning(t("selfEvolutionRun.message.extraEvalRequired"), 1.2);
            return;
          }
          setNewSessionDraft((prev) => ({
            ...prev,
            extraEvalStrategy: nextStrategy,
          }));
          setHasNewSessionValidationTriggered(false);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.extraEvalStrategy ? " is-warning" : ""
        }`}
      >
        <ExperimentOutlined />
        <span>{draftExtraEvalLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionInterventionButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: modeMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.mode ? [newSessionDraft.mode] : [],
        onClick: ({ key }) => {
          setNewSessionDraft((prev) => ({
            ...prev,
            mode: key as EvolutionMode,
          }));
          setHasNewSessionValidationTriggered(false);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.mode ? " is-warning" : ""
        }`}
      >
        <MessageOutlined />
        <span>{draftInterventionLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const launchOptionCards = [
    {
      key: "knowledge-base",
      step: "1",
      title: t("selfEvolutionRun.stepKnowledgeBase"),
      description: t("selfEvolutionRun.stepKnowledgeBaseDesc"),
      currentValue: knowledgeBaseLaunchLabel,
      toneClassName: "is-blue",
      icon: <DatabaseOutlined />,
      isHighlighted: isKnowledgeBaseRequired && hasLaunchValidationTriggered,
      isDescSingleLine: false,
      control: renderKnowledgeBaseButton("is-launch-control"),
    },
    {
      key: "existing-eval-set",
      step: "2",
      title: t("selfEvolutionRun.stepExistingEvalSet"),
      description: t("selfEvolutionRun.stepExistingEvalSetDesc"),
      currentValue: selectedEvalSetLabel,
      toneClassName: "is-green",
      icon: <FileTextOutlined />,
      isHighlighted: false,
      isDescSingleLine: false,
      control: renderExistingEvalSetButton("is-launch-control"),
    },
    {
      key: "extra-eval-set",
      step: "3",
      title: t("selfEvolutionRun.stepExtraEvalSet"),
      description: t("selfEvolutionRun.stepExtraEvalSetDesc"),
      currentValue: extraEvalLabel,
      toneClassName: "is-amber",
      icon: <ExperimentOutlined />,
      isHighlighted: false,
      isDescSingleLine: true,
      control: renderExtraEvalStrategyButton("is-launch-control"),
    },
    {
      key: "intervention",
      step: "4",
      title: t("selfEvolutionRun.stepIntervention"),
      description: t("selfEvolutionRun.stepInterventionDesc"),
      currentValue: interventionLabel,
      toneClassName: "is-violet",
      icon: <MessageOutlined />,
      isHighlighted: false,
      isDescSingleLine: false,
      control: renderInterventionButton("is-launch-control"),
    },
  ];

  const launchSummaryItems = [
    { label: t("selfEvolutionRun.summaryTarget"), value: knowledgeBaseLaunchLabel },
    { label: t("selfEvolutionRun.summaryExistingEvalSet"), value: selectedEvalSetLabel },
    { label: t("selfEvolutionRun.summaryExtraEvalSet"), value: extraEvalLabel },
    { label: t("selfEvolutionRun.summaryIntervention"), value: interventionLabel },
  ];

  const newSessionOptionCards = [
    {
      key: "new-session-knowledge-base",
      step: "1",
      title: t("selfEvolutionRun.stepKnowledgeBase"),
      description: t("selfEvolutionRun.stepKnowledgeBaseDesc"),
      currentValue: draftKnowledgeBaseLaunchLabel,
      toneClassName: "is-blue",
      icon: <DatabaseOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.selectedKb,
      isDescSingleLine: false,
      control: renderNewSessionKnowledgeBaseButton(),
    },
    {
      key: "new-session-existing-eval-set",
      step: "2",
      title: t("selfEvolutionRun.stepExistingEvalSet"),
      description: t("selfEvolutionRun.stepExistingEvalSetDesc"),
      currentValue: draftEvalSetLabel,
      toneClassName: "is-green",
      icon: <FileTextOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.selectedEvalSet,
      isDescSingleLine: false,
      control: renderNewSessionEvalSetButton(),
    },
    {
      key: "new-session-extra-eval-set",
      step: "3",
      title: t("selfEvolutionRun.stepExtraEvalSet"),
      description: t("selfEvolutionRun.stepExtraEvalSetDesc"),
      currentValue: draftExtraEvalLabel,
      toneClassName: "is-amber",
      icon: <ExperimentOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.extraEvalStrategy,
      isDescSingleLine: false,
      control: renderNewSessionExtraEvalStrategyButton(),
    },
    {
      key: "new-session-intervention",
      step: "4",
      title: t("selfEvolutionRun.stepIntervention"),
      description: t("selfEvolutionRun.stepInterventionDesc"),
      currentValue: draftInterventionLabel,
      toneClassName: "is-violet",
      icon: <MessageOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.mode,
      isDescSingleLine: true,
      control: renderNewSessionInterventionButton(),
    },
  ];

  const newSessionSummaryItems = [
    { label: t("selfEvolutionRun.summaryTarget"), value: draftKnowledgeBaseLaunchLabel },
    { label: t("selfEvolutionRun.summaryExistingEvalSet"), value: draftEvalSetLabel },
    { label: t("selfEvolutionRun.summaryExtraEvalSet"), value: draftExtraEvalLabel },
    { label: t("selfEvolutionRun.summaryIntervention"), value: draftInterventionLabel },
  ];

  const renderKnowledgeAndModeTools = () => (
    <div className="self-evolution-chatlike-tools">
      {renderKnowledgeBaseButton("", isRuntimeConfigLocked)}
      {renderModeButton("", isRuntimeConfigLocked)}
    </div>
  );

  const renderSendButton = () => (
    <button
      type="button"
      onClick={() => void onSend()}
      disabled={isSendDisabled}
      className={`self-evolution-chatlike-send-button${isSendDisabled ? " disabled" : ""}`}
      aria-label={t("selfEvolutionRun.send")}
    >
      <SendIcon />
    </button>
  );

  const renderDatasetPreview = () => {
    const state = workflowResults.datasets;
    if (state.loading || state.error || !state.loaded || isEmptyResultPayload(state.data)) {
      return renderWorkflowResultPayload("datasets");
    }

    const checks = getStructuredRecordField(datasetArtifactData, ["checks"]) || getNestedRecordField(datasetArtifactData, ["checks"]);
    const stats = getStructuredRecordField(datasetArtifactData, ["stats"]) || getNestedRecordField(datasetArtifactData, ["stats"]);
    const typeCounts = getStructuredRecordField(stats, ["question_type_counts"]) || getNestedRecordField(stats, ["question_type_counts"]);
    const caseIds = getStructuredArrayField(datasetArtifactData, ["case_ids"]) || [];
    const errors = getStructuredArrayField(checks, ["errors"]) || [];
    const warnings = getStructuredArrayField(checks, ["warnings"]) || [];
    const totalCases = getNumberField(datasetArtifactData, ["size", "total_nums", "case_count"]) || caseIds.length || datasetCaseRows.length;

    return (
      <section className="self-evolution-dataset-preview" aria-label={t("selfEvolutionRun.datasetResultAria")}>
        <div className="self-evolution-dataset-cases-head">
          <Text>{t("selfEvolutionRun.finalEvalDataset")}</Text>
          <Text>{t("selfEvolutionRun.datasetSampleStats", { total: totalCases, shown: datasetCaseRows.length })}</Text>
        </div>
        <div className="self-evolution-dataset-metrics">
          <span>ready：{checks?.ready === false ? t("selfEvolutionRun.datasetReadyNo") : t("selfEvolutionRun.datasetReadyYes")}</span>
          <span>{t("selfEvolutionRun.datasetTypeCount", { count: typeCounts ? Object.keys(typeCounts).length : 0 })}</span>
          <span>{t("selfEvolutionRun.datasetWarningError", { warnings: warnings.length, errors: errors.length })}</span>
        </div>
        {datasetCaseRows.length === 0 ? (
          renderWorkflowResultPayload("datasets")
        ) : (
          <Table<DatasetCasePreviewRow>
            className="self-evolution-dataset-table"
            size="small"
            rowKey="key"
            columns={datasetCaseColumns}
            dataSource={datasetCaseRows}
            pagination={{ pageSize: 8, size: "small", showSizeChanger: false }}
            scroll={{ x: 1250, y: 360 }}
          />
        )}
      </section>
    );
  };

  const renderWorkflowResultPayload = (kind: WorkflowResultKind) => {
    const resultState = workflowResults[kind];
    const label = getWorkflowResultLabels()[kind];

    if (resultState.loading) {
      return (
        <div className="self-evolution-result-state is-loading">
          <LoadingOutlined spin />
          <span>{t("selfEvolutionRun.resultLoading", { label })}</span>
        </div>
      );
    }

    if (resultState.error) {
      return (
        <div className="self-evolution-result-state is-error" role="alert">
          <span>{resultState.error}</span>
          <button type="button" onClick={() => void fetchWorkflowResult(kind, { force: true })}>
            {t("selfEvolutionRun.resultRetry")}
          </button>
        </div>
      );
    }

    if (!resultState.loaded) {
      return (
        <Paragraph className="self-evolution-px-empty">
          {t("selfEvolutionRun.resultNotLoadedHint", { label })}
        </Paragraph>
      );
    }

    if (isEmptyResultPayload(resultState.data)) {
      return (
        <Paragraph className="self-evolution-px-empty">
          {t("selfEvolutionRun.resultEmptyHint", { label })}
        </Paragraph>
      );
    }

    return (
      <div className="self-evolution-result-json">
        <div className="self-evolution-result-json-head">
          <Text>{t("selfEvolutionRun.resultJsonHead", { label })}</Text>
          <Text>{t("selfEvolutionRun.resultItemCount", { count: getResultItems(resultState.data).length || 1 })}</Text>
        </div>
        <pre>{stringifyResultPayload(resultState.data)}</pre>
      </div>
    );
  };

  const renderPxSingleCategoryPie = (categoryMetric: PxCategoryMetricAverage) => {
    const chartSize = 220;
    const center = chartSize / 2;
    const radius = 74;
    const strokeWidth = 34;
    const circumference = 2 * Math.PI * radius;
    const metricValues = getPxMetricMeta().map((metric) => ({
      ...metric,
      value: clampScore(categoryMetric.metrics[metric.key]),
    }));
    const valueSum = metricValues.reduce((acc, item) => acc + item.value, 0);
    const normalized = metricValues.map((item) => ({
      ...item,
      ratio: valueSum > 0 ? item.value / valueSum : 1 / metricValues.length,
    }));
    let cumulativeOffset = 0;

    return (
      <div className="self-evolution-px-chart-wrap" aria-label={t("selfEvolutionRun.singleCategoryPieAria")}>
        <svg className="self-evolution-px-pie-chart" viewBox={`0 0 ${chartSize} ${chartSize}`} role="img">
          <title>{t("selfEvolutionRun.pieChartTitle", { category: categoryMetric.category })}</title>
          <circle
            cx={center}
            cy={center}
            r={radius}
            fill="none"
            stroke="#ecf2fb"
            strokeWidth={strokeWidth}
          />
          <g transform={`rotate(-90 ${center} ${center})`}>
            {normalized.map((item) => {
              const dashLength = item.ratio * circumference;
              const currentOffset = cumulativeOffset;
              cumulativeOffset += dashLength;
              return (
                <circle
                  key={item.key}
                  cx={center}
                  cy={center}
                  r={radius}
                  fill="none"
                  stroke={item.color}
                  strokeWidth={strokeWidth}
                  strokeDasharray={`${dashLength} ${circumference - dashLength}`}
                  strokeDashoffset={-currentOffset}
                />
              );
            })}
          </g>
          <text x={center} y={center - 4} textAnchor="middle" className="self-evolution-px-pie-center-title">
            {categoryMetric.category}
          </text>
          <text x={center} y={center + 20} textAnchor="middle" className="self-evolution-px-pie-center-value">
            {t("selfEvolutionRun.resultItemCount", { count: categoryMetric.caseCount })}
          </text>
        </svg>
      </div>
    );
  };

  const renderPxMultiCategoryBars = (categoryMetrics: PxCategoryMetricAverage[]) => {
    const width = 960;
    const height = 300;
    const padding = { top: 22, right: 32, bottom: 66, left: 54 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const categoryCount = categoryMetrics.length;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const groupWidth = chartWidth / Math.max(categoryCount, 1);
    const metricCount = getPxMetricMeta().length;
    const barGap = 4;
    const groupInnerWidth = Math.min(96, groupWidth * 0.74);
    const barWidth = Math.max(5, Math.min(18, (groupInnerWidth - barGap * (metricCount - 1)) / metricCount));
    const groupBarsWidth = barWidth * metricCount + barGap * (metricCount - 1);
    const xToCenter = (index: number) => padding.left + groupWidth * index + groupWidth / 2;
    const axisTicks = [0, 0.25, 0.5, 0.75, 1];

    return (
      <div className="self-evolution-px-chart-wrap" aria-label={t("selfEvolutionRun.multiCategoryBarAria")}>
        <svg className="self-evolution-px-bar-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>{t("selfEvolutionRun.barChartTitle")}</title>
          {axisTicks.map((tick) => {
            const y = yToPx(tick);
            return (
              <g key={tick}>
                <line
                  x1={padding.left}
                  y1={y}
                  x2={width - padding.right}
                  y2={y}
                  className="self-evolution-px-grid-line"
                />
                <text x={padding.left - 8} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                  {tick.toFixed(2)}
                </text>
              </g>
            );
          })}

          <line
            x1={padding.left}
            y1={padding.top + chartHeight}
            x2={width - padding.right}
            y2={padding.top + chartHeight}
            className="self-evolution-px-axis-line"
          />

          {categoryMetrics.map((item, categoryIndex) => {
            const groupStartX = xToCenter(categoryIndex) - groupBarsWidth / 2;
            return (
              <g key={`px-bar-group-${item.category}`}>
                {getPxMetricMeta().map((metric, metricIndex) => {
                  const value = clampScore(item.metrics[metric.key]);
                  const y = yToPx(value);
                  return (
                    <rect
                      key={`${item.category}-${metric.key}`}
                      x={groupStartX + metricIndex * (barWidth + barGap)}
                      y={y}
                      width={barWidth}
                      height={padding.top + chartHeight - y}
                      rx={3}
                      fill={metric.color}
                      className="self-evolution-px-bar"
                    >
                      <title>{`${metric.label} ${item.category}: ${formatPercent(value)}`}</title>
                    </rect>
                  );
                })}
              </g>
            );
          })}

          {categoryMetrics.map((item, index) => {
            const x = xToCenter(index);
            return (
              <text
                key={item.category}
                x={x}
                y={height - 28}
                textAnchor="middle"
                className="self-evolution-px-axis-label"
              >
                {getShortLabel(item.category, 18)}
              </text>
            );
          })}
        </svg>
      </div>
    );
  };

  const renderPxReportPreview = () => (
    <section className="self-evolution-px-report" aria-label={t("selfEvolutionRun.pxReportAria")}>
      {workflowResults["eval-reports"].loading ? (
        renderWorkflowResultPayload("eval-reports")
      ) : workflowResults["eval-reports"].error ? (
        renderWorkflowResultPayload("eval-reports")
      ) : evalTraceObservation && pxReportCategoryMetrics.length === 0 ? (
        <TraceObservationView observation={evalTraceObservation} title={t("selfEvolutionRun.agenticRagObservationTitle")} />
      ) : (
        <>
      <div className="self-evolution-px-report-head">
        <Text>{t("selfEvolutionRun.pxReportAggDesc")}</Text>
        <div className="self-evolution-px-report-actions">
          <Text>{t("selfEvolutionRun.pxReportStats", { cases: pxReportTotalCases, categories: pxReportCategoryMetrics.length })}</Text>
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              openObservationPage("eval");
            }}
          >
            {t("selfEvolutionRun.enterObservation")}
          </button>
        </div>
      </div>

      {pxReportCategoryMetrics.length === 0 ? (
        <Paragraph className="self-evolution-px-empty">{t("selfEvolutionRun.noMetricData")}</Paragraph>
      ) : isSinglePxCategory ? (
        <div className="self-evolution-px-panel">
          {renderPxSingleCategoryPie(pxReportCategoryMetrics[0])}
          <div className="self-evolution-px-legend">
            {getPxMetricMeta().map((metric) => (
              <div key={metric.key} className="self-evolution-px-legend-item">
                <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                <span className="self-evolution-px-legend-label">{metric.label}</span>
                <span className="self-evolution-px-legend-value">
                  {formatPercent(pxReportCategoryMetrics[0].metrics[metric.key])}
                </span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="self-evolution-px-panel is-bar">
          <div className="self-evolution-px-legend is-compact">
            {getPxMetricMeta().map((metric) => (
              <div key={metric.key} className="self-evolution-px-legend-item">
                <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                <span className="self-evolution-px-legend-label">{metric.label}</span>
              </div>
            ))}
          </div>
          {renderPxMultiCategoryBars(pxReportCategoryMetrics)}
        </div>
      )}
      <div className="self-evolution-px-case-section">
        <div className="self-evolution-px-case-section-head">
          <Text>{t("selfEvolutionRun.dataListTitle")}</Text>
          <Text>{t("selfEvolutionRun.resultItemCount", { count: pxCaseDetailCount })}</Text>
        </div>
        {evalReportBadCases.loading || isPxCaseDetailPending ? (
          <div className="self-evolution-result-state is-loading">
            <LoadingOutlined spin />
            <span>{t("selfEvolutionRun.loadingDataList")}</span>
          </div>
        ) : evalReportBadCases.error ? (
          <div className="self-evolution-result-state is-error" role="alert">
            <span>{evalReportBadCases.error}</span>
            <button
              type="button"
              disabled={!evalReportId}
              onClick={() => void fetchEvalReportBadCases(workflowResults["eval-reports"].data, {
                force: true,
                page: pxCaseDetailPage,
              })}
            >
              {t("selfEvolutionRun.resultRetry")}
            </button>
          </div>
        ) : pxCaseDetailRows.length === 0 ? (
          <Paragraph className="self-evolution-px-empty">{t("selfEvolutionRun.noDataList")}</Paragraph>
        ) : (
          <Table<PxCaseDetailRow>
            className="self-evolution-px-case-table"
            size="small"
            rowKey="key"
            columns={pxCaseDetailColumns}
            dataSource={pxCaseDetailRows}
            pagination={{
              current: pxCaseDetailPage,
              pageSize: pxCaseDetailPageSize,
              total: pxCaseDetailCount,
              showSizeChanger: false,
              showQuickJumper: false,
              onChange: (page) => {
                void fetchEvalReportBadCases(workflowResults["eval-reports"].data, {
                  force: true,
                  page,
                });
              },
            }}
            scroll={{ x: 1582, y: 280 }}
          />
        )}
      </div>
        </>
      )}
    </section>
  );

  const renderAnalysisReportPreview = () => (
    <section className="self-evolution-analysis-report" aria-label={t("selfEvolutionRun.analysisReportAria")}>
      <div className="self-evolution-analysis-head">
        <Text>{t("selfEvolutionRun.fullAnalysisReportTitle")}</Text>
      </div>
      <div className="self-evolution-analysis-body">
        {hasAnalysisStructuredReport ? (
          <>
            {analysisCategoryRows.length > 0 && (
              <div className="self-evolution-analysis-category-section">
                <div className="self-evolution-analysis-section-head">
                  <Text strong>{t("selfEvolutionRun.coarseCategoryDist")}</Text>
                  <Text>{t("selfEvolutionRun.categoryCountLabel", { count: analysisCategoryRows.length })}</Text>
                </div>
                <div className="self-evolution-analysis-category-panel">
                  <div className="self-evolution-px-legend is-compact">
                    {analysisCategoryRows.map((item) => (
                      <div
                        key={`analysis-category-legend-${item.key}`}
                        className={`self-evolution-px-legend-item${highlightedAnalysisCategory === item.key ? " is-active" : ""}`}
                        onMouseEnter={() => setHighlightedAnalysisCategory(item.key)}
                        onMouseLeave={() => setHighlightedAnalysisCategory(null)}
                        onFocus={() => setHighlightedAnalysisCategory(item.key)}
                        onBlur={() => setHighlightedAnalysisCategory(null)}
                        role="button"
                        tabIndex={0}
                      >
                        <span className="self-evolution-px-legend-dot" style={{ backgroundColor: item.color }} />
                        <span className="self-evolution-px-legend-label">{item.category}</span>
                        <span className="self-evolution-px-legend-value">{item.ratio}</span>
                      </div>
                    ))}
                  </div>
                  <div className="self-evolution-analysis-category-chart-wrap">
                    <AnalysisCategoryPieChart
                      rows={analysisCategoryRows}
                      highlightedCategory={highlightedAnalysisCategory}
                      onCategoryHover={setHighlightedAnalysisCategory}
                      className="self-evolution-analysis-category-echart"
                    />
                  </div>
                </div>
              </div>
            )}
            <div className="self-evolution-analysis-case-section">
              <div className="self-evolution-analysis-section-head">
                <Text strong>{t("selfEvolutionRun.caseDataTitle")}</Text>
                <Text>{t("selfEvolutionRun.resultItemCount", { count: analysisCaseRows.length })}</Text>
              </div>
              {analysisCaseRows.length > 0 ? (
                <Table<AnalysisCasePreviewRow>
                  className="self-evolution-dataset-table self-evolution-analysis-table"
                  size="small"
                  rowKey="key"
                  columns={analysisCaseColumns}
                  dataSource={analysisCaseRows}
                  pagination={{ pageSize: 8, size: "small", showSizeChanger: false }}
                  scroll={{ x: 760, y: 330 }}
                />
              ) : (
                <Paragraph className="self-evolution-px-empty">{t("selfEvolutionRun.noCaseData")}</Paragraph>
              )}
            </div>
          </>
        ) : workflowResults["analysis-reports"].loaded ||
        workflowResults["analysis-reports"].loading ||
        workflowResults["analysis-reports"].error ? (
          fetchedAnalysisReportMarkdown ? (
            <div className="self-evolution-analysis-markdown">
              <MarkdownViewer>{fetchedAnalysisReportMarkdown}</MarkdownViewer>
            </div>
          ) : (
            renderWorkflowResultPayload("analysis-reports")
          )
        ) : (
          renderWorkflowResultPayload("analysis-reports")
        )}
      </div>
    </section>
  );

  const renderCodeOptimizeDiffPreview = () => {
    if (!directFetchedDiffText && diffArtifactContent.loading && !diffArtifactContent.content) {
      return (
        <section className="self-evolution-optimize-report" aria-label={t("selfEvolutionRun.codeOptimizeDiffAria")}>
          <div className="self-evolution-optimize-head">
            <Text>{t("selfEvolutionRun.codeChangesTitle")}</Text>
            <Text>{t("selfEvolutionRun.loadingFileContent")}</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">{t("selfEvolutionRun.loadingFileContentHint")}</Paragraph>
        </section>
      );
    }

    if (!directFetchedDiffText && diffArtifactContent.error && !diffArtifactContent.content) {
      return (
        <section className="self-evolution-optimize-report" aria-label={t("selfEvolutionRun.codeOptimizeDiffAria")}>
          <div className="self-evolution-optimize-head">
            <Text>{t("selfEvolutionRun.codeChangesTitle")}</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">{diffArtifactContent.error}</Paragraph>
        </section>
      );
    }

    if (
      (workflowResults.diffs.loaded || workflowResults.diffs.loading || workflowResults.diffs.error) &&
      !fetchedDiffText
    ) {
      return (
        <section className="self-evolution-optimize-report" aria-label={t("selfEvolutionRun.codeOptimizeDiffAria")}>
          <div className="self-evolution-optimize-head">
            <Text>{t("selfEvolutionRun.codeChangesTitle")}</Text>
          </div>
          {renderWorkflowResultPayload("diffs")}
        </section>
      );
    }

    const renderTreeNodes = (nodes: DiffFileTreeNode[], depth = 0): ReactNode[] =>
      nodes.map((node) => {
        if (node.nodeType === "dir") {
          const isCollapsed = !!collapsedDiffDirs[node.path];
          return (
            <div key={`dir-${node.path}`}>
              <button
                type="button"
                className={`self-evolution-diff-tree-node is-dir${isCollapsed ? " is-collapsed" : ""}`}
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
                onClick={() =>
                  setCollapsedDiffDirs((prev) => ({
                    ...prev,
                    [node.path]: !prev[node.path],
                  }))
                }
              >
                <span className="self-evolution-diff-tree-icon">{isCollapsed ? "▸" : "▾"}</span>
                <span className="self-evolution-diff-tree-text">{node.name}</span>
              </button>
              {!isCollapsed && renderTreeNodes(node.children, depth + 1)}
            </div>
          );
        }

        const isActive = node.fileId === activeDiffFile?.id;
        return (
          <button
            key={`file-${node.path}-${node.fileId}`}
            type="button"
            className={`self-evolution-diff-tree-node is-file${isActive ? " is-active" : ""}`}
            style={{ paddingLeft: `${depth * 14 + 8}px` }}
            onClick={() => node.fileId && setActiveDiffFileId(node.fileId)}
          >
            <span className="self-evolution-diff-tree-icon">•</span>
            <span className="self-evolution-diff-tree-text">{node.name}</span>
          </button>
        );
      });

    if (!activeDiffFile) {
      return (
        <section className="self-evolution-optimize-report" aria-label={t("selfEvolutionRun.codeOptimizeDiffAria")}>
          <div className="self-evolution-optimize-head">
            <Text>{t("selfEvolutionRun.codeChangesTitle")}</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">{t("selfEvolutionRun.noChangedFiles")}</Paragraph>
        </section>
      );
    }

    const allLineCount = parsedDiffFiles.reduce((total, file) => total + file.lines.length, 0);
    return (
      <section className="self-evolution-optimize-report" aria-label={t("selfEvolutionRun.codeOptimizeDiffAria")}>
        <div className="self-evolution-optimize-head">
          <Text>{t("selfEvolutionRun.codeChangesTitle")}</Text>
          <Text>{t("selfEvolutionRun.fileStats", { files: parsedDiffFiles.length, lines: allLineCount })}</Text>
        </div>
        <div className="self-evolution-optimize-layout">
          <aside className="self-evolution-optimize-tree" aria-label={t("selfEvolutionRun.changedFilesTreeAria")}>
            <div className="self-evolution-optimize-tree-head">{t("selfEvolutionRun.fileStructureTitle")}</div>
            <div className="self-evolution-optimize-tree-body">{renderTreeNodes(diffFileTree)}</div>
          </aside>
          <div className="self-evolution-optimize-viewer" aria-label={t("selfEvolutionRun.changedCodeAria")}>
            <div className="self-evolution-optimize-file-head">
              <Text className="self-evolution-optimize-file-path">{activeDiffFile.displayPath}</Text>
              <Text className="self-evolution-optimize-file-stat">
                {`+${activeDiffFile.additions} / -${activeDiffFile.deletions}`}
              </Text>
            </div>
            <div className="self-evolution-optimize-body">
              <pre className="self-evolution-optimize-diff">
                {activeDiffFile.lines.map((line, index) => {
                  const lineType = getDiffLineType(line);
                  return (
                    <div key={`diff-line-${activeDiffFile.id}-${index}`} className={`self-evolution-diff-line is-${lineType}`}>
                      <span className="self-evolution-diff-line-no">{index + 1}</span>
                      <span className="self-evolution-diff-line-code">{line || " "}</span>
                    </div>
                  );
                })}
              </pre>
            </div>
          </div>
        </div>
      </section>
    );
  };

  const renderAbSingleCategoryBars = (comparison: AbCategoryComparison) => {
    const width = 700;
    const height = 300;
    const padding = { top: 24, right: 24, bottom: 58, left: 44 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const ticks = [0, 0.25, 0.5, 0.75, 1];
    const groupWidth = chartWidth / getPxMetricMeta().length;
    const barWidth = Math.min(24, groupWidth * 0.28);
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";

    return (
      <div className="self-evolution-ab-chart-wrap">
        <svg className="self-evolution-ab-single-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>{t("selfEvolutionRun.abChartTitle", { category: comparison.category })}</title>
          {ticks.map((tick) => {
            const y = yToPx(tick);
            return (
              <g key={`ab-single-tick-${tick}`}>
                <line
                  x1={padding.left}
                  y1={y}
                  x2={width - padding.right}
                  y2={y}
                  className="self-evolution-px-grid-line"
                />
                <text x={padding.left - 8} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                  {tick.toFixed(2)}
                </text>
              </g>
            );
          })}

          {getPxMetricMeta().map((metric, index) => {
            const groupCenter = padding.left + groupWidth * index + groupWidth / 2;
            const baselineValue = comparison.baseline[metric.key];
            const experimentValue = comparison.experiment[metric.key];
            const baselineY = yToPx(baselineValue);
            const experimentY = yToPx(experimentValue);
            const delta = comparison.delta[metric.key];
            return (
              <g key={`ab-single-group-${metric.key}`}>
                <rect
                  x={groupCenter - barWidth - 4}
                  y={baselineY}
                  width={barWidth}
                  height={padding.top + chartHeight - baselineY}
                  fill={aColor}
                  rx={3}
                />
                <rect
                  x={groupCenter + 4}
                  y={experimentY}
                  width={barWidth}
                  height={padding.top + chartHeight - experimentY}
                  fill={bColor}
                  rx={3}
                />
                <text
                  x={groupCenter}
                  y={Math.min(baselineY, experimentY) - 8}
                  textAnchor="middle"
                  className={`self-evolution-ab-delta-text${delta >= 0 ? " is-up" : " is-down"}`}
                >
                  {`${delta >= 0 ? "+" : ""}${(delta * 100).toFixed(1)}%`}
                </text>
                <text
                  x={groupCenter}
                  y={height - 16}
                  textAnchor="middle"
                  className="self-evolution-px-axis-label"
                >
                  {metric.label}
                </text>
              </g>
            );
          })}
        </svg>

        <div className="self-evolution-ab-legend">
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-a" />
            {t("selfEvolutionRun.abLegendA")}
          </span>
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-b" />
            {t("selfEvolutionRun.abLegendB")}
          </span>
        </div>
      </div>
    );
  };

  const renderAbFacetCharts = (comparisons: AbCategoryComparison[]) => {
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";
    return (
      <div className="self-evolution-ab-facet-grid">
        {getPxMetricMeta().map((metric) => {
          const width = Math.max(320, comparisons.length * 96);
          const height = 220;
          const padding = { top: 20, right: 16, bottom: 54, left: 36 };
          const chartWidth = width - padding.left - padding.right;
          const chartHeight = height - padding.top - padding.bottom;
          const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
          const ticks = [0, 0.5, 1];
          const groupWidth = chartWidth / Math.max(comparisons.length, 1);
          const barWidth = Math.min(14, groupWidth * 0.24);

          return (
            <div key={`ab-facet-${metric.key}`} className="self-evolution-ab-facet-card">
              <div className="self-evolution-ab-facet-title">{metric.label}</div>
              <div className="self-evolution-ab-facet-scroller">
                <svg className="self-evolution-ab-facet-chart" viewBox={`0 0 ${width} ${height}`} role="img">
                  <title>{t("selfEvolutionRun.abFacetChartTitle", { metric: metric.label })}</title>
                  {ticks.map((tick) => {
                    const y = yToPx(tick);
                    return (
                      <g key={`ab-facet-${metric.key}-${tick}`}>
                        <line
                          x1={padding.left}
                          y1={y}
                          x2={width - padding.right}
                          y2={y}
                          className="self-evolution-px-grid-line"
                        />
                        <text x={padding.left - 6} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                          {tick.toFixed(1)}
                        </text>
                      </g>
                    );
                  })}
                  {comparisons.map((comparison, index) => {
                    const groupCenter = padding.left + groupWidth * index + groupWidth / 2;
                    const aValue = comparison.baseline[metric.key];
                    const bValue = comparison.experiment[metric.key];
                    const aY = yToPx(aValue);
                    const bY = yToPx(bValue);
                    return (
                      <g key={`ab-facet-group-${metric.key}-${comparison.category}`}>
                        <rect
                          x={groupCenter - barWidth - 3}
                          y={aY}
                          width={barWidth}
                          height={padding.top + chartHeight - aY}
                          fill={aColor}
                          rx={2}
                        />
                        <rect
                          x={groupCenter + 3}
                          y={bY}
                          width={barWidth}
                          height={padding.top + chartHeight - bY}
                          fill={bColor}
                          rx={2}
                        />
                        <text
                          x={groupCenter}
                          y={height - 16}
                          textAnchor="middle"
                          className="self-evolution-px-axis-label"
                        >
                          {getShortLabel(comparison.category, 4)}
                        </text>
                      </g>
                    );
                  })}
                </svg>
              </div>
            </div>
          );
        })}

        <div className="self-evolution-ab-legend is-facet">
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-a" />
            {t("selfEvolutionRun.abLegendA")}
          </span>
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-b" />
            {t("selfEvolutionRun.abLegendB")}
          </span>
        </div>
      </div>
    );
  };

  const renderAbSummaryMetricChart = (rows: AbSummaryMetricRow[]) => {
    const width = Math.max(620, rows.length * 132);
    const height = 300;
    const padding = { top: 28, right: 24, bottom: 62, left: 44 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const ticks = [0, 0.25, 0.5, 0.75, 1];
    const groupWidth = chartWidth / Math.max(rows.length, 1);
    const barWidth = Math.min(24, groupWidth * 0.26);
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";

    return (
      <div className="self-evolution-ab-summary-chart-scroller">
        <svg className="self-evolution-ab-summary-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>{t("selfEvolutionRun.abTestReportTitle")}</title>
          {ticks.map((tick) => {
            const y = yToPx(tick);
            return (
              <g key={`ab-summary-tick-${tick}`}>
                <line
                  x1={padding.left}
                  y1={y}
                  x2={width - padding.right}
                  y2={y}
                  className="self-evolution-px-grid-line"
                />
                <text x={padding.left - 8} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                  {tick.toFixed(2)}
                </text>
              </g>
            );
          })}
          {rows.map((row, index) => {
            const groupCenter = padding.left + groupWidth * index + groupWidth / 2;
            const meanAY = yToPx(row.meanA);
            const meanBY = yToPx(row.meanB);
            return (
              <g key={`ab-summary-group-${row.key}`}>
                <rect
                  x={groupCenter - barWidth - 4}
                  y={meanAY}
                  width={barWidth}
                  height={padding.top + chartHeight - meanAY}
                  fill={aColor}
                  rx={3}
                />
                <rect
                  x={groupCenter + 4}
                  y={meanBY}
                  width={barWidth}
                  height={padding.top + chartHeight - meanBY}
                  fill={bColor}
                  rx={3}
                />
                <text
                  x={groupCenter}
                  y={Math.min(meanAY, meanBY) - 8}
                  textAnchor="middle"
                  className={`self-evolution-ab-delta-text${row.deltaMean >= 0 ? " is-up" : " is-down"}`}
                >
                  {`${row.deltaMean >= 0 ? "+" : ""}${(row.deltaMean * 100).toFixed(1)}%`}
                </text>
                <text x={groupCenter} y={height - 28} textAnchor="middle" className="self-evolution-px-axis-label">
                  {getShortLabel(row.metricLabel, 7)}
                </text>
                <text x={groupCenter} y={height - 12} textAnchor="middle" className="self-evolution-px-axis-label">
                  {t("selfEvolutionRun.winRateLabel", { rate: formatPercent(row.winRateB) })}
                </text>
              </g>
            );
          })}
        </svg>
      </div>
    );
  };

  const renderAbSummaryReport = (report: AbSummaryReport) => {
    const metricColumns: ColumnsType<AbSummaryMetricRow> = [
      { title: t("selfEvolutionRun.colMetric"), dataIndex: "metricLabel", key: "metricLabel", width: 150 },
      { title: "mean A", dataIndex: "meanA", key: "meanA", width: 110, render: (value: number) => formatPercent(value) },
      { title: "mean B", dataIndex: "meanB", key: "meanB", width: 110, render: (value: number) => formatPercent(value) },
      {
        title: "Δmean",
        dataIndex: "deltaMean",
        key: "deltaMean",
        width: 110,
        render: (value: number) => <span className={value >= 0 ? "is-up" : "is-down"}>{formatMetricDelta(value)}</span>,
      },
      { title: t("selfEvolutionRun.colBWinRate"), dataIndex: "winRateB", key: "winRateB", width: 110, render: (value: number) => formatPercent(value) },
      { title: "sign p", dataIndex: "signP", key: "signP", width: 100, render: (value: number | null | undefined) => formatMaybePValue(value) },
    ];
    const topDiffColumns: ColumnsType<AbTopDiffRow> = [
      {
        title: "case",
        dataIndex: "caseKey",
        key: "caseKey",
        width: 280,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
      { title: "A", dataIndex: "a", key: "a", width: 90 },
      { title: "B", dataIndex: "b", key: "b", width: 90 },
      {
        title: "Δ",
        dataIndex: "delta",
        key: "delta",
        width: 90,
        render: (value: number) => <span className={value >= 0 ? "is-up" : "is-down"}>{value}</span>,
      },
    ];

    return (
      <div key={report.id} className="self-evolution-ab-summary-report">
        <div className="self-evolution-ab-summary-head">
          <div>
            <Text strong>{report.id}</Text>
            <div className="self-evolution-ab-summary-meta">
              {report.alignedCases !== undefined && <span>{t("selfEvolutionRun.abSummaryAlignedCases", { count: report.alignedCases })}</span>}
              {report.primaryMetric && <span>{t("selfEvolutionRun.abSummaryPrimaryMetric", { metric: formatAbMetricLabel(report.primaryMetric) })}</span>}
              {report.guardMetrics.length > 0 && (
                <span>{t("selfEvolutionRun.abSummaryGuardMetrics", { metrics: report.guardMetrics.map(formatAbMetricLabel).join(" / ") })}</span>
              )}
            </div>
          </div>
          {report.verdict && <Tag color={["pass", "accept"].includes(report.verdict) ? "success" : "warning"}>{report.verdict}</Tag>}
        </div>

        {report.metricRows.length > 0 && (
          <div className="self-evolution-ab-chart-shell">
            {renderAbSummaryMetricChart(report.metricRows)}
            <div className="self-evolution-ab-legend">
              <span className="self-evolution-ab-legend-item">
                <span className="self-evolution-ab-legend-dot is-a" />
                {t("selfEvolutionRun.abLegendA")}
              </span>
              <span className="self-evolution-ab-legend-item">
                <span className="self-evolution-ab-legend-dot is-b" />
                {t("selfEvolutionRun.abLegendB")}
              </span>
            </div>
          </div>
        )}

        {report.metricRows.length > 0 && (
          <Table<AbSummaryMetricRow>
            className="self-evolution-dataset-table self-evolution-ab-table self-evolution-ab-summary-table"
            size="small"
            rowKey="key"
            columns={metricColumns}
            dataSource={report.metricRows}
            pagination={false}
            scroll={{ x: 690 }}
          />
        )}

        {report.markdown && (
          <div className="self-evolution-ab-markdown">
            <div className="self-evolution-ab-section-title">{t("selfEvolutionRun.markdownReport")}</div>
            <div className="self-evolution-ab-markdown-body">
              <MarkdownViewer>{report.markdown}</MarkdownViewer>
            </div>
          </div>
        )}

        {report.topDiffRows.length > 0 && (
          <div className="self-evolution-ab-top-diff">
            <div className="self-evolution-ab-section-title">Top diff cases</div>
            <Table<AbTopDiffRow>
              className="self-evolution-dataset-table self-evolution-ab-table"
              size="small"
              rowKey="key"
              columns={topDiffColumns}
              dataSource={report.topDiffRows}
              pagination={false}
              scroll={{ x: 550 }}
            />
          </div>
        )}

        {(report.reasons.length > 0 || report.missingMetrics.length > 0) && (
          <div className="self-evolution-ab-reasons">
            {report.reasons.map((reason) => (
              <span key={`reason-${report.id}-${reason}`}>{reason}</span>
            ))}
            {report.missingMetrics.length > 0 && <span>{t("selfEvolutionRun.missingMetrics", { metrics: report.missingMetrics.join(" / ") })}</span>}
          </div>
        )}
      </div>
    );
  };

  const renderAbTestPreview = () => {
    if (!workflowResults.abtests.loading && !workflowResults.abtests.error && !abSummaryReports.length && isEmptyResultPayload(workflowResults.abtests.data) && !abCategoryComparisons.length) return null;
    return (
      <section className="self-evolution-ab-report" aria-label={t("selfEvolutionRun.abReportAria")}>
        {workflowResults.abtests.loading || workflowResults.abtests.error ? (
          renderWorkflowResultPayload("abtests")
        ) : workflowResults.abtests.loaded && abTraceObservation && abSummaryReports.length === 0 ? (
          <TraceObservationView observation={abTraceObservation} title={t("selfEvolutionRun.abTraceObservationTitle")} />
        ) : workflowResults.abtests.loaded && abSummaryReports.length > 0 ? (
          <>
            <div className="self-evolution-ab-head">
              <Text>{t("selfEvolutionRun.abTestReportTitle")}</Text>
              <Text>{t("selfEvolutionRun.abCurrentShown", { count: abSummaryReports.length })}</Text>
            </div>
            <div className="self-evolution-ab-summary-list">{abSummaryReports.map(renderAbSummaryReport)}</div>
          </>
        ) : workflowResults.abtests.loaded && !isEmptyResultPayload(workflowResults.abtests.data) ? (
          renderWorkflowResultPayload("abtests")
        ) : (
          <>
            <div className="self-evolution-ab-head">
              <Text>{t("selfEvolutionRun.abComparisonDetailTitle")}</Text>
              <Text>{t("selfEvolutionRun.abComparisonCurrentShown", { shown: abComparisonRows.length, total: abCategoryComparisons.length })}</Text>
            </div>
            <Table<AbComparisonRow>
              className="self-evolution-dataset-table self-evolution-ab-table"
              size="small"
              rowKey="key"
              columns={abComparisonColumns}
              dataSource={abComparisonRows}
              pagination={false}
              scroll={{ x: 1100, y: 320 }}
            />
            <div className="self-evolution-ab-chart-shell">
              {isSingleAbCategory ? renderAbSingleCategoryBars(abCategoryComparisons[0]) : renderAbFacetCharts(abCategoryComparisons)}
            </div>
          </>
        )}
      </section>
    );
  };

  const artifactItems: ArtifactPanelItem[] = [
    {
      kind: "datasets",
      stepId: "dataset",
      sectionTitle: t("selfEvolutionRun.artifact1Title"),
      sectionDesc: t("selfEvolutionRun.artifact1Desc"),
      title: t("selfEvolutionRun.artifact1Name"),
      desc: t("selfEvolutionRun.artifact1Detail"),
      fallbackUrl: datasetDownloadUrl,
      fileName: datasetDownloadFileName,
      preview: renderDatasetPreview(),
    },
    {
      kind: "eval-reports",
      stepId: "px-report",
      sectionTitle: t("selfEvolutionRun.artifact2Title"),
      sectionDesc: t("selfEvolutionRun.artifact2Desc"),
      title: t("selfEvolutionRun.artifact2Name"),
      desc: t("selfEvolutionRun.artifact2Detail"),
      fallbackUrl: evalReportDownloadUrl,
      fileName: "eval-report.json",
      preview: renderPxReportPreview(),
    },
    {
      kind: "analysis-reports",
      stepId: "analysis",
      sectionTitle: t("selfEvolutionRun.artifact3Title"),
      sectionDesc: t("selfEvolutionRun.artifact3Desc"),
      title: t("selfEvolutionRun.artifact3Name"),
      desc: t("selfEvolutionRun.artifact3Detail"),
      fallbackUrl: "",
      fileName: "analysis-report.md",
      preview: renderAnalysisReportPreview(),
    },
    {
      kind: "diffs",
      stepId: "code-optimize",
      sectionTitle: t("selfEvolutionRun.artifact4Title"),
      sectionDesc: t("selfEvolutionRun.artifact4Desc"),
      title: t("selfEvolutionRun.artifact4Name"),
      desc: t("selfEvolutionRun.artifact4Detail"),
      fallbackUrl: diffResultDownloadUrl,
      fileName: "code-diff.diff",
      preview: renderCodeOptimizeDiffPreview(),
    },
    {
      kind: "abtests",
      stepId: "ab-test",
      sectionTitle: t("selfEvolutionRun.artifact5Title"),
      sectionDesc: t("selfEvolutionRun.artifact5Desc"),
      title: t("selfEvolutionRun.artifact5Name"),
      desc: t("selfEvolutionRun.artifact5Detail"),
      fallbackUrl: abtestResultDownloadUrl || abComparisonDownloadUrl,
      fileName: "ab-test-comparison.json",
      preview: renderAbTestPreview(),
    },
  ];

  const activeArtifactItem = artifactItems.find((item) => item.kind === activeArtifactKind);
  const visibleArtifactItems = artifactItems.filter((item) =>
    workflowSteps.some((step) => step.id === item.stepId),
  );
  const isOpaqueStepTitle = (title: string | undefined, stepId: string) => {
    const normalizedTitle = title?.trim();
    if (!normalizedTitle || normalizedTitle === stepId) {
      return true;
    }
    return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(normalizedTitle);
  };
  const threadStepNavigationItems = threadStepList.steps.map((step, index) => ({
    step,
    index,
    item: artifactItems[index],
  }));
  const getArtifactStep = (item: ArtifactPanelItem) =>
    workflowSteps.find((step) => step.id === item.stepId);
  const getNavigationStepStatus = (item?: ArtifactPanelItem, step?: ThreadStepSummary): StepStatus => {
    const threadStatus = normalizeThreadStepStatus(step?.status);
    if (threadStatus) {
      return threadStatus;
    }
    if (item) {
      return getArtifactStep(item)?.status || "pending";
    }
    return "pending";
  };
  const getArtifactStatusLabel = (item: ArtifactPanelItem, stepSummary?: ThreadStepSummary) => {
    const state = workflowResults[item.kind];
    if (state.loading) {
      return t("selfEvolutionRun.artifactLoadingStatus");
    }
    if (state.error) {
      return t("selfEvolutionRun.artifactErrorStatus");
    }
    if (state.loaded) {
      return isEmptyResultPayload(state.data) ? t("selfEvolutionRun.artifactNoResult") : t("selfEvolutionRun.artifactLoaded");
    }
    return localizedGetStepStatusLabel(getNavigationStepStatus(item, stepSummary));
  };
  const getStepNavigationTitle = (item: ArtifactPanelItem | undefined, step: ThreadStepSummary | undefined, index: number) => {
    if (step && !isOpaqueStepTitle(step.title, step.stepId)) {
      return step.title;
    }
    return item?.sectionTitle || `Step ${index + 1}`;
  };
  const getStepNavigationDesc = (item: ArtifactPanelItem | undefined, step: ThreadStepSummary | undefined) => {
    if (item) {
      return item.sectionDesc;
    }
    return step?.stepId ? t("selfEvolutionRun.stepIdLabel", { id: getShortLabel(step.stepId) }) : t("selfEvolutionRun.waitingStepInfo");
  };
  const renderArtifactNavigationPanel = () => (
    <>
      {threadStepNavigationItems.length > 0 ? (
        threadStepNavigationItems.map(({ item, step, index }) => {
          const stepStatus = getNavigationStepStatus(item, step);
          const isSelected = selectedThreadStepId === step.stepId;
          const isActive = isSelected || step.active || Boolean(item && item.kind === activeArtifactItem?.kind);
          const isStepLoading = loadingThreadStepId === step.stepId;

          return (
            <button
              key={step.stepId}
              type="button"
              className={`self-evolution-artifact-item${isActive ? " is-active" : ""}${isStepLoading ? " is-loading" : ""}`}
              onClick={(event) => {
                event.stopPropagation();
                void onSelectThreadStep(step, item?.stepId);
              }}
            >
              <span className="self-evolution-artifact-item-title">
                {getStepNavigationTitle(item, step, index)}
              </span>
              <span className="self-evolution-artifact-item-desc">
                {getStepNavigationDesc(item, step)}
              </span>
              <span className={`self-evolution-artifact-item-status is-${stepStatus}`}>
                {isStepLoading
                  ? t("selfEvolutionRun.artifactLoadingStatus")
                  : item
                    ? getArtifactStatusLabel(item, step)
                    : localizedGetStepStatusLabel(stepStatus)}
              </span>
            </button>
          );
        })
      ) : visibleArtifactItems.length === 0 ? (
        <Paragraph className="self-evolution-artifact-empty">
          {t("selfEvolutionRun.artifactEmptyHint")}
        </Paragraph>
      ) : (
        visibleArtifactItems.map((item) => {
          const step = getArtifactStep(item);
          const stepStatus = getNavigationStepStatus(item);
          const isActive = item.kind === activeArtifactItem?.kind;
          const resultState = workflowResults[item.kind];
          const hasLoadedArtifact = resultState.loaded && !isEmptyResultPayload(resultState.data);
          const canOpenArtifact = stepStatus === "done" || hasLoadedArtifact;

          return (
            <button
              key={item.kind}
              type="button"
              className={`self-evolution-artifact-item${isActive ? " is-active" : ""}`}
              onClick={(event) => {
                event.stopPropagation();
                if (!canOpenArtifact) {
                  message.info(t("selfEvolutionRun.artifactNotReady", { title: item.title }), 2);
                  return;
                }
                openWorkflowArtifact(item.kind);
              }}
            >
              <span className="self-evolution-artifact-item-title">
                {step?.title || item.sectionTitle}
              </span>
              <span className="self-evolution-artifact-item-desc">
                {item.sectionDesc}
              </span>
              <span className={`self-evolution-artifact-item-status is-${stepStatus}`}>
                {getArtifactStatusLabel(item)}
              </span>
            </button>
          );
        })
      )}
    </>
  );
  const renderCaseArtifactPreview = () => {
    if (!caseArtifact) {
      return null;
    }
    if (caseArtifact.loading) {
      return (
        <div className="self-evolution-result-state is-loading">
          <LoadingOutlined spin />
          <span>{t("selfEvolutionRun.caseArtifactLoading", { id: caseArtifact.artifactId })}</span>
        </div>
      );
    }
    if (caseArtifact.error) {
      return (
        <div className="self-evolution-result-state is-error" role="alert">
          <span>{caseArtifact.error}</span>
          <button type="button" onClick={() => void openCaseArtifact(caseArtifact.kind, caseArtifact.artifactId, caseArtifact.title, caseArtifact.caseId)}>
            {t("selfEvolutionRun.resultRetry")}
          </button>
        </div>
      );
    }
    const traceObservation = normalizeTraceObservation(caseArtifact.data);
    if (traceObservation) {
      return (
        <TraceObservationView
          observation={traceObservation}
          title={traceObservation.kind === "compare" ? `${caseArtifact.title}${t("selfEvolutionRun.caseTraceABObservationSuffix")}` : `${caseArtifact.title}${t("selfEvolutionRun.caseObservationDetailSuffix")}`}
        />
      );
    }
    return (
      <div className="self-evolution-result-json">
        <div className="self-evolution-result-json-head">
          <Text>{caseArtifact.artifactId}</Text>
          <Text>{t("selfEvolutionRun.resultItemCount", { count: getResultItems(caseArtifact.data).length || 1 })}</Text>
        </div>
        <pre>{stringifyResultPayload(caseArtifact.data)}</pre>
      </div>
    );
  };
  const renderArtifactPanel = () => (
    caseArtifact ? (
      <section className="self-evolution-artifact-detail" aria-label={t("selfEvolutionRun.artifactDetailAria")}>
        <div className="self-evolution-artifact-detail-head">
          <div>
            <Text strong>{caseArtifact.title}</Text>
            <span>{`${getWorkflowResultLabels()[caseArtifact.kind]} · ${t("selfEvolutionRun.singleCaseArtifact")}`}</span>
          </div>
        </div>
        <div className="self-evolution-artifact-detail-body">
          {renderCaseArtifactPreview()}
        </div>
      </section>
    ) : activeArtifactItem ? (
      <section className="self-evolution-artifact-detail" aria-label={t("selfEvolutionRun.artifactProductDetail")}>
        <div className="self-evolution-artifact-detail-head">
          <div>
            <Text strong>{activeArtifactItem.title}</Text>
            <span>{activeArtifactItem.desc}</span>
          </div>
          <button
            type="button"
            onClick={(event) =>
              void handleWorkflowDownload(
                activeArtifactItem.kind,
                activeArtifactItem.fallbackUrl,
                activeArtifactItem.fileName,
                event,
              )
            }
          >
            <DownloadOutlined />
            <span>{t("selfEvolutionRun.downloadArtifact")}</span>
          </button>
        </div>
        <div className={`self-evolution-artifact-detail-body${activeArtifactItem.kind === "analysis-reports" ? " is-analysis-report" : ""}`}>
          {activeArtifactItem.preview}
        </div>
      </section>
    ) : null
  );

  return (
    <>
      {children({
        isWorkbenchVisible,
        homeViewProps: {
          isLoadingThreadHistoryList,
          workflowSteps,
          launchOptionCards,
          launchSummaryItems,
          isLaunchConfigValid,
          isStartingSession,
          onOpenHistorySessionModal,
          onStartSession,
        },
        homeHistoryModalProps: {
          open: isHistorySessionModalOpen,
          threadHistoryListError,
          isLoadingThreadHistoryList,
          historySessionEntries,
          deletingHistoryKeys,
          onCancel: () => setIsHistorySessionModalOpen(false),
          onRetry: () => void fetchThreadHistoryList(),
          onSelectHistorySession,
          onEnterHistorySession: enterHistorySession,
          onDeleteHistorySession,
        },
        workbenchViewProps: {
          processDashboard,
          finalResultSummary,
          abtestPreviewPanel: renderAbTestPreview(),
          activeWorkbenchTab,
          artifactNavigationPanel: renderArtifactNavigationPanel(),
          artifactPanel: renderArtifactPanel(),
          isArtifactPanelOpen: isArtifactPanelOpen && Boolean(activeArtifactItem || caseArtifact),
          activeStepText,
          routeThreadId,
          isRestoringThread,
          threadRestoreError,
          activeSession,
          chatSessionsCount: chatSessions.length,
          historySessionEntries,
          deletingHistoryKeys,
          displayedMessages,
          chatStreamRef,
          isAutoMode,
          isAutoInteractionActive,
          isPlanningNextStep,
          isSendingMessage,
          displayedCheckpointWaitPrompt,
          prompt,
          selectedViewStage,
          isHistorySessionModalOpen,
          threadHistoryListError,
          isLoadingThreadHistoryList,
          isNewSessionConfigOpen,
          newSessionOptionCards,
          newSessionSummaryItems,
          isNewSessionStepOneDone,
          isNewSessionStepTwoDone,
          isNewSessionStepThreeDone,
          isNewSessionStepFourDone,
          isNewSessionConfirmDisabled: !isNewSessionDraftValid || isConfirmingNewSession,
          isConfirmingNewSession,
          getStepStatusLabel: localizedGetStepStatusLabel,
          renderKnowledgeAndModeTools,
          renderSendButton,
          onRetryRestoreThread: () => {
            if (!routeThreadId) {
              return;
            }
            const controller = new AbortController();
            void restoreThreadDetail(routeThreadId, controller.signal);
          },
          onCloseSession,
          onSelectHistorySession,
          onEnterHistorySession: enterHistorySession,
          onDeleteHistorySession,
          onCreateSession,
          onOpenHistorySessionModal,
          onPromptChange: setPrompt,
          onSend: (command) => void onSend(command),
          onConfirmIntentCheckpoint: () => void onConfirmIntentCheckpoint(),
          onContinueCheckpoint: (command?: string) => void onContinueCheckpoint(command),
          onOpenArtifact: openWorkflowArtifact,
          onOpenObservation: openObservationPage,
          onOpenCaseArtifact: openCaseArtifact,
          onWorkbenchTabChange: handleWorkbenchTabChange,
          onCloseArtifactPanel: closeArtifactPanel,
          onCloseHistorySessionModal: () => setIsHistorySessionModalOpen(false),
          onRetryThreadHistoryList: () => void fetchThreadHistoryList(),
          onCancelCreateSession,
          onConfirmCreateSession,
        },
      })}
    </>
  );
}
