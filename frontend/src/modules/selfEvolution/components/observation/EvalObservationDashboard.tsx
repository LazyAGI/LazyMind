import { useEffect, useMemo, useState } from "react";
import { Alert, Empty, Spin, Tag } from "antd";
import { useTranslation } from "react-i18next";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  EVO_API_BASE,
  createCoreAgentGeneratedApiClient,
  isCanceledRequest,
  isEmptyResultPayload,
} from "../../shared";
import { normalizeTraceObservation } from "../TraceObservationView";
import type { EvalBadcaseListState } from "./types";
import { EVAL_BADCASE_PAGE_SIZE, normalizeBadcaseRows, normalizeEvalReportSummary } from "./dataUtils";
import { getPrimaryObservation } from "./traceUtils";
import { ObservationHeaderControls } from "./ObservationHeaderControls";
import { EvalReportPanel } from "./EvalReportPanel";
import { EvalTracePanel } from "./EvalTracePanel";

export function EvalObservationDashboard({
  data,
  notice,
  threadId,
  onBack,
  isMenuCollapsed,
  toggleMenu,
}: {
  data: unknown;
  notice?: string;
  threadId?: string;
  onBack: () => void;
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
}) {
  const { t } = useTranslation();
  const summary = useMemo(() => normalizeEvalReportSummary(data), [data]);
  const [badcaseReloadToken, setBadcaseReloadToken] = useState(0);
  const [badcaseState, setBadcaseState] = useState<EvalBadcaseListState>({
    loading: false,
    loaded: false,
  });
  const rows = useMemo(() => normalizeBadcaseRows(t, badcaseState.data), [badcaseState.data, t]);
  const [selectedCaseId, setSelectedCaseId] = useState(rows[0]?.caseId || "");
  const [traceState, setTraceState] = useState<{
    loading: boolean;
    data?: unknown;
    error?: string;
    traceId?: string;
  }>({ loading: false });
  const selectedRow = rows.find((item) => item.caseId === selectedCaseId) || rows[0];
  const selectedObservation = useMemo(() => {
    return normalizeTraceObservation(traceState.data) || normalizeTraceObservation(selectedRow?.tracePayload);
  }, [selectedRow, traceState.data]);
  const detail = getPrimaryObservation(selectedObservation);

  useEffect(() => {
    if (!threadId || !summary.reportId || summary.reportId === "-") {
      setBadcaseState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setBadcaseState((prev) => ({
      reportId: summary.reportId,
      loading: true,
      loaded: prev.reportId === summary.reportId ? prev.loaded : false,
      data: prev.reportId === summary.reportId ? prev.data : undefined,
      error: undefined,
    }));

    createCoreAgentGeneratedApiClient()
      .apiCoreAgentThreadsThreadIdResultsEvalReportsReportIdBadCasesGet(
        {
          threadId,
          reportId: summary.reportId,
          pageSize: EVAL_BADCASE_PAGE_SIZE,
        },
        { signal: controller.signal },
      )
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setBadcaseState({
          reportId: summary.reportId,
          loading: false,
          loaded: true,
          data: response.data,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setBadcaseState((prev) => ({
          ...prev,
          reportId: summary.reportId,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.badcaseLoadFailed")),
        }));
      });

    return () => {
      controller.abort();
    };
  }, [badcaseReloadToken, summary.reportId, threadId]);

  useEffect(() => {
    if (!rows.some((item) => item.caseId === selectedCaseId)) {
      setSelectedCaseId(rows[0]?.caseId || "");
    }
  }, [rows, selectedCaseId]);

  useEffect(() => {
    const traceId = selectedRow?.traceId;
    if (!threadId || !traceId || traceId === "-") {
      setTraceState({ loading: false, data: undefined, error: traceId ? undefined : t("selfEvolutionRun.observation.noTraceId") });
      return;
    }

    const controller = new AbortController();
    setTraceState({ loading: true, data: undefined, error: undefined, traceId });

    axiosInstance
      .get(`${EVO_API_BASE}/threads/${encodeURIComponent(threadId)}/results/traces/${encodeURIComponent(traceId)}`, {
        signal: controller.signal,
      })
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        if (isEmptyResultPayload(response.data)) {
          setTraceState({
            loading: false,
            data: undefined,
            error: t("selfEvolutionRun.observation.traceNoData"),
            traceId,
          });
          return;
        }
        setTraceState({ loading: false, data: response.data, error: undefined, traceId });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setTraceState({
          loading: false,
          data: undefined,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.observationDetailLoadFailed")),
          traceId,
        });
      });

    return () => {
      controller.abort();
    };
  }, [selectedRow?.traceId, threadId]);

  return (
    <div className="self-evolution-eval-dashboard">
      <header className="self-evolution-eval-dashboard-head">
        <ObservationHeaderControls isMenuCollapsed={isMenuCollapsed} toggleMenu={toggleMenu} onBack={onBack} />
        <div className="self-evolution-eval-dashboard-head-right">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
        </div>
      </header>
      {notice && <Alert type="warning" showIcon message={notice} />}
      <div className="self-evolution-eval-dashboard-grid">
        <EvalReportPanel
          summary={summary}
          rows={rows}
          rowsError={badcaseState.error}
          rowsLoading={badcaseState.loading}
          selectedCaseId={selectedCaseId}
          onSelectCase={setSelectedCaseId}
          onReloadRows={() => setBadcaseReloadToken((prev) => prev + 1)}
        />
        {selectedRow ? (
          traceState.loading ? (
            <section className="self-evolution-eval-trace-card" aria-label={t("selfEvolutionRun.observation.agenticTraceCardAria")}>
              <Spin />
            </section>
          ) : detail ? (
            <EvalTracePanel detail={detail} selectedRow={selectedRow} />
          ) : (
            <section className="self-evolution-eval-trace-card">
              <Empty description={traceState.error || t("selfEvolutionRun.observation.emptyObservation")} />
            </section>
          )
        ) : (
          <section className="self-evolution-eval-trace-card">
            <Empty description={t("selfEvolutionRun.observation.emptyObservation")} />
          </section>
        )}
      </div>
    </div>
  );
}
