import { useMemo } from "react";
import { Button, Empty, Spin, Tag, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { TraceDetailObservation } from "../TraceObservationView";
import type { AbCaseRow, AbCompareObservation, FlowRow } from "./types";
import {
  buildFlowRows,
  formatDuration,
  getAbMaxScore,
  getAbReturnedDocs,
  getDetailRoundCount,
  getSearchNode,
  getShortTraceId,
  getStatusColor,
  getTraceMode,
} from "./traceUtils";

const { Text, Title } = Typography;

function AbSummaryStrip({ observation }: { observation: AbCompareObservation }) {
  const { t } = useTranslation();
  const a = observation.a;
  const b = observation.b;
  return (
    <div className="self-evolution-abtest-summary-strip" aria-label={t("selfEvolutionRun.observation.abTraceSummaryAria")}>
      <span className="self-evolution-abtest-summary-group">
        <span className="is-side">A</span>
        <span><strong>{getDetailRoundCount(a)}</strong><em>{t("selfEvolutionRun.observation.decisionRounds")}</em></span>
        <span><strong>{a.summary.toolCallCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.toolCalls")}</em></span>
        <span><strong>{a.summary.retrievalCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.kbRetrievals")}</em></span>
        <strong>{formatDuration(a.summary.latencyMs)}</strong>
      </span>
      <i>→</i>
      <span className="self-evolution-abtest-summary-group">
        <span className="is-side is-b">B</span>
        <span><strong>{getDetailRoundCount(b)}</strong><em>{t("selfEvolutionRun.observation.decisionRounds")}</em></span>
        <span><strong>{b.summary.toolCallCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.toolCalls")}</em></span>
        <span><strong>{b.summary.retrievalCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.kbRetrievals")}</em></span>
        <strong>{formatDuration(b.summary.latencyMs)}</strong>
      </span>
    </div>
  );
}

function AbTraceStep({ row }: { row: FlowRow }) {
  const isSearch = row.node.name.includes("kb_search") || row.node.type === "retriever";
  const tone = isSearch ? "warning" : row.tone;
  return (
    <article className={`self-evolution-abtest-trace-step is-${tone}`}>
      <div className="self-evolution-abtest-step-head">
        <strong>{row.title}</strong>
        <span>{row.duration}</span>
        <Tag color={tone === "warning" ? "orange" : getStatusColor(row.node.status)}>{tone === "warning" ? "warning" : row.node.status}</Tag>
      </div>
      <p>{row.desc}</p>
      {isSearch && (
        <div className="self-evolution-abtest-step-fields">
          <span>returned_docs: <strong>{getAbReturnedDocs(row.node)}</strong></span>
          <span>max_score: <strong>{getAbMaxScore(row.node)?.toFixed(2) ?? "-"}</strong></span>
        </div>
      )}
    </article>
  );
}

function AbTraceColumn({
  title,
  variant,
  detail,
  selectedCase,
}: {
  title: string;
  variant: "a" | "b";
  detail: TraceDetailObservation;
  selectedCase: AbCaseRow;
}) {
  const { t } = useTranslation();
  const rowsByRound = useMemo(() => {
    const grouped = new Map<number, FlowRow[]>();
    buildFlowRows(t, detail).forEach((row) => {
      grouped.set(row.round, [...(grouped.get(row.round) || []), row]);
    });
    return Array.from(grouped.entries()).slice(0, 4);
  }, [detail, t]);
  const score = variant === "a" ? selectedCase.aScore : selectedCase.bScore;

  return (
    <section className={`self-evolution-abtest-trace-column is-${variant}`} aria-label={`${title} Trace`}>
      <div className="self-evolution-abtest-column-title">
        <Text strong>{title}</Text>
        <span>{getTraceMode(detail)}</span>
      </div>
      <div className="self-evolution-abtest-algo-grid">
        <span><em>{t("selfEvolutionRun.observation.algoVersion")}</em><strong>{variant === "a" ? "baseline-v1" : "candidate-v2"}</strong></span>
        <span><em>Trace ID</em><strong>{getShortTraceId(detail.traceId)}</strong></span>
        <span><em>Score</em><strong>{score.toFixed(2)}</strong></span>
        <span><em>Latency</em><strong>{formatDuration(detail.summary.latencyMs)}</strong></span>
      </div>
      <div className="self-evolution-abtest-round-list">
        {rowsByRound.map(([round, rows]) => (
          <div key={round} className="self-evolution-abtest-round">
            <div className="self-evolution-abtest-round-head">
              <strong>{`Round ${round}`}</strong>
              <span>{formatDuration(rows.reduce((total, row) => total + (row.node.latencyMs || 0), 0))}</span>
            </div>
            {rows.slice(0, 3).map((row) => (
              <AbTraceStep key={`${variant}-${row.key}`} row={row} />
            ))}
          </div>
        ))}
      </div>
      <div className={`self-evolution-abtest-column-note is-${variant === "a" ? "danger" : "warning"}`}>
        {variant === "a"
          ? t("selfEvolutionRun.observation.abColumnNoteA")
          : t("selfEvolutionRun.observation.abColumnNoteB")}
      </div>
    </section>
  );
}

function AbDiffPanel({
  observation,
}: {
  observation: AbCompareObservation;
}) {
  const { t } = useTranslation();
  const aNode = getSearchNode(observation.a);
  const bNode = getSearchNode(observation.b);
  const aScore = getAbMaxScore(aNode);
  const bScore = getAbMaxScore(bNode);
  return (
    <section className="self-evolution-abtest-diff-panel" aria-label={t("selfEvolutionRun.observation.abDiffPanelAria")}>
      <Text strong>{t("selfEvolutionRun.observation.abDiffPanelTitle")}</Text>
      <div className="self-evolution-abtest-diff-grid">
        <article>
          <Text strong>{t("selfEvolutionRun.observation.abDiffOutputA")}</Text>
          <dl>
            <dt>returned_docs</dt><dd>{getAbReturnedDocs(aNode)}</dd>
            <dt>max_score</dt><dd>{aScore?.toFixed(2) ?? "-"}</dd>
            <dt>{t("selfEvolutionRun.observation.abDiffJudge")}</dt><dd className="is-bad">{t("selfEvolutionRun.observation.abDiffJudgeABad")}</dd>
          </dl>
        </article>
        <article>
          <Text strong>{t("selfEvolutionRun.observation.abDiffOutputB")}</Text>
          <dl>
            <dt>returned_docs</dt><dd>{getAbReturnedDocs(bNode)}</dd>
            <dt>max_score</dt><dd>{bScore?.toFixed(2) ?? "-"}</dd>
            <dt>{t("selfEvolutionRun.observation.abDiffJudge")}</dt><dd className="is-warn">{t("selfEvolutionRun.observation.abDiffJudgeBWarn")}</dd>
          </dl>
        </article>
      </div>
    </section>
  );
}

export function AbTraceComparePanel({
  observation,
  selectedCase,
  abtestId,
  loading,
  error,
  onRetry,
}: {
  observation?: AbCompareObservation;
  selectedCase: AbCaseRow;
  abtestId?: string;
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
}) {
  const { t } = useTranslation();
  const reportIdLabel = abtestId && abtestId.length > 16 ? `${abtestId.slice(0, 8)}...${abtestId.slice(-4)}` : abtestId || "abtest";

  if (loading) {
    return (
      <section className="self-evolution-abtest-compare-card" aria-label={t("selfEvolutionRun.observation.abComparePanelAria")}>
        <div className="self-evolution-observation-page-loading">
          <Spin />
          <Text>{t("selfEvolutionRun.observation.loadingAbTrace")}</Text>
        </div>
      </section>
    );
  }

  if (error || !observation) {
    return (
      <section className="self-evolution-abtest-compare-card" aria-label={t("selfEvolutionRun.observation.abComparePanelAria")}>
        <Empty
          description={error || t("selfEvolutionRun.observation.emptyObservation")}
          image={Empty.PRESENTED_IMAGE_SIMPLE}
        />
        {onRetry ? <Button onClick={onRetry}>{t("selfEvolutionRun.observation.retry")}</Button> : null}
      </section>
    );
  }

  return (
    <section className="self-evolution-abtest-compare-card" aria-label={t("selfEvolutionRun.observation.abComparePanelAria")}>
      <div className="self-evolution-abtest-compare-head">
        <Title level={3}>{t("selfEvolutionRun.observation.abComparePanelTitle", { caseId: selectedCase.caseId })}</Title>
        <div>
          <Tag>{`Query: ${selectedCase.query}`}</Tag>
          <Tag>{`Report ID: ${reportIdLabel}`}</Tag>
          <Tag color="orange">{t("selfEvolutionRun.observation.abStatusNeedsAnalysis")}</Tag>
        </div>
      </div>
      <AbSummaryStrip observation={observation} />
      <div className="self-evolution-abtest-trace-columns">
        <AbTraceColumn title={t("selfEvolutionRun.observation.abBaselineTitle")} variant="a" detail={observation.a} selectedCase={selectedCase} />
        <AbTraceColumn title={t("selfEvolutionRun.observation.abOptimizedTitle")} variant="b" detail={observation.b} selectedCase={selectedCase} />
      </div>
      <AbDiffPanel observation={observation} />
      <div className="self-evolution-abtest-conclusion">
        <Text strong>{t("selfEvolutionRun.observation.abConclusionLabel")}</Text>
        <span>{t("selfEvolutionRun.observation.abConclusionText")}</span>
      </div>
    </section>
  );
}
