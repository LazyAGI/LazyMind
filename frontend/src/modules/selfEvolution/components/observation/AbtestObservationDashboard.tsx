import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Spin, Tag, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  AGENT_API_BASE,
  buildAbSummaryFromComparisonArtifact,
  buildAbSummaryReports,
  buildAbCaseTraceMapFromComparisonArtifact,
  buildAbCaseDetailItemFromComparisonCase,
  fetchThreadGateContent,
  isCanceledRequest,
  parseAbtestComparisonArtifact,
  stringifyResultPayload,
} from "../../shared";
import { normalizeTraceObservation } from "../TraceObservationView";
import type {
  AbCaseListState,
  AbTraceCompareState,
  EvalReportsTraceState,
} from "./types";
import {
  AB_CASE_DETAIL_PAGE_SIZE,
  buildAbCaseTraceIdMap,
  findAbCaseDetailItem,
  normalizeAbCaseRows,
  resolveAbtestIdFromPayload,
  resolveCaseTraceIds,
} from "./dataUtils";
import { ObservationHeaderControls } from "./ObservationHeaderControls";
import { AbReportPanel } from "./AbReportPanel";
import { AbTraceComparePanel } from "./AbTraceComparePanel";

const { Text } = Typography;

export function AbtestObservationDashboard({
  data,
  notice,
  loading,
  threadId,
  onBack,
  onReload,
  isMenuCollapsed,
  toggleMenu,
}: {
  data: unknown;
  notice?: string;
  loading: boolean;
  threadId?: string;
  onBack: () => void;
  onReload: () => void;
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
}) {
  const { t } = useTranslation();
  const abtestComparisonArtifact = useMemo(
    () => parseAbtestComparisonArtifact(data),
    [data],
  );
  const abSummary = useMemo(() => {
    if (abtestComparisonArtifact) {
      return buildAbSummaryFromComparisonArtifact(abtestComparisonArtifact);
    }
    return buildAbSummaryReports(data)[0];
  }, [abtestComparisonArtifact, data]);
  const abtestId = useMemo(() => resolveAbtestIdFromPayload(data), [data]);
  const comparisonTraceMap = useMemo(
    () => buildAbCaseTraceMapFromComparisonArtifact(abtestComparisonArtifact),
    [abtestComparisonArtifact],
  );
  const [caseReloadToken, setCaseReloadToken] = useState(0);
  const [traceReloadToken, setTraceReloadToken] = useState(0);
  const [caseState, setCaseState] = useState<AbCaseListState>({
    loading: false,
    loaded: false,
  });
  const [traceCompareState, setTraceCompareState] =
    useState<AbTraceCompareState>({
      loading: false,
      loaded: false,
    });
  const [evalReportsState, setEvalReportsState] =
    useState<EvalReportsTraceState>({
      loading: false,
      loaded: false,
    });
  const traceIdMap = useMemo(() => {
    const merged = buildAbCaseTraceIdMap(evalReportsState.data);
    comparisonTraceMap.forEach((value, key) => {
      merged.set(key, { ...merged.get(key), ...value });
    });
    return merged;
  }, [comparisonTraceMap, evalReportsState.data]);
  const hasInlineTraceMap = comparisonTraceMap.size > 0;
  const rows = useMemo(() => {
    if (abtestComparisonArtifact) {
      return normalizeAbCaseRows(t, data);
    }
    if (caseState.loaded && caseState.data) {
      return normalizeAbCaseRows(t, caseState.data);
    }
    return normalizeAbCaseRows(t, data);
  }, [abtestComparisonArtifact, caseState.data, caseState.loaded, data, t]);
  const selectedCaseObservation = useMemo(() => {
    const observation = normalizeTraceObservation(traceCompareState.data);
    return observation?.kind === "compare" ? observation : undefined;
  }, [traceCompareState.data]);
  const [selectedCaseId, setSelectedCaseId] = useState(rows[0]?.caseId || "");
  const selectedCase =
    rows.find((row) => row.caseId === selectedCaseId) || rows[0];
  const selectedCaseItem = useMemo(() => {
    if (selectedCase) {
      const fromApi = findAbCaseDetailItem(caseState.data, selectedCase.caseId);
      if (fromApi) {
        return fromApi;
      }
    }
    const comparisonCase = abtestComparisonArtifact?.caseRows.find(
      (row) => row.caseId === selectedCase?.caseId,
    );
    if (comparisonCase) {
      return buildAbCaseDetailItemFromComparisonCase(comparisonCase);
    }
    return undefined;
  }, [abtestComparisonArtifact, caseState.data, selectedCase]);

  useEffect(() => {
    if (!threadId) {
      setEvalReportsState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setEvalReportsState((prev) => ({ ...prev, loading: true }));

    fetchThreadGateContent(threadId, "eval-reports", { signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) {
          return;
        }
        setEvalReportsState({
          loading: false,
          loaded: true,
          data,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setEvalReportsState({ loading: false, loaded: true, data: undefined });
      });

    return () => {
      controller.abort();
    };
  }, [threadId]);

  useEffect(() => {
    if (abtestComparisonArtifact?.caseRows.length) {
      setCaseState({
        abtestId,
        loading: false,
        loaded: true,
        totalSize: abtestComparisonArtifact.caseRows.length,
      });
      return;
    }
    if (!threadId || !abtestId) {
      setCaseState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setCaseState((prev) => ({
      abtestId,
      loading: true,
      loaded: prev.abtestId === abtestId ? prev.loaded : false,
      data: prev.abtestId === abtestId ? prev.data : undefined,
      error: undefined,
    }));

    axiosInstance
      .get(
        `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/abtests/${encodeURIComponent(abtestId)}/case-details`,
        {
          params: { page_size: AB_CASE_DETAIL_PAGE_SIZE },
          signal: controller.signal,
        },
      )
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setCaseState({
          abtestId,
          loading: false,
          loaded: true,
          data: response.data,
          totalSize: response.data.total_size,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setCaseState((prev) => ({
          ...prev,
          abtestId,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(
            error,
            t("selfEvolutionRun.observation.abCaseDetailLoadFailed"),
          ),
        }));
      });

    return () => {
      controller.abort();
    };
  }, [abtestComparisonArtifact, abtestId, caseReloadToken, threadId, t]);

  useEffect(() => {
    if (!threadId || !selectedCase?.caseId) {
      setTraceCompareState({
        loading: false,
        loaded: false,
        error: t("selfEvolutionRun.observation.noTraceId"),
      });
      return;
    }
    if (!hasInlineTraceMap && (evalReportsState.loading || !evalReportsState.loaded)) {
      setTraceCompareState({
        caseId: selectedCase.caseId,
        loading: true,
        loaded: false,
      });
      return;
    }

    const { a: aTraceId, b: bTraceId } = resolveCaseTraceIds(
      selectedCaseItem,
      selectedCase.caseId,
      traceIdMap,
    );
    if (!aTraceId || !bTraceId || aTraceId === "-" || bTraceId === "-") {
      setTraceCompareState({
        caseId: selectedCase.caseId,
        loading: false,
        loaded: true,
        error: t("selfEvolutionRun.observation.abTraceIdsMissing"),
        aTraceId,
        bTraceId,
      });
      return;
    }

    const controller = new AbortController();
    setTraceCompareState({
      caseId: selectedCase.caseId,
      loading: true,
      loaded: false,
      data: undefined,
      error: undefined,
      aTraceId,
      bTraceId,
    });

    axiosInstance
      .get(
        `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/traces:compare`,
        {
          params: { a: aTraceId, b: bTraceId },
          signal: controller.signal,
        },
      )
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setTraceCompareState({
          caseId: selectedCase.caseId,
          loading: false,
          loaded: true,
          data: response.data,
          aTraceId,
          bTraceId,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setTraceCompareState({
          caseId: selectedCase.caseId,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(
            error,
            t("selfEvolutionRun.observation.abTraceCompareLoadFailed"),
          ),
          aTraceId,
          bTraceId,
        });
      });

    return () => {
      controller.abort();
    };
  }, [
    evalReportsState.loaded,
    evalReportsState.loading,
    hasInlineTraceMap,
    selectedCase?.caseId,
    selectedCaseItem,
    threadId,
    traceIdMap,
    traceReloadToken,
    t,
  ]);

  useEffect(() => {
    if (!rows.some((row) => row.caseId === selectedCaseId)) {
      setSelectedCaseId(rows[0]?.caseId || "");
    }
  }, [rows, selectedCaseId]);

  return (
    <div className="self-evolution-abtest-dashboard">
      <header className="self-evolution-eval-dashboard-head">
        <ObservationHeaderControls
          isMenuCollapsed={isMenuCollapsed}
          toggleMenu={toggleMenu}
          onBack={onBack}
        />
        <div className="self-evolution-eval-dashboard-head-right">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          <Button
            icon={<ReloadOutlined />}
            loading={loading}
            onClick={onReload}
          >
            {t("selfEvolutionRun.observation.refresh")}
          </Button>
        </div>
      </header>
      {notice && !loading && <Alert type="warning" showIcon message={notice} />}
      {loading && !data ? (
        <div className="self-evolution-observation-page-loading">
          <Spin />
          <Text>{t("selfEvolutionRun.observation.loadingAbData")}</Text>
        </div>
      ) : abSummary && rows.length > 0 ? (
        <div className="self-evolution-abtest-dashboard-grid">
          <AbReportPanel
            summary={abSummary}
            comparisonArtifact={abtestComparisonArtifact}
            rows={rows}
            rowsError={caseState.error}
            rowsLoading={caseState.loading}
            totalSize={caseState.totalSize}
            selectedCaseId={selectedCase.caseId}
            onSelectCase={setSelectedCaseId}
            onReloadRows={() => setCaseReloadToken((prev) => prev + 1)}
          />
          <AbTraceComparePanel
            observation={selectedCaseObservation}
            selectedCase={selectedCase}
            abtestId={abtestId || abSummary?.id}
            loading={traceCompareState.loading}
            error={traceCompareState.error}
            onRetry={() => setTraceReloadToken((prev) => prev + 1)}
          />
        </div>
      ) : (
        <section
          className="self-evolution-observation-json-card"
          aria-label={t("selfEvolutionRun.observation.rawAbDataAria")}
        >
          <div className="self-evolution-observation-data-head">
            <div>
              <Text strong>{t("selfEvolutionRun.observation.rawData")}</Text>
              <span>{t("selfEvolutionRun.observation.rawAbDataNote")}</span>
            </div>
          </div>
          <pre>{stringifyResultPayload(data)}</pre>
        </section>
      )}
    </div>
  );
}
