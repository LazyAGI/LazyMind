import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Spin, Tag, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  AGENT_API_BASE,
  buildAbSummaryReports,
  createCoreAgentGeneratedApiClient,
  isCanceledRequest,
  stringifyResultPayload,
} from "../../shared";
import { normalizeTraceObservation } from "../TraceObservationView";
import type { AbCaseListState, AbTraceCompareState, EvalReportsTraceState } from "./types";
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
  const abSummary = useMemo(() => buildAbSummaryReports(data)[0], [data]);
  const abtestId = useMemo(() => resolveAbtestIdFromPayload(data), [data]);
  const [caseReloadToken, setCaseReloadToken] = useState(0);
  const [traceReloadToken, setTraceReloadToken] = useState(0);
  const [caseState, setCaseState] = useState<AbCaseListState>({
    loading: false,
    loaded: false,
  });
  const [traceCompareState, setTraceCompareState] = useState<AbTraceCompareState>({
    loading: false,
    loaded: false,
  });
  const [evalReportsState, setEvalReportsState] = useState<EvalReportsTraceState>({
    loading: false,
    loaded: false,
  });
  const traceIdMap = useMemo(() => buildAbCaseTraceIdMap(evalReportsState.data), [evalReportsState.data]);
  const rows = useMemo(() => {
    if (caseState.loaded && caseState.data) {
      return normalizeAbCaseRows(t, caseState.data);
    }
    return normalizeAbCaseRows(t, data);
  }, [caseState.data, caseState.loaded, data, t]);
  const selectedCaseObservation = useMemo(() => {
    const observation = normalizeTraceObservation(traceCompareState.data);
    return observation?.kind === "compare" ? observation : undefined;
  }, [traceCompareState.data]);
  const [selectedCaseId, setSelectedCaseId] = useState(rows[0]?.caseId || "");
  const selectedCase = rows.find((row) => row.caseId === selectedCaseId) || rows[0];
  const selectedCaseItem = useMemo(
    () => (selectedCase ? findAbCaseDetailItem(caseState.data, selectedCase.caseId) : undefined),
    [caseState.data, selectedCase],
  );

  useEffect(() => {
    if (!threadId) {
      setEvalReportsState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setEvalReportsState((prev) => ({ ...prev, loading: true }));

    axiosInstance
      .get(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/eval-reports`, {
        signal: controller.signal,
      })
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setEvalReportsState({ loading: false, loaded: true, data: response.data });
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

    createCoreAgentGeneratedApiClient()
      .apiCoreAgentThreadsThreadIdResultsAbtestsAbtestIdCaseDetailsGet(
        {
          threadId,
          abtestId,
          pageSize: AB_CASE_DETAIL_PAGE_SIZE,
        },
        { signal: controller.signal },
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
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.abCaseDetailLoadFailed")),
        }));
      });

    return () => {
      controller.abort();
    };
  }, [abtestId, caseReloadToken, threadId, t]);

  useEffect(() => {
    if (!threadId || !selectedCase?.caseId) {
      setTraceCompareState({ loading: false, loaded: false, error: t("selfEvolutionRun.observation.noTraceId") });
      return;
    }
    if (evalReportsState.loading || !evalReportsState.loaded) {
      setTraceCompareState({
        caseId: selectedCase.caseId,
        loading: true,
        loaded: false,
      });
      return;
    }

    const { a: aTraceId, b: bTraceId } = resolveCaseTraceIds(selectedCaseItem, selectedCase.caseId, traceIdMap);
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

    createCoreAgentGeneratedApiClient()
      .apiCoreAgentThreadsThreadIdResultsTracesCompareGet(
        {
          threadId,
          a: aTraceId,
          b: bTraceId,
        },
        { signal: controller.signal },
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
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.abTraceCompareLoadFailed")),
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
        <ObservationHeaderControls isMenuCollapsed={isMenuCollapsed} toggleMenu={toggleMenu} onBack={onBack} />
        <div className="self-evolution-eval-dashboard-head-right">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          <Button icon={<ReloadOutlined />} loading={loading} onClick={onReload}>{t("selfEvolutionRun.observation.refresh")}</Button>
        </div>
      </header>
      {notice && !loading && <Alert type="warning" showIcon message={notice} />}
      {loading && !data ? (
        <div className="self-evolution-observation-page-loading">
          <Spin />
          <Text>{t("selfEvolutionRun.observation.loadingAbData")}</Text>
        </div>
      ) : selectedCase ? (
        <div className="self-evolution-abtest-dashboard-grid">
          <AbReportPanel
            summary={abSummary}
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
        <section className="self-evolution-observation-json-card" aria-label={t("selfEvolutionRun.observation.rawAbDataAria")}>
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
