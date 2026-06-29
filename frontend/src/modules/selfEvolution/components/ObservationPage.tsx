import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button, Empty, Spin, Tag, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { useNavigate, useOutletContext, useParams } from "react-router-dom";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  AGENT_API_BASE,
  isCanceledRequest,
  isEmptyResultPayload,
  stringifyResultPayload,
} from "../shared";
import "../index.scss";
import type {
  ObservationPageLayoutContext,
  ObservationPageState,
  ObservationRouteParams,
} from "./observation/types";
import { normalizeObservationKind } from "./observation/dataUtils";
import { ObservationHeaderControls } from "./observation/ObservationHeaderControls";
import { EvalObservationDashboard } from "./observation/EvalObservationDashboard";
import { AbtestObservationDashboard } from "./observation/AbtestObservationDashboard";

const { Paragraph, Text, Title } = Typography;

export function SelfEvolutionObservationPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { threadId, kind } = useParams<ObservationRouteParams>();
  const { isMenuCollapsed, toggleMenu } = useOutletContext<ObservationPageLayoutContext>();
  const resultKind = normalizeObservationKind(kind);
  const [reloadToken, setReloadToken] = useState(0);
  const [state, setState] = useState<ObservationPageState>({ loading: false, loaded: false });
  const resultUrl = threadId && resultKind
    ? `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/${resultKind}`
    : "";

  useEffect(() => {
    if (!threadId || !resultKind) {
      setState({ loading: false, loaded: true, error: t("selfEvolutionRun.observation.routeError") });
      return;
    }

    const controller = new AbortController();
    setState((prev) => ({ ...prev, loading: true, error: undefined }));

    axiosInstance
      .get(resultUrl, { signal: controller.signal })
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        if (isEmptyResultPayload(response.data)) {
          setState({
            loading: false,
            loaded: true,
            data: undefined,
            notice: resultKind === "eval-reports"
              ? t("selfEvolutionRun.observation.noEvalCsvData")
              : t("selfEvolutionRun.observation.noObservationData"),
          });
          return;
        }
        setState({ loading: false, loaded: true, data: response.data });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        const errorMessage = getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.observationLoadFailed"));
        setState({
          loading: false,
          loaded: true,
          data: undefined,
          notice: resultKind === "eval-reports"
            ? errorMessage
            : t("selfEvolutionRun.observation.observationUnavailable", { error: errorMessage }),
        });
      });

    return () => {
      controller.abort();
    };
  }, [reloadToken, resultKind, resultUrl, threadId]);

  const isEmpty = state.loaded && !state.loading && !state.error && isEmptyResultPayload(state.data);
  const backToDetail = () => {
    if (threadId) {
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      return;
    }
    navigate("/self-evolution");
  };
  const reload = () => setReloadToken((prev) => prev + 1);

  if (resultKind === "eval-reports" && (state.data || state.loading)) {
    return (
      <EvalObservationDashboard
        data={state.data}
        notice={state.notice}
        threadId={threadId}
        onBack={backToDetail}
        isMenuCollapsed={isMenuCollapsed}
        toggleMenu={toggleMenu}
      />
    );
  }

  if (resultKind === "abtests" && (state.data || state.loading)) {
    return (
      <AbtestObservationDashboard
        data={state.data}
        notice={state.notice}
        loading={state.loading}
        threadId={threadId}
        onBack={backToDetail}
        onReload={reload}
        isMenuCollapsed={isMenuCollapsed}
        toggleMenu={toggleMenu}
      />
    );
  }

  return (
    <div className="self-evolution-observation-page">
      <header className="self-evolution-observation-page-head">
        <div className="self-evolution-observation-page-title">
          <ObservationHeaderControls isMenuCollapsed={isMenuCollapsed} toggleMenu={toggleMenu} onBack={backToDetail} />
          <div>
            <Title level={3}>{t("selfEvolutionRun.observation.pageTitle")}</Title>
            <Paragraph>{t("selfEvolutionRun.observation.pageDesc")}</Paragraph>
          </div>
        </div>
        <div className="self-evolution-observation-page-meta">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          {resultKind && <Tag color="blue">{resultKind}</Tag>}
          <Button icon={<ReloadOutlined />} loading={state.loading} onClick={reload}>{t("selfEvolutionRun.observation.refresh")}</Button>
        </div>
      </header>
      <main className="self-evolution-observation-page-body" aria-live="polite">
        {state.notice && !state.loading && <Alert type="warning" showIcon message={state.notice} />}
        {!resultKind ? (
          <Alert type="warning" showIcon message={t("selfEvolutionRun.observation.unknownObservationType")} description={t("selfEvolutionRun.observation.unknownObservationTypeDesc", { kind: kind || "-" })} />
        ) : state.loading && !state.loaded ? (
          <div className="self-evolution-observation-page-loading">
            <Spin />
            <Text>{t("selfEvolutionRun.observation.loadingData")}</Text>
          </div>
        ) : state.error ? (
          <Alert
            type="error"
            showIcon
            message={t("selfEvolutionRun.observation.observationLoadFailedTitle")}
            description={state.error}
            action={<Button size="small" onClick={reload}>{t("selfEvolutionRun.observation.retry")}</Button>}
          />
        ) : isEmpty ? (
          <Empty description={t("selfEvolutionRun.observation.emptyObservations")} />
        ) : (
          <section className="self-evolution-observation-json-card" aria-label={t("selfEvolutionRun.observation.rawDataAria")}>
            <div className="self-evolution-observation-data-head">
              <div>
                <Text strong>{t("selfEvolutionRun.observation.rawData")}</Text>
                <span>{t("selfEvolutionRun.observation.rawDataNote")}</span>
              </div>
            </div>
            <pre>{stringifyResultPayload(state.data)}</pre>
          </section>
        )}
      </main>
    </div>
  );
}
