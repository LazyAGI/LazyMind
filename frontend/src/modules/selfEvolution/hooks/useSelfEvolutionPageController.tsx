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
import {
  DatasetWorkflowStep,
  PxReportWorkflowStep,
  AnalysisWorkflowStep,
  CodeOptimizeWorkflowStep,
  AbTestWorkflowStep,
} from "../components/WorkflowSteps";
import { type HistorySessionModalProps } from "../components/HistorySessions";
import { type SelfEvolutionHomeViewProps } from "../components/LaunchViews";
import { type SelfEvolutionWorkbenchViewProps } from "../components/WorkbenchView";
import { AnalysisRuntimeSummary, ApplyRuntimeSummary } from "../components/ExecutionSummaries";
import "../index.scss";
import {
  EvolutionMode,
  ExtraEvalStrategy,
  WorkflowStep,
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
  workflowResultLabels,
  createCoreAgentApiClient,
  DiffFileTreeNode,
  PxCategoryMetricAverage,
  AbCategoryComparison,
  AbSummaryMetricRow,
  AbTopDiffRow,
  AbSummaryReport,
  pxMetricMeta,
  getKnowledgeBaseName,
  isCanceledRequest,
  existingEvalSetOptions,
  evalSetPreviewData,
  clampScore,
  formatPercent,
  buildPxCategoryMetricAveragesFromReport,
  getTimeLabel,
  createInitialWorkflowRuntimeState,
  createThreadRestoreWorkflowRuntimeState,
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
  isTerminalThreadEvent,
  normalizeThreadEvent,
  compareNormalizedThreadEvents,
  dedupeNormalizedEvents,
  buildVisibleWorkflowSteps,
  buildAnalysisRunSummary,
  buildApplyRunSummary,
  getPendingCheckpointWaitPrompt,
  isTerminalAbtestCheckpoint,
  isThreadEventAfter,
  reduceWorkflowRuntimeState,
  getThreadTitleFromHistoryPayload,
  getThreadTitleFromPayload,
  getThreadKnowledgeBaseId,
  getThreadModeFromPayload,
} from "../shared";
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
  const [isRestoringThread, setIsRestoringThread] = useState(false);
  const [isHistorySessionModalOpen, setIsHistorySessionModalOpen] = useState(false);
  const [isLoadingThreadHistoryList, setIsLoadingThreadHistoryList] = useState(false);
  const [deletingHistoryKeys, setDeletingHistoryKeys] = useState<string[]>([]);
  const [threadHistoryListError, setThreadHistoryListError] = useState("");
  const [threadRestoreError, setThreadRestoreError] = useState("");
  const [isNewSessionConfigOpen, setIsNewSessionConfigOpen] = useState(false);
  const [hasNewSessionValidationTriggered, setHasNewSessionValidationTriggered] = useState(false);
  const [newSessionDraft, setNewSessionDraft] = useState<NewSessionDraft>({});
  const [workflowRuntimeState, setWorkflowRuntimeState] = useState<WorkflowRuntimeState>(
    createInitialWorkflowRuntimeState,
  );
  const [workflowResults, setWorkflowResults] = useState<WorkflowResultsState>(
    createInitialWorkflowResultsState,
  );
  const [liveCheckpointWaitPrompt, setLiveCheckpointWaitPrompt] = useState<CheckpointWaitPrompt>();
  const [diffArtifactContent, setDiffArtifactContent] = useState<DiffArtifactContentState>({
    loading: false,
    key: "",
    content: "",
  });
  const [threadEvents, setThreadEvents] = useState<NormalizedThreadEvent[]>([]);
  const threadEventsRef = useRef<NormalizedThreadEvent[]>([]);
  const [remoteThreadHistory, setRemoteThreadHistory] = useState<ThreadHistoryEntry[]>([]);
  const isThreadHistoryListFetchingRef = useRef(false);
  const [chatSessions, setChatSessions] = useState<ChatSession[]>([
    {
      id: "session-1",
      title: "当前会话",
      updatedAt: "刚刚",
      messages: [],
    },
  ]);
  const [activeSessionId, setActiveSessionId] = useState("session-1");
  const chatStreamRef = useRef<HTMLDivElement | null>(null);
  const threadEventsAbortRef = useRef<{ threadId: string; controller: AbortController } | null>(null);
  const processedThreadEventIdsRef = useRef<Set<string>>(new Set());
  const processedWorkflowEventKeysRef = useRef<Set<string>>(new Set());
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
      const option = existingEvalSetOptions.find((item) => item.value === value);
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
  const modeLabel = mode === "auto" ? t("selfEvolutionRun.modeAuto") : t("selfEvolutionRun.modeInteractive");
  const isKnowledgeBaseRequired = !selectedKb;
  const isLaunchConfigComplete = Boolean(selectedKb && selectedEvalSet && extraEvalStrategy && mode);
  const isLaunchConfigValid =
    isLaunchConfigComplete && (!isExtraEvalRequired || extraEvalStrategy === "generate");
  const isSendDisabled = !prompt.trim() || isSendingMessage;
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
    () => buildVisibleWorkflowSteps(threadEvents, workflowRuntimeState, isWorkbenchVisible),
    [isWorkbenchVisible, threadEvents, workflowRuntimeState],
  );
  const analysisRunSummary = useMemo(() => buildAnalysisRunSummary(threadEvents), [threadEvents]);
  const applyRunSummary = useMemo(() => buildApplyRunSummary(threadEvents), [threadEvents]);
  const pendingCheckpointWaitPrompt = useMemo(
    () => liveCheckpointWaitPrompt || getPendingCheckpointWaitPrompt(threadEvents),
    [liveCheckpointWaitPrompt, threadEvents],
  );
  const activeStepText = useMemo(() => {
    const activeStep =
      workflowSteps.find((item) => item.status === "running") ||
      workflowSteps.find((item) => item.status === "paused") ||
      workflowSteps.find((item) => item.status === "failed") ||
      workflowSteps.find((item) => item.status === "pending");
    return activeStep?.title || t("selfEvolutionRun.workflowCompleted");
  }, [workflowSteps, t]);
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
  const pxReportCategoryMetrics = fetchedPxCategoryMetricAverages;
  const isSinglePxCategory = pxReportCategoryMetrics.length === 1;
  const pxReportTotalCases = useMemo(() => {
    const sourceRecord = Array.isArray(workflowResults["eval-reports"].data)
      ? (workflowResults["eval-reports"].data.find((item): item is Record<string, unknown> => isRecord(item)) ??
        undefined)
      : isRecord(workflowResults["eval-reports"].data)
        ? workflowResults["eval-reports"].data
        : undefined;
    const caseDetailSummary =
      getStructuredRecordField(sourceRecord, ["case_details_summary"]) ||
      getNestedRecordField(sourceRecord, ["case_details_summary"]);

    return (
      getNumberField(caseDetailSummary, ["total_count"]) ||
      getNumberField(sourceRecord, ["total_cases"]) ||
      pxReportCategoryMetrics.reduce((total, item) => total + item.caseCount, 0)
    );
  }, [pxReportCategoryMetrics, workflowResults]);
  const abSummaryReports = useMemo<AbSummaryReport[]>(
    () => buildAbSummaryReports(workflowResults.abtests.data),
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
          `正确性 ${formatMetricDelta(item.delta.answer_correctness)}`,
          `忠实性 ${formatMetricDelta(item.delta.faithfulness)}`,
          `上下文召回 ${formatMetricDelta(item.delta.context_recall)}`,
          `文档召回 ${formatMetricDelta(item.delta.doc_recall)}`,
        ].join(" / "),
      })),
    [abCategoryComparisons],
  );
  const abComparisonColumns = useMemo<ColumnsType<AbComparisonRow>>(
    () => [
      { title: "评测分类", dataIndex: "category", key: "category", width: 140 },
      {
        title: "基线结果",
        dataIndex: "baselineSummary",
        key: "baselineSummary",
        width: 320,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
      {
        title: "优化结果",
        dataIndex: "experimentSummary",
        key: "experimentSummary",
        width: 320,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
      {
        title: "变化摘要",
        dataIndex: "deltaSummary",
        key: "deltaSummary",
        width: 320,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
    ],
    [],
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
  }, [activeMessages, threadDialogueMessages]);
  const displayedCheckpointWaitPrompt =
    isAutoInteractionActive || isTerminalAbtestCheckpoint(pendingCheckpointWaitPrompt)
      ? undefined
      : pendingCheckpointWaitPrompt;
  const datasetResultDownloadUrl = useMemo(
    () => buildCoreDownloadUrl(getResultDownloadPath(workflowResults.datasets.data)),
    [workflowResults.datasets.data],
  );
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
          error: getLocalizedErrorMessage(error, "代码文件内容加载失败，请稍后重试。"),
        });
      });

    return () => {
      controller.abort();
    };
  }, [diffArtifactFiles, diffArtifactKey, directFetchedDiffText, fetchDiffDownloadText, workflowResults.diffs.data]);

  const historySessionEntries = useMemo<HistorySessionEntry[]>(() => {
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
      }));
    const mergedEntries = [
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
        })),
    ];

    return mergedEntries.sort((a, b) =>
      b.updatedAt.localeCompare(a.updatedAt, "zh-CN", { numeric: true }),
    );
  }, [activeSessionId, activeThreadId, chatSessions, remoteThreadHistory]);
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
  const fetchWorkflowResult = useCallback(
    async (kind: WorkflowResultKind, options?: { force?: boolean }) => {
      if (!activeThreadId) {
        message.warning("当前没有可用线程 ID，无法请求结果接口。", 2);
        return undefined;
      }

      const currentState = workflowResults[kind];
      if (!options?.force && (currentState.loading || currentState.loaded)) {
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
        return response.data;
      } catch (error) {
        setWorkflowResults((prev) => ({
          ...prev,
          [kind]: {
            ...prev[kind],
            loading: false,
            loaded: true,
            error: getLocalizedErrorMessage(error, `${workflowResultLabels[kind]}加载失败，请稍后重试。`),
          },
        }));
        return undefined;
      }
    },
    [activeThreadId, getWorkflowResultUrl, workflowResults],
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

      if (!downloadUrl) {
        message.warning(`${workflowResultLabels[kind]}暂无可下载文件。`, 1.5);
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
  const handleWorkflowResultCollapseChange = useCallback(
    (kind: WorkflowResultKind) => (activeKeys: string | string[]) => {
      const isOpen = Array.isArray(activeKeys) ? activeKeys.length > 0 : Boolean(activeKeys);
      if (isOpen) {
        void fetchWorkflowResult(kind);
      }
    },
    [fetchWorkflowResult],
  );

  useEffect(() => {
    const controller = new AbortController();
    fetchKnowledgeBaseOptions(controller.signal);

    return () => {
      controller.abort();
    };
  }, [fetchKnowledgeBaseOptions]);

  useEffect(() => {
    setWorkflowResults(createInitialWorkflowResultsState());
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
    ...existingEvalSetOptions.map((item) => ({
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
        target_chat_url: "http://evo-chat:8046/api/chat",
        dataset_name: "algo",
      },
    });
    const threadId = extractThreadId(createResponse.data);
    if (!threadId) {
      throw new Error("创建 thread 成功但响应中缺少 thread_id");
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
    setLiveCheckpointWaitPrompt(undefined);
    setThreadEvents(events);
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
    if (!delta) {
      return;
    }

    const nowLabel = getTimeLabel();
    const streamMessageId = `${sessionId}-assistant-stream-${streamId}`;
    const initialContent = kind === "thinking" ? `思考过程：${delta}` : delta;
    const getNextContent = (currentMessage: ChatMessage) => {
      if (kind === "thinking") {
        return `${currentMessage.content}${delta}`;
      }
      const needsAnswerSeparator =
        currentMessage.content.startsWith("思考过程：") && !currentMessage.streamAnswerStarted;
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
          event.type === "checkpoint.cancel"
        ) {
          return undefined;
        }
        if (prev.nextStage && event.stage === prev.nextStage) {
          return undefined;
        }
        const latestCheckpointEvent = threadEventsRef.current
          .filter((item) => item.type === "checkpoint.wait" && item.checkpointWait)
          .sort(compareNormalizedThreadEvents)
          .at(-1);
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

  const consumeThreadMessageStream = async (
    response: Response,
    sessionId: string,
    signal?: AbortSignal,
  ) => {
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
        const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
        if (chatStreamDeltaKind) {
          const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
          appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
        }
        if (isTerminalThreadEvent(event.type)) {
          return;
        }
      }
    }

    const trailingText = buffer.trim();
    if (trailingText) {
      const frame = parseSSEFrame(trailingText);
      if (frame) {
        const event = normalizeThreadEvent(frame);
        const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
        if (chatStreamDeltaKind) {
          const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
          appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
        }
      }
    }
  };

  const subscribeThreadEvents = async (threadId: string, sessionId = activeSessionId) => {
    const activeSubscription = threadEventsAbortRef.current;
    if (activeSubscription?.threadId === threadId && !activeSubscription.controller.signal.aborted) {
      return;
    }

    if (activeSubscription && !activeSubscription.controller.signal.aborted) {
      activeSubscription.controller.abort();
    }
    processedThreadEventIdsRef.current = new Set();

    const controller = new AbortController();
    const subscription = { threadId, controller };
    threadEventsAbortRef.current = subscription;
    const shouldAppendEventChat = mode === "auto";

    try {
      const response = await fetch(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}:events`, {
        method: "GET",
        headers: {
          Accept: "text/event-stream",
          ...AgentAppsAuth.getAuthHeaders(),
        },
        signal: controller.signal,
      });

      if (!response.ok) {
        throw new Error(`事件流连接失败：HTTP ${response.status}`);
      }
      if (!response.body) {
        throw new Error("事件流连接失败：浏览器未返回可读流");
      }

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
          applyWorkflowEvent(event, sessionId, { appendChat: shouldAppendEventChat });
          if (isTerminalThreadEvent(event.type)) {
            controller.abort();
            break;
          }
        }
      }

      const trailingText = buffer.trim();
      if (!controller.signal.aborted && trailingText) {
        const frame = parseSSEFrame(trailingText);
        if (frame) {
          applyWorkflowEvent(normalizeThreadEvent(frame), sessionId, { appendChat: shouldAppendEventChat });
        }
      }
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      message.error(getLocalizedErrorMessage(error, "线程事件流连接失败，请检查 SSE 接口。"), 2);
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
    replaceThreadEvents([]);
    processedWorkflowEventKeysRef.current = new Set();

    const restoredSessionId = `thread-${threadId}`;
    subscribeThreadEvents(threadId, restoredSessionId);
    setActiveSessionId(restoredSessionId);
    setChatSessions([
      {
        id: restoredSessionId,
        title: "线程恢复中",
        updatedAt: getTimeLabel(),
        threadId,
        messages: [
          {
            id: `${threadId}-restore-loading`,
            role: "assistant",
            content: `正在恢复自进化线程：${threadId}`,
            time: getTimeLabel(),
          },
        ],
      },
    ]);

    try {
      const encodedThreadId = encodeURIComponent(threadId);
      let historyTitle: string | undefined;
      let historyMessages: ChatMessage[] = [];
      try {
        const historyPayload = (
          await axiosInstance.get(
            `${AGENT_API_BASE}/threads/${encodedThreadId}/history`,
            { signal },
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
        `自进化详情 ${threadId.slice(0, 8)}`;
      applySessionRestore(titleFromHistory, true);
      setActiveSessionId(restoredSessionId);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);

      const threadResult = await axiosInstance.get(`${AGENT_API_BASE}/threads/${encodedThreadId}`, { signal });
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
    } catch (error) {
      if (signal?.aborted || isCanceledRequest(error)) {
        return;
      }
      const responseStatus = (error as AxiosError | undefined)?.response?.status;
      const errorTextRaw = getLocalizedErrorMessage(error, "线程详情恢复失败，请稍后重试。") || "";
      const isThreadNotFound = responseStatus === 404 && errorTextRaw.toLowerCase().includes("thread not found");
      if (isThreadNotFound) {
        setWorkflowRuntimeState(createThreadRestoreWorkflowRuntimeState());
        setWorkflowResults(createInitialWorkflowResultsState());
      }
      const errorText =
        errorTextRaw ||
        "线程详情恢复失败，请稍后重试。";
      setThreadRestoreError(errorText);
      setChatSessions([
        {
          id: restoredSessionId,
          title: `自进化详情 ${threadId.slice(0, 8)}`,
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

  const onSend = async (command?: string) => {
    const trimmedPrompt = (command ?? prompt).trim();
    const activeThreadId = activeSession?.threadId;
    if (isKnowledgeBaseRequired && !activeThreadId) {
      setHasLaunchValidationTriggered(true);
      message.warning("必须选择知识库才可以生成数据集。", 1.2);
      return;
    }
    if (!trimmedPrompt) {
      return;
    }

    const firstRound = !isWorkbenchVisible;
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
          throw new Error(`消息发送失败：HTTP ${response.status}`);
        }

        const contentType = response.headers.get("content-type") || "";
        if (contentType.includes("text/event-stream")) {
          await consumeThreadMessageStream(response, activeSessionId, controller.signal);
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
      } catch (error) {
        appendSystemMessage(
          getLocalizedErrorMessage(error, "消息发送失败，请检查 message 接口。") ||
            "消息发送失败，请检查 message 接口。",
          activeSessionId,
        );
      } finally {
        setIsSendingMessage(false);
      }
      return;
    }

    if (firstRound) {
      setIsWorkbenchVisible(true);
      message.success(`已基于「${selectedKnowledgeBase}」启动流程：${activeStepText}`, 1.2);
    }
    appendSystemMessage(
      firstRound
        ? `已收到任务「${trimmedPrompt}」，进入第一步：生成数据集。我会先产出样本结构和数据集数据。`
        : "已继续推进当前流程，我会优先补齐数据集样本并回传下一步状态。",
      activeSessionId,
    );
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
      setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
      replaceThreadEvents([]);
      processedWorkflowEventKeysRef.current = new Set();
      setIsWorkbenchVisible(true);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);
      const nowLabel = getTimeLabel();
      setChatSessions((prev) =>
        prev.map((session) =>
          session.id === activeSessionId
            ? {
                ...session,
                threadId,
                title: session.title === "当前会话" ? selectedKnowledgeBase : session.title,
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
                          )}\n\n线程 ID：${threadId}`,
                          time: nowLabel,
                        },
                      ]
                    : session.messages,
              }
            : session,
        ),
      );
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      message.success("已调用接口并启动自进化流程。", 1.2);
    } catch (error) {
      showLocalErrorWhenNotHandledByAxios(error, "启动自进化流程失败，请检查接口联调状态。");
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
      knowledgeBaseOptions.find((item) => item.value === nextKnowledgeBase)?.label || "知识库";
    const nextEvalSetLabel = getExistingEvalSetLabel(nextEvalSet);
    const nextExtraEvalLabel = nextExtraEvalStrategy === "generate" ? "是，补充评测集" : "否，不补充";
    const nextInterventionLabel = nextMode === "interactive" ? "是，人工干预" : "否，自动处理";
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
        title: `新会话 ${nextIndex}`,
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
            )}\n\n线程 ID：${threadId}`,
            time: nowLabel,
          },
        ],
      };

      setSelectedKb(nextKnowledgeBase);
      setSelectedEvalSet(nextEvalSet);
      setExtraEvalStrategy(nextExtraEvalStrategy);
      setMode(nextMode);
      setHasLaunchValidationTriggered(false);
      setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
      replaceThreadEvents([]);
      processedWorkflowEventKeysRef.current = new Set();
      setChatSessions((prev) => [...prev, newSession]);
      setActiveSessionId(newSessionId);
      setPrompt("");
      setIsWorkbenchVisible(true);
      setIsNewSessionConfigOpen(false);
      setHasNewSessionValidationTriggered(false);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      message.success("已调用接口并启动新会话流程。", 1.2);
    } catch (error) {
      showLocalErrorWhenNotHandledByAxios(error, "启动新会话流程失败，请检查接口联调状态。");
    } finally {
      setIsConfirmingNewSession(false);
    }
  };

  const onCloseSession = (sessionId: string) => {
    if (chatSessions.length <= 1) {
      message.info("至少保留一个会话标签。", 1);
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
        message.info("暂未获取到服务端历史会话。", 1.2);
      }
    } catch (error) {
      const errorText =
        getLocalizedErrorMessage(error, "获取历史会话列表失败，请稍后重试。") ||
        "获取历史会话列表失败，请稍后重试。";
      setThreadHistoryListError(errorText);
      message.error(errorText, 2);
    } finally {
      isThreadHistoryListFetchingRef.current = false;
      setIsLoadingThreadHistoryList(false);
    }
  }, []);

  useEffect(() => {
    if (view !== "detail" || !routeThreadId) {
      return;
    }
    void fetchThreadHistoryList({ showEmptyMessage: false });
  }, [fetchThreadHistoryList, routeThreadId, view]);

  const onOpenHistorySessionModal = () => {
    setIsHistorySessionModalOpen(true);
    void fetchThreadHistoryList({ showEmptyMessage: true });
  };

  const onSelectHistorySession = (entry: {
    sessionId?: string;
    threadId?: string;
    title?: string;
  }) => {
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

  const resetToEmptySession = () => {
    const nowLabel = getTimeLabel();
    const nextSessionId = `session-${Date.now()}`;
    setChatSessions([
      {
        id: nextSessionId,
        title: "当前会话",
        updatedAt: nowLabel,
        messages: [],
      },
    ]);
    setActiveSessionId(nextSessionId);
    setIsWorkbenchVisible(false);
    setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
    setWorkflowResults(createInitialWorkflowResultsState());
    replaceThreadEvents([]);
    processedWorkflowEventKeysRef.current = new Set();
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

  const renderWorkflowResultPayload = (kind: WorkflowResultKind) => {
    const resultState = workflowResults[kind];
    const label = workflowResultLabels[kind];

    if (resultState.loading) {
      return (
        <div className="self-evolution-result-state is-loading">
          <LoadingOutlined spin />
          <span>{`正在请求${label}接口...`}</span>
        </div>
      );
    }

    if (resultState.error) {
      return (
        <div className="self-evolution-result-state is-error" role="alert">
          <span>{resultState.error}</span>
          <button type="button" onClick={() => void fetchWorkflowResult(kind, { force: true })}>
            重试
          </button>
        </div>
      );
    }

    if (!resultState.loaded) {
      return (
        <Paragraph className="self-evolution-px-empty">
          展开后会请求当前线程的{label}接口。
        </Paragraph>
      );
    }

    if (isEmptyResultPayload(resultState.data)) {
      return (
        <Paragraph className="self-evolution-px-empty">
          {`${label}接口已返回，当前暂无可展示结果。`}
        </Paragraph>
      );
    }

    return (
      <div className="self-evolution-result-json">
        <div className="self-evolution-result-json-head">
          <Text>{`${label}接口返回`}</Text>
          <Text>{`${getResultItems(resultState.data).length || 1} 条`}</Text>
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
    const metricValues = pxMetricMeta.map((metric) => ({
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
      <div className="self-evolution-px-chart-wrap" aria-label="单分类指标饼图">
        <svg className="self-evolution-px-pie-chart" viewBox={`0 0 ${chartSize} ${chartSize}`} role="img">
          <title>{`${categoryMetric.category} 指标分布`}</title>
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
            {`${categoryMetric.caseCount} 条`}
          </text>
        </svg>
      </div>
    );
  };

  const renderPxMultiCategoryBars = (categoryMetrics: PxCategoryMetricAverage[]) => {
    const width = 640;
    const height = 280;
    const padding = { top: 18, right: 24, bottom: 46, left: 44 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const categoryCount = categoryMetrics.length;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const groupWidth = chartWidth / Math.max(categoryCount, 1);
    const metricCount = pxMetricMeta.length;
    const barGap = 4;
    const groupInnerWidth = Math.min(96, groupWidth * 0.74);
    const barWidth = Math.max(5, Math.min(18, (groupInnerWidth - barGap * (metricCount - 1)) / metricCount));
    const groupBarsWidth = barWidth * metricCount + barGap * (metricCount - 1);
    const xToCenter = (index: number) => padding.left + groupWidth * index + groupWidth / 2;
    const axisTicks = [0, 0.25, 0.5, 0.75, 1];

    return (
      <div className="self-evolution-px-chart-wrap" aria-label="多分类指标柱状图">
        <svg className="self-evolution-px-bar-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>问题分类指标均值柱状图</title>
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
                {pxMetricMeta.map((metric, metricIndex) => {
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
                y={height - 16}
                textAnchor="middle"
                className="self-evolution-px-axis-label"
              >
                {item.category}
              </text>
            );
          })}
        </svg>
      </div>
    );
  };

  const renderPxCategorySummaryGrid = (categoryMetrics: PxCategoryMetricAverage[]) => (
    <div className="self-evolution-px-summary-grid" aria-label="问题类别指标概览">
      {categoryMetrics.map((item) => (
        <article key={item.category} className="self-evolution-px-summary-card">
          <div className="self-evolution-px-summary-card-head">
            <div className="self-evolution-px-summary-card-title-group">
              <span className="self-evolution-px-summary-card-icon" aria-hidden>
                {item.category.slice(0, 1)}
              </span>
              <div className="self-evolution-px-summary-card-copy">
                <strong>{item.category}</strong>
                <span>{`${item.caseCount} 条样本`}</span>
              </div>
            </div>
          </div>
          <div className="self-evolution-px-summary-metrics">
            {pxMetricMeta.map((metric) => (
              <div key={`${item.category}-${metric.key}`} className="self-evolution-px-summary-metric-row">
                <span className="self-evolution-px-summary-metric-label">
                  <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                  {metric.label}
                </span>
                <strong>{formatPercent(item.metrics[metric.key])}</strong>
              </div>
            ))}
          </div>
        </article>
      ))}
    </div>
  );

  const renderPxReportPreview = () => (
    <section className="self-evolution-px-report" aria-label="评测报告指标展示">
      {workflowResults["eval-reports"].loading ? (
        renderWorkflowResultPayload("eval-reports")
      ) : workflowResults["eval-reports"].error ? (
        renderWorkflowResultPayload("eval-reports")
      ) : (
        <>
      <div className="self-evolution-px-report-head">
        <Text>按问题类别聚合四项指标均值</Text>
        <Text>{`样本数 ${pxReportTotalCases}，分类数 ${pxReportCategoryMetrics.length}`}</Text>
      </div>

      {pxReportCategoryMetrics.length === 0 ? (
        <Paragraph className="self-evolution-px-empty">当前报告无可用指标数据。</Paragraph>
      ) : isSinglePxCategory ? (
        <div className="self-evolution-px-panel">
          {renderPxSingleCategoryPie(pxReportCategoryMetrics[0])}
          <div className="self-evolution-px-legend">
            {pxMetricMeta.map((metric) => (
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
          {renderPxMultiCategoryBars(pxReportCategoryMetrics)}
          <div className="self-evolution-px-legend is-compact">
            {pxMetricMeta.map((metric) => (
              <div key={metric.key} className="self-evolution-px-legend-item">
                <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                <span className="self-evolution-px-legend-label">{metric.label}</span>
              </div>
            ))}
          </div>
        </div>
      )}
      {renderPxCategorySummaryGrid(pxReportCategoryMetrics)}
        </>
      )}
    </section>
  );

  const renderAnalysisReportPreview = () => (
    <section className="self-evolution-analysis-report" aria-label="分析报告展示">
      <div className="self-evolution-analysis-head">
        <Text>完整分析报告</Text>
      </div>
      <div className="self-evolution-analysis-body">
        {workflowResults["analysis-reports"].loaded ||
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

  const renderAnalysisRuntimeSummary = () => <AnalysisRuntimeSummary summary={analysisRunSummary} />;

  const renderApplyRuntimeSummary = () => <ApplyRuntimeSummary summary={applyRunSummary} />;

  const renderCodeOptimizeDiffPreview = () => {
    if (!directFetchedDiffText && diffArtifactContent.loading && !diffArtifactContent.content) {
      return (
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
            <Text>正在加载文件内容</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">正在读取代码文件内容...</Paragraph>
        </section>
      );
    }

    if (!directFetchedDiffText && diffArtifactContent.error && !diffArtifactContent.content) {
      return (
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
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
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
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
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">当前没有可展示的变更文件。</Paragraph>
        </section>
      );
    }

    const allLineCount = parsedDiffFiles.reduce((total, file) => total + file.lines.length, 0);
    return (
      <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
        <div className="self-evolution-optimize-head">
          <Text>代码改动详情</Text>
          <Text>{`文件 ${parsedDiffFiles.length} 个，总代码行 ${allLineCount} 行`}</Text>
        </div>
        <div className="self-evolution-optimize-layout">
          <aside className="self-evolution-optimize-tree" aria-label="变更文件结构">
            <div className="self-evolution-optimize-tree-head">文件结构</div>
            <div className="self-evolution-optimize-tree-body">{renderTreeNodes(diffFileTree)}</div>
          </aside>
          <div className="self-evolution-optimize-viewer" aria-label="变更代码内容">
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
    const groupWidth = chartWidth / pxMetricMeta.length;
    const barWidth = Math.min(24, groupWidth * 0.28);
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";

    return (
      <div className="self-evolution-ab-chart-wrap">
        <svg className="self-evolution-ab-single-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>{`${comparison.category} A/B 指标对比`}</title>
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

          {pxMetricMeta.map((metric, index) => {
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
            A 评测（基线）
          </span>
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-b" />
            B 评测（优化后）
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
        {pxMetricMeta.map((metric) => {
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
                  <title>{`${metric.label} 分类型 A/B 对比`}</title>
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
            A 评测（基线）
          </span>
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-b" />
            B 评测（优化后）
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
          <title>A/B 测试 summary 指标对比</title>
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
                  {`胜率 ${formatPercent(row.winRateB)}`}
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
      { title: "指标", dataIndex: "metricLabel", key: "metricLabel", width: 150 },
      { title: "mean A", dataIndex: "meanA", key: "meanA", width: 110, render: (value: number) => formatPercent(value) },
      { title: "mean B", dataIndex: "meanB", key: "meanB", width: 110, render: (value: number) => formatPercent(value) },
      {
        title: "Δmean",
        dataIndex: "deltaMean",
        key: "deltaMean",
        width: 110,
        render: (value: number) => <span className={value >= 0 ? "is-up" : "is-down"}>{formatMetricDelta(value)}</span>,
      },
      { title: "B 胜率", dataIndex: "winRateB", key: "winRateB", width: 110, render: (value: number) => formatPercent(value) },
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
              {report.alignedCases !== undefined && <span>{`对齐样本 ${report.alignedCases}`}</span>}
              {report.primaryMetric && <span>{`主指标 ${formatAbMetricLabel(report.primaryMetric)}`}</span>}
              {report.guardMetrics.length > 0 && (
                <span>{`保护指标 ${report.guardMetrics.map(formatAbMetricLabel).join(" / ")}`}</span>
              )}
            </div>
          </div>
          {report.verdict && <Tag color={report.verdict === "pass" ? "success" : "warning"}>{report.verdict}</Tag>}
        </div>

        {report.metricRows.length > 0 && (
          <div className="self-evolution-ab-chart-shell">
            {renderAbSummaryMetricChart(report.metricRows)}
            <div className="self-evolution-ab-legend">
              <span className="self-evolution-ab-legend-item">
                <span className="self-evolution-ab-legend-dot is-a" />
                A 评测（基线）
              </span>
              <span className="self-evolution-ab-legend-item">
                <span className="self-evolution-ab-legend-dot is-b" />
                B 评测（优化后）
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
            <div className="self-evolution-ab-section-title">Markdown 报告</div>
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
            {report.missingMetrics.length > 0 && <span>{`缺失指标：${report.missingMetrics.join(" / ")}`}</span>}
          </div>
        )}
      </div>
    );
  };

  const renderAbTestPreview = () => (
    <section className="self-evolution-ab-report" aria-label="A/B 对比展示">
      {workflowResults.abtests.loaded && abSummaryReports.length > 0 ? (
        <>
          <div className="self-evolution-ab-head">
            <Text>ABTest 对照报告</Text>
            <Text>{`当前展示 ${abSummaryReports.length} 条`}</Text>
          </div>
          <div className="self-evolution-ab-summary-list">{abSummaryReports.map(renderAbSummaryReport)}</div>
        </>
      ) : workflowResults.abtests.loaded || workflowResults.abtests.loading || workflowResults.abtests.error ? (
        renderWorkflowResultPayload("abtests")
      ) : (
        <>
      <div className="self-evolution-ab-head">
        <Text>对照结果明细</Text>
        <Text>{`当前展示 ${abComparisonRows.length} / 共 ${abCategoryComparisons.length} 条`}</Text>
      </div>
      {abCategoryComparisons.length === 0 ? (
        <Paragraph className="self-evolution-px-empty">暂无可用 A/B 对比数据。</Paragraph>
      ) : (
        <>
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
            {isSingleAbCategory
              ? renderAbSingleCategoryBars(abCategoryComparisons[0])
              : renderAbFacetCharts(abCategoryComparisons)}
          </div>
        </>
      )}
        </>
      )}
    </section>
  );

  const renderStepRuntimeSummary = (step: WorkflowStep) =>
    step.id === "analysis"
      ? renderAnalysisRuntimeSummary()
      : step.id === "code-optimize"
        ? renderApplyRuntimeSummary()
        : undefined;

  const renderStepChildren = (step: WorkflowStep) => {
    if (step.status !== "done") {
      return null;
    }

    if (step.id === "dataset") {
      return (
        <DatasetWorkflowStep
          downloadUrl={datasetResultDownloadUrl}
          fallbackDownloadUrl={datasetDownloadUrl}
          fileName={datasetDownloadFileName}
          getDownloadFileName={getDownloadFileName}
          onDownload={(event) =>
            void handleWorkflowDownload("datasets", datasetDownloadUrl, datasetDownloadFileName, event)
          }
        />
      );
    }

    if (step.id === "px-report") {
      return (
        <PxReportWorkflowStep
          categoryCount={pxReportCategoryMetrics.length}
          isSingleCategory={isSinglePxCategory}
          downloadUrl={evalReportDownloadUrl}
          getDownloadFileName={getDownloadFileName}
          onCollapseChange={handleWorkflowResultCollapseChange("eval-reports")}
          onDownload={(event) =>
            void handleWorkflowDownload("eval-reports", "", "eval-report.json", event)
          }
        >
          {renderPxReportPreview()}
        </PxReportWorkflowStep>
      );
    }

    if (step.id === "analysis") {
      return (
        <AnalysisWorkflowStep onCollapseChange={handleWorkflowResultCollapseChange("analysis-reports")}>
          {renderAnalysisReportPreview()}
        </AnalysisWorkflowStep>
      );
    }

    if (step.id === "code-optimize") {
      return (
        <CodeOptimizeWorkflowStep
          downloadUrl={diffResultDownloadUrl}
          getDownloadFileName={getDownloadFileName}
          onCollapseChange={handleWorkflowResultCollapseChange("diffs")}
          onDownload={(event) =>
            void handleWorkflowDownload("diffs", diffResultDownloadUrl, "code-diff.diff", event)
          }
        >
          {renderCodeOptimizeDiffPreview()}
        </CodeOptimizeWorkflowStep>
      );
    }

    if (step.id === "ab-test") {
      return (
        <AbTestWorkflowStep
          downloadUrl={abtestResultDownloadUrl}
          fallbackDownloadUrl={abComparisonDownloadUrl}
          getDownloadFileName={getDownloadFileName}
          onCollapseChange={handleWorkflowResultCollapseChange("abtests")}
          onDownload={(event) =>
            void handleWorkflowDownload("abtests", abComparisonDownloadUrl, "ab-test-comparison.json", event)
          }
        >
          {renderAbTestPreview()}
        </AbTestWorkflowStep>
      );
    }

    return null;
  };

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
          onDeleteHistorySession,
        },
        workbenchViewProps: {
          workflowSteps,
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
          isAutoInteractionActive,
          isSendingMessage,
          displayedCheckpointWaitPrompt,
          prompt,
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
          renderStepRuntimeSummary,
          renderStepChildren,
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
          onDeleteHistorySession,
          onCreateSession,
          onOpenHistorySessionModal,
          onPromptChange: setPrompt,
          onSend: (command) => void onSend(command),
          onCloseHistorySessionModal: () => setIsHistorySessionModalOpen(false),
          onRetryThreadHistoryList: () => void fetchThreadHistoryList(),
          onCancelCreateSession,
          onConfirmCreateSession,
        },
      })}
    </>
  );
}
