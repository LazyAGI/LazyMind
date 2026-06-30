import { useEffect, useMemo, useState } from "react";
import { Tag, Typography } from "antd";
import { CheckCircleOutlined, FileSearchOutlined, FileTextOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { stringifyResultPayload } from "../../shared";
import type { TraceDetailObservation } from "../TraceObservationView";
import type { CsvBadcaseRow, FlowRow, TFunction, TraceNode } from "./types";
import {
  buildFlowRows,
  formatDuration,
  getDisplayText,
  getNodeDataRecord,
  getNodeTitle,
  getShortTraceId,
  getStatusColor,
  getTraceDocs,
} from "./traceUtils";

const { Paragraph, Text, Title } = Typography;

function renderPayloadBlock(t: TFunction, label: string, payload?: { summary?: string; data?: unknown }) {
  if (!payload?.summary && payload?.data === undefined) {
    return (
      <Paragraph className="self-evolution-eval-empty">
        {t("selfEvolutionRun.observation.noLabelData", { label })}
      </Paragraph>
    );
  }

  return (
    <>
      {payload?.summary && <Paragraph className="self-evolution-eval-payload-summary">{payload.summary}</Paragraph>}
      {payload?.data !== undefined && (
        <details className="self-evolution-eval-payload-json">
          <summary>{t("selfEvolutionRun.observation.viewLabelJson", { label })}</summary>
          <pre>{stringifyResultPayload(payload.data)}</pre>
        </details>
      )}
    </>
  );
}

function renderMetadataTiles(t: TFunction, metadata?: Record<string, unknown>) {
  const entries = Object.entries(metadata || {}).slice(0, 8);
  if (entries.length === 0) {
    return <Paragraph className="self-evolution-eval-empty">{t("selfEvolutionRun.observation.noMetadata")}</Paragraph>;
  }

  return (
    <div className="self-evolution-eval-kv-grid">
      {entries.map(([key, value]) => (
        <span key={key}>
          <em>{key}</em>
          <strong title={getDisplayText(value, 400)}>{getDisplayText(value, 90)}</strong>
        </span>
      ))}
    </div>
  );
}

function TraceMetaCard({ label, value, status }: { label: string; value: string; status?: boolean }) {
  return (
    <article className="self-evolution-eval-trace-meta-card">
      <span>{label}</span>
      {status ? <Tag color={getStatusColor(value)}>{value}</Tag> : <strong>{value}</strong>}
    </article>
  );
}

function TraceFlowPanel({
  detail,
  selectedNodeId,
  onSelectNode,
}: {
  detail: TraceDetailObservation;
  selectedNodeId?: string;
  onSelectNode: (node: TraceNode) => void;
}) {
  const { t } = useTranslation();
  const flowRows = useMemo(() => buildFlowRows(t, detail), [detail, t]);
  const rowsByRound = useMemo(() => {
    const grouped = new Map<number, FlowRow[]>();
    flowRows.forEach((row) => {
      grouped.set(row.round, [...(grouped.get(row.round) || []), row]);
    });
    return Array.from(grouped.entries());
  }, [flowRows]);

  return (
    <section className="self-evolution-eval-flow-panel" aria-label={t("selfEvolutionRun.observation.flowPanelAria")}>
      <div className="self-evolution-eval-panel-title">
        <Text strong>{t("selfEvolutionRun.observation.flowPanelTitle")}</Text>
      </div>
      <div className="self-evolution-eval-flow-list">
        {rowsByRound.map(([round, rows]) => (
          <div key={round} className="self-evolution-eval-flow-round">
            <div className="self-evolution-eval-flow-round-head">
              <CheckCircleOutlined />
              <strong>{`Round ${round}`}</strong>
              <span>{formatDuration(rows.reduce((total, row) => total + (row.node.latencyMs || 0), 0))}</span>
            </div>
            {rows.map((row) => (
              <button
                key={row.key}
                type="button"
                className={`self-evolution-eval-flow-step is-${row.tone}${row.node.id === selectedNodeId ? " is-active" : ""}`}
                onClick={() => onSelectNode(row.node)}
              >
                <span className="self-evolution-eval-flow-dot" />
                <span>
                  <strong>{row.title}</strong>
                  <em>{row.desc}</em>
                </span>
                <i>{row.duration}</i>
              </button>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}

function TraceInspectorPanel({
  selectedRow,
  node,
}: {
  selectedRow: CsvBadcaseRow;
  node?: TraceNode;
}) {
  const { t } = useTranslation();
  const docs = getTraceDocs(node);
  const metadata = node?.metadata || {};
  const inputData = getNodeDataRecord(node?.input);

  if (!node) {
    return (
      <section className="self-evolution-eval-inspector-panel is-empty" aria-label={t("selfEvolutionRun.observation.inspectorPanelAria")}>
        <div className="self-evolution-eval-panel-title">
          <Text strong>{t("selfEvolutionRun.observation.inspectorPanelTitle")}</Text>
        </div>
        <div className="self-evolution-eval-inspector-empty">
          <FileSearchOutlined />
          <Text strong>{t("selfEvolutionRun.observation.inspectorEmptyHint")}</Text>
          <Paragraph>{t("selfEvolutionRun.observation.inspectorEmptyDesc")}</Paragraph>
        </div>
      </section>
    );
  }

  return (
    <section className="self-evolution-eval-inspector-panel" aria-label={t("selfEvolutionRun.observation.inspectorPanelAria")}>
      <div className="self-evolution-eval-panel-title">
        <Text strong>{t("selfEvolutionRun.observation.inspectorNodeTitle", { title: getNodeTitle(node) })}</Text>
        <Tag color={getStatusColor(node.status)}>{node.status}</Tag>
      </div>
      <div className="self-evolution-eval-inspector-body">
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.nodeInfoSection")}</h4>
          <div className="self-evolution-eval-kv-grid">
            <span><em>node_id</em><strong>{getShortTraceId(node.id)}</strong></span>
            <span><em>node_type</em><strong>{node.type}</strong></span>
            <span><em>duration</em><strong>{formatDuration(node.latencyMs)}</strong></span>
            <span><em>status</em><strong>{node.status}</strong></span>
            <span><em>input_kind</em><strong>{node.input?.kind || "-"}</strong></span>
            <span><em>output_kind</em><strong>{node.output?.kind || "-"}</strong></span>
          </div>
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.inputSection")}</h4>
          {renderPayloadBlock(t, t("selfEvolutionRun.observation.inputLabel"), node.input?.summary || inputData !== undefined ? { summary: node.input?.summary, data: inputData } : undefined)}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.outputSection")}</h4>
          {renderPayloadBlock(t, t("selfEvolutionRun.observation.outputLabel"), node.output)}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.retrievedDocsSection")}</h4>
          {docs.length ? (
            docs.map((doc, index) => (
              <article key={doc.key} className="self-evolution-eval-doc-card">
                <FileTextOutlined />
                <div>
                  <strong>{`Doc #${index + 1} ${doc.title}`}</strong>
                  <span>{`score: ${doc.score?.toFixed(2) || "-"} · ${doc.ref}`}</span>
                  <p>{doc.text}</p>
                </div>
                {doc.score !== undefined && <Tag color="blue">{t("selfEvolutionRun.observation.relevanceScore", { score: doc.score.toFixed(2) })}</Tag>}
              </article>
            ))
          ) : (
            <Paragraph className="self-evolution-eval-empty">{t("selfEvolutionRun.observation.noRetrievedDocs")}</Paragraph>
          )}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.metadataSection")}</h4>
          {renderMetadataTiles(t, metadata)}
        </div>
        <div className="self-evolution-eval-inspector-section is-warning">
          <h4>{t("selfEvolutionRun.observation.observationSection")}</h4>
          <p>{selectedRow.failureReason}</p>
        </div>
      </div>
    </section>
  );
}

export function EvalTracePanel({ detail, selectedRow }: { detail: TraceDetailObservation; selectedRow: CsvBadcaseRow }) {
  const { t } = useTranslation();
  const [selectedNode, setSelectedNode] = useState<TraceNode | undefined>();
  useEffect(() => {
    setSelectedNode(undefined);
  }, [detail, selectedRow.caseId]);
  const roundCount = Math.max(detail.root.children.length, detail.summary.roundCount || 0);
  return (
    <section className="self-evolution-eval-trace-card" aria-label={t("selfEvolutionRun.observation.agenticTraceCardAria")}>
      <div className="self-evolution-eval-trace-title">
        <Title level={3}>{t("selfEvolutionRun.observation.agenticTraceCardTitle", { caseId: selectedRow.caseId })}</Title>
      </div>
      <div className="self-evolution-eval-trace-meta-grid">
        <TraceMetaCard label="trace_id" value={getShortTraceId(detail.traceId)} />
        <TraceMetaCard label="scene" value="eval" />
        <TraceMetaCard label="execution_mode" value="agentic_rag" />
        <TraceMetaCard label="status" value={selectedRow.score < 0.5 ? t("selfEvolutionRun.observation.lowScore") : detail.status} status />
        <TraceMetaCard label="latency" value={formatDuration(detail.summary.latencyMs)} />
      </div>
      <div className="self-evolution-eval-trace-summary">
        <span><strong>{roundCount || "-"}</strong><em>{t("selfEvolutionRun.observation.decisionRounds")}</em></span>
        <span><strong>{detail.summary.toolCallCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.toolCalls")}</em></span>
        <span><strong>{detail.summary.retrievalCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.kbRetrievals")}</em></span>
        <span><strong>{detail.summary.rerankCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.rerankCount")}</em></span>
        <span className="is-status"><strong>{detail.status}</strong><em>{t("selfEvolutionRun.observation.completionStatus")}</em></span>
      </div>
      <div className="self-evolution-eval-trace-grid">
        <TraceFlowPanel detail={detail} selectedNodeId={selectedNode?.id} onSelectNode={setSelectedNode} />
        <TraceInspectorPanel selectedRow={selectedRow} node={selectedNode} />
      </div>
    </section>
  );
}
