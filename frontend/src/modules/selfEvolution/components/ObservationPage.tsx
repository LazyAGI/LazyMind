import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Alert, Button, Empty, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  AimOutlined,
  ArrowLeftOutlined,
  CheckCircleOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  MenuUnfoldOutlined,
  ReloadOutlined,
  SearchOutlined,
  ThunderboltOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { useNavigate, useOutletContext, useParams } from "react-router-dom";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  AGENT_API_BASE,
  EVO_API_BASE,
  getNumberField,
  getResultItems,
  getStringField,
  getStructuredArrayField,
  getStructuredRecordField,
  isCanceledRequest,
  isEmptyResultPayload,
  isRecord,
  stringifyResultPayload,
  type WorkflowResultKind,
} from "../shared";
import {
  normalizeTraceObservation,
  type TraceDetailObservation,
  type TraceObservation,
} from "./TraceObservationView";
import traceCompareFixture from "../fixtures/evo_trace_compare_result.json";
import "../index.scss";

const { Paragraph, Text, Title } = Typography;

type ObservationResultKind = Extract<WorkflowResultKind, "eval-reports" | "abtests">;
type TraceNode = TraceDetailObservation["root"];

type ObservationPageLayoutContext = {
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
};

type ObservationHeaderControlsProps = {
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
  onBack: () => void;
};

function ObservationHeaderControls({
  isMenuCollapsed,
  toggleMenu,
  onBack,
}: ObservationHeaderControlsProps) {
  return (
    <div className="self-evolution-observation-head-controls">
      {isMenuCollapsed && toggleMenu ? (
        <Button type="text" icon={<MenuUnfoldOutlined />} onClick={toggleMenu} aria-label="展开菜单" title="展开菜单" />
      ) : null}
      <Button type="text" icon={<ArrowLeftOutlined />} onClick={onBack}>返回</Button>
    </div>
  );
}

type ObservationRouteParams = {
  threadId?: string;
  kind?: string;
};

type ObservationPageState = {
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  notice?: string;
  isFallback?: boolean;
};

type EvalBadcaseListState = {
  reportId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
};

type CsvBadcaseRow = {
  caseId: string;
  query: string;
  reference: string;
  answer: string;
  score: number;
  failureType: string;
  failureTone: "red" | "orange" | "blue";
  defect: string;
  reason: string;
  mode: string;
  traceId: string;
  traceStatus: string;
  failureReason: string;
  tracePayload?: unknown;
};

type EvalReportSummary = {
  reportId: string;
  dataset: string;
  correctRate?: number;
  badCaseCount?: number;
  traceCoverageRate?: number;
};

type TraceDocRow = {
  key: string;
  title: string;
  ref: string;
  score?: number;
  text: string;
};

type FlowRow = {
  key: string;
  round: number;
  title: string;
  desc: string;
  duration: string;
  tone: "normal" | "warning" | "success";
  node: TraceNode;
};

type AbCompareObservation = Extract<TraceObservation, { kind: "compare" }>;

type AbCaseRow = {
  caseId: string;
  query: string;
  aScore: number;
  bScore: number;
  delta: number;
  conclusion: string;
  tone: "up" | "down" | "flat";
};

type AbMetricRow = {
  key: string;
  label: string;
  meanA: number;
  meanB: number;
  winRate: number;
  signP?: string;
};

const observationKindMap: Record<string, ObservationResultKind> = {
  eval: "eval-reports",
  "eval-reports": "eval-reports",
  abtest: "abtests",
  abtests: "abtests",
};

const fallbackObservationData: Partial<Record<ObservationResultKind, unknown>> = {
  abtests: traceCompareFixture,
};
const EVAL_BADCASE_PAGE_SIZE = 1000;

const fallbackAbCaseRows: AbCaseRow[] = [
  {
    caseId: "case-083",
    query: "如何申请发票抬头变更?",
    aScore: 0.36,
    bScore: 0.42,
    delta: 0.06,
    conclusion: "小幅提升但召回不足",
    tone: "up",
  },
  {
    caseId: "case-019",
    query: "如何重置管理员密码?",
    aScore: 0.58,
    bScore: 0.46,
    delta: -0.12,
    conclusion: "B 退化",
    tone: "down",
  },
  {
    caseId: "case-104",
    query: "上传失败如何处理?",
    aScore: 0.43,
    bScore: 0.67,
    delta: 0.24,
    conclusion: "明显提升",
    tone: "up",
  },
];

const fallbackAbMetricRows: AbMetricRow[] = [
  { key: "correctness", label: "答案正确性", meanA: 0.867, meanB: 0.891, winRate: 0.1, signP: "0.629" },
  { key: "context", label: "上下文召回", meanA: 0.71, meanB: 0.7, winRate: 0, signP: "-" },
  { key: "document", label: "文档召回", meanA: 0.98, meanB: 0.98, winRate: 0, signP: "-" },
  { key: "faithfulness", label: "忠实性", meanA: 0.917, meanB: 0.943, winRate: 0.13, signP: "0.678" },
];

function normalizeObservationKind(kind?: string): ObservationResultKind | undefined {
  return kind ? observationKindMap[kind] : undefined;
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function formatDuration(ms?: number) {
  if (!isFiniteNumber(ms)) {
    return "-";
  }
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(ms >= 10_000 ? 1 : 2)}s`;
  }
  return `${ms.toFixed(ms < 10 ? 1 : 0)}ms`;
}

function getShortTraceId(traceId: string) {
  return traceId.length > 14 ? `${traceId.slice(0, 6)}...${traceId.slice(-6)}` : traceId;
}

function getStatusColor(status: string) {
  const normalized = status.toLowerCase();
  if (["success", "done", "completed", "succeeded"].includes(normalized)) {
    return "success";
  }
  if (["failed", "error"].includes(normalized)) {
    return "error";
  }
  if (["running", "pending"].includes(normalized)) {
    return "processing";
  }
  return "default";
}

function getDisplayText(value: unknown, maxLength = 120): string {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return "-";
    }
    return trimmed.length > maxLength ? `${trimmed.slice(0, maxLength)}...` : trimmed;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (Array.isArray(value)) {
    return `${value.length} items`;
  }
  if (isRecord(value)) {
    const keys = Object.keys(value);
    return keys.length ? `${keys.slice(0, 4).join(" / ")}${keys.length > 4 ? "..." : ""}` : "0 fields";
  }
  return "-";
}

function flattenTraceNodes(root: TraceNode) {
  const rows: Array<{ node: TraceNode; depth: number }> = [];
  const walk = (node: TraceNode, depth: number) => {
    rows.push({ node, depth });
    node.children.forEach((child) => walk(child, depth + 1));
  };
  walk(root, 0);
  return rows;
}

function findArrayValue(value: unknown, keys: string[], depth = 0, allowDirectArray = true): unknown[] {
  if (depth > 5) {
    return [];
  }
  if (Array.isArray(value)) {
    if (allowDirectArray) {
      return value;
    }
    for (const item of value) {
      const result = findArrayValue(item, keys, depth + 1, false);
      if (result.length > 0) {
        return result;
      }
    }
    return [];
  }
  if (!isRecord(value)) {
    return [];
  }
  for (const key of keys) {
    const nested = value[key];
    if (Array.isArray(nested)) {
      return nested;
    }
  }
  for (const nested of Object.values(value)) {
    const result = findArrayValue(nested, keys, depth + 1, false);
    if (result.length > 0) {
      return result;
    }
  }
  return [];
}

function getTraceDocs(node?: TraceNode): TraceDocRow[] {
  const docs = findArrayValue(node?.output?.data, ["items", "docs", "documents", "nodes"]);
  return docs.filter(isRecord).slice(0, 3).map((item, index) => {
    const title =
      getStringField(item, ["file_name", "display_name", "docid", "document_id", "title"]) ||
      `Doc #${index + 1}`;
    const score = getNumberField(item, ["score", "similarity", "max_score"]);
    return {
      key: getStringField(item, ["docid", "id", "chunk_id"]) || `${title}-${index}`,
      title,
      ref: getStringField(item, ["ref", "citation_index", "chunk_id"]) || `chunk-${index + 1}`,
      score,
      text: getStringField(item, ["text", "content", "summary"]) || getDisplayText(item, 180),
    };
  });
}

function getNodeDataRecord(payload?: TraceNode["input"]) {
  return isRecord(payload?.data) ? payload.data : undefined;
}

function findRecordValue(value: unknown, keys: string[], depth = 0): unknown {
  if (depth > 5) {
    return undefined;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const result = findRecordValue(item, keys, depth + 1);
      if (result !== undefined) {
        return result;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }
  for (const key of keys) {
    if (value[key] !== undefined) {
      return value[key];
    }
  }
  for (const nested of Object.values(value)) {
    const result = findRecordValue(nested, keys, depth + 1);
    if (result !== undefined) {
      return result;
    }
  }
  return undefined;
}

function getNodeTitle(node: TraceNode) {
  if (node.type === "tool") {
    return `Tool Call: ${node.name}`;
  }
  if (node.type === "llm") {
    return "LLM Generate";
  }
  return node.name;
}

function renderPayloadBlock(label: string, payload?: { summary?: string; data?: unknown }) {
  if (!payload?.summary && payload?.data === undefined) {
    return <Paragraph className="self-evolution-eval-empty">暂无{label}数据。</Paragraph>;
  }

  return (
    <>
      {payload?.summary && <Paragraph className="self-evolution-eval-payload-summary">{payload.summary}</Paragraph>}
      {payload?.data !== undefined && (
        <details className="self-evolution-eval-payload-json">
          <summary>{`查看${label} JSON`}</summary>
          <pre>{stringifyResultPayload(payload.data)}</pre>
        </details>
      )}
    </>
  );
}

function renderMetadataTiles(metadata?: Record<string, unknown>) {
  const entries = Object.entries(metadata || {}).slice(0, 8);
  if (entries.length === 0) {
    return <Paragraph className="self-evolution-eval-empty">暂无 metadata。</Paragraph>;
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

function buildFlowRows(detail: TraceDetailObservation): FlowRow[] {
  const rootChildren = detail.root.children.length ? detail.root.children : [detail.root];
  return rootChildren.flatMap((roundNode, roundIndex) => {
    const descendants = flattenTraceNodes(roundNode)
      .filter(({ node }) => !["_build_history", "Pipeline", "FunctionCall"].includes(node.name))
      .filter(({ node }) => node.type === "llm" || node.type === "tool" || node.type === "retriever" || node.type === "rerank" || node.children.length === 0)
      .slice(0, 5);
    const rows = descendants.length ? descendants : [{ node: roundNode, depth: 0 }];
    return rows.map(({ node }, index) => ({
      key: `${roundIndex}-${node.id}-${index}`,
      round: roundIndex + 1,
      title: getNodeTitle(node),
      desc: node.output?.summary || node.input?.summary || "暂无摘要",
      duration: formatDuration(node.latencyMs),
      tone: getTraceDocs(node).length > 0 || node.status === "warning" ? "warning" : node.status === "success" ? "success" : "normal",
      node,
    }));
  });
}

function getBadcaseSourceRecords(value: unknown): Record<string, unknown>[] {
  const rawSources = Array.isArray(value) ? value : [value];
  return rawSources.filter(isRecord).flatMap((item) => {
    const dataRecord = getStructuredRecordField(item, ["data"]);
    return dataRecord ? [dataRecord, item] : [item];
  });
}

function normalizeBadcaseRows(value: unknown): CsvBadcaseRow[] {
  const candidateRows = getBadcaseSourceRecords(value)
    .flatMap((item) => [
      ...(getStructuredArrayField(item, ["bad_cases"]) || []),
      ...(getStructuredArrayField(item, ["badcases"]) || []),
      ...(getStructuredArrayField(item, ["badcase_list"]) || []),
      ...(getStructuredArrayField(item, ["cases"]) || []),
      ...(getStructuredArrayField(item, ["rows"]) || []),
      ...(getStructuredArrayField(item, ["records"]) || []),
      ...(getStructuredArrayField(item, ["items"]) || []),
    ]);
  const rows = candidateRows.filter(isRecord).map((item, index): CsvBadcaseRow => {
    const score = getNumberField(item, ["score", "metric_score", "answer_correctness", "value"]) ?? 0;
    const failureType = getStringField(item, ["failure_type", "failure_reason", "fail_reason", "category"]) || "待分析";
    return {
      caseId: getStringField(item, ["case_id", "caseId", "case", "id"]) || `case-${String(index + 1).padStart(3, "0")}`,
      query: getStringField(item, ["query", "question", "prompt"]) || "-",
      reference: getStringField(item, ["reference", "ground_truth", "expected_answer"]) || "-",
      answer: getStringField(item, ["answer", "actual_answer", "prediction"]) || "-",
      score,
      failureType,
      failureTone: score < 0.5 ? "orange" : score < 0.6 ? "red" : "blue",
      defect: getStringField(item, ["Defect", "defect"]) || "-",
      reason: getStringField(item, ["Reason", "reason", "failure_detail"]) || failureType,
      mode: getStringField(item, ["mode", "execution_mode"]) || "Agentic RAG",
      traceId: getStringField(item, ["trace_id", "traceId"]) || "-",
      traceStatus: getStringField(item, ["trace_status", "traceStatus"]) || "已关联",
      failureReason: getStringField(item, ["failure_detail", "failure_reason", "fail_reason", "Reason", "reason"]) || failureType,
      tracePayload: item.trace || item.observation || item.trace_detail,
    };
  });
  return rows;
}

function normalizeAbCaseRows(value: unknown): AbCaseRow[] {
  const candidateRows = isRecord(value)
    ? (["cases", "case_list", "rows", "records", "items", "badcases"] as const)
      .flatMap((key) => Array.isArray(value[key]) ? value[key] : [])
    : Array.isArray(value)
      ? value
      : [];
  const rows = candidateRows.filter(isRecord).map((item, index): AbCaseRow => {
    const aScore = getNumberField(item, ["a_score", "score_a", "baseline_score", "mean_a"]) ?? 0;
    const bScore = getNumberField(item, ["b_score", "score_b", "candidate_score", "mean_b"]) ?? 0;
    const delta = getNumberField(item, ["delta", "change", "score_delta"]) ?? bScore - aScore;
    return {
      caseId: getStringField(item, ["case_id", "caseId", "case", "id"]) || `case-${String(index + 1).padStart(3, "0")}`,
      query: getStringField(item, ["query", "question", "prompt"]) || "-",
      aScore,
      bScore,
      delta,
      conclusion: getStringField(item, ["conclusion", "judgement", "result"]) || (delta > 0 ? "B 提升" : delta < 0 ? "B 退化" : "持平"),
      tone: delta > 0 ? "up" : delta < 0 ? "down" : "flat",
    };
  });
  return rows.length ? rows : fallbackAbCaseRows;
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function formatOptionalPercent(value?: number) {
  if (!isFiniteNumber(value)) {
    return "-";
  }
  return formatPercent(value);
}

function formatDeltaScore(value: number) {
  return `${value > 0 ? "+" : ""}${value.toFixed(2)}`;
}

function formatDeltaPercent(a: number, b: number) {
  const delta = b - a;
  return `${delta > 0 ? "+" : ""}${Math.round(delta * 100)}%`;
}

function getDetailRoundCount(detail: TraceDetailObservation) {
  return Math.max(detail.root.children.length, detail.summary.roundCount || 0);
}

function getTraceMode(detail: TraceDetailObservation) {
  const rows = flattenTraceNodes(detail.root);
  return rows.some(({ node }) => node.type === "tool" || node.type === "retriever") ? "Agentic RAG" : "RAG";
}

function getSearchNode(detail: TraceDetailObservation) {
  const rows = flattenTraceNodes(detail.root);
  return (
    rows.find(({ node }) => node.name.includes("kb_search"))?.node ||
    rows.find(({ node }) => getTraceDocs(node).length > 0)?.node ||
    rows.find(({ node }) => node.type === "retriever")?.node ||
    rows.find(({ node }) => node.type === "tool")?.node ||
    rows[0]?.node
  );
}

function getAbReturnedDocs(node?: TraceNode) {
  const docs = getTraceDocs(node);
  const outputData = getNodeDataRecord(node?.output);
  return docs.length || Number(findRecordValue(outputData, ["total", "returned_docs", "node_count"])) || 0;
}

function getAbMaxScore(node?: TraceNode) {
  const docs = getTraceDocs(node);
  const outputData = getNodeDataRecord(node?.output);
  const score = docs[0]?.score ?? getNumberField(outputData, ["max_score", "score"]);
  return isFiniteNumber(score) ? score : undefined;
}

function getPrimaryObservation(observation?: TraceObservation) {
  if (!observation) {
    return undefined;
  }
  return observation.kind === "detail" ? observation.detail : observation.a;
}

function getPrimaryEvalReportRecord(value: unknown) {
  const items = getResultItems(value);
  const itemRecord = items.find((item): item is Record<string, unknown> => isRecord(item));
  if (itemRecord) {
    return itemRecord;
  }
  return isRecord(value) ? value : undefined;
}

function normalizeEvalReportSummary(value: unknown): EvalReportSummary {
  const row = getPrimaryEvalReportRecord(value);
  const data = getStructuredRecordField(row, ["data"]);
  const metrics = getStructuredRecordField(data, ["metrics"]);
  const traceCoverage = getStructuredRecordField(row, ["trace_coverage"]);

  return {
    reportId: getStringField(row, ["report_id"]) || "-",
    dataset: getStringField(data, ["eval_dataset_ref"]) || "-",
    correctRate: getNumberField(metrics, ["correct_rate"]),
    badCaseCount: getNumberField(row, ["bad_case_count"]),
    traceCoverageRate: getNumberField(traceCoverage, ["rate"]),
  };
}

function MetricCard({
  icon,
  label,
  value,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  tone: "blue" | "red" | "green" | "purple";
}) {
  return (
    <article className={`self-evolution-eval-metric is-${tone}`}>
      <span>{icon}</span>
      <div>
        <em>{label}</em>
        <strong>{value}</strong>
      </div>
    </article>
  );
}

function EvalReportPanel({
  summary,
  rows,
  rowsError,
  rowsLoading,
  selectedCaseId,
  onSelectCase,
  onReloadRows,
}: {
  summary: EvalReportSummary;
  rows: CsvBadcaseRow[];
  rowsError?: string;
  rowsLoading?: boolean;
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
  onReloadRows: () => void;
}) {
  const selectedRow = rows.find((item) => item.caseId === selectedCaseId) || rows[0];
  const columns: ColumnsType<CsvBadcaseRow> = [
    { title: "Case", dataIndex: "caseId", key: "caseId", width: 104 },
    {
      title: "Score",
      dataIndex: "score",
      key: "score",
      width: 78,
      render: (value: number) => <span className={value < 0.5 ? "is-low-score" : ""}>{value.toFixed(2)}</span>,
    },
    {
      title: "失败原因",
      dataIndex: "failureType",
      key: "failureType",
      width: 110,
      render: (value: string, row) => <Tag className={`self-evolution-eval-reason is-${row.failureTone}`}>{value}</Tag>,
    },
    {
      title: "Defect",
      dataIndex: "defect",
      key: "defect",
      width: 230,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Reason",
      dataIndex: "reason",
      key: "reason",
      width: 360,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Trace",
      dataIndex: "traceId",
      key: "traceId",
      width: 170,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
  ];

  return (
    <section className="self-evolution-eval-report-card" aria-label="评测报告详情">
      <div className="self-evolution-eval-report-head">
        <div>
          <Title level={3}>评测报告详情</Title>
          <Text>{`报告 ID：${summary.reportId}`}</Text>
          <Text>{`数据集：${summary.dataset} · 样本数 - · Badcase ${summary.badCaseCount ?? "-"}`}</Text>
        </div>
      </div>
      <div className="self-evolution-eval-metric-grid">
        <MetricCard icon={<AimOutlined />} label="准确率" value={formatOptionalPercent(summary.correctRate)} tone="blue" />
        <MetricCard icon={<WarningOutlined />} label="Badcase" value={String(summary.badCaseCount ?? "-")} tone="red" />
        <MetricCard icon={<ThunderboltOutlined />} label="Trace 覆盖率" value={formatOptionalPercent(summary.traceCoverageRate)} tone="purple" />
      </div>
      <div className="self-evolution-eval-badcase-panel">
        <div className="self-evolution-eval-section-title">
          <Text strong>Badcase 列表</Text>
          <span>来自 eval-reports/{summary.reportId}/bad-cases</span>
        </div>
        <div className="self-evolution-eval-filter-row">
          <label>
            状态：
            <select aria-label="Badcase 状态筛选">
              <option>全部</option>
            </select>
          </label>
          <label>
            失败类型：
            <select aria-label="Badcase 失败类型筛选">
              <option>全部</option>
            </select>
          </label>
          <label className="self-evolution-eval-search">
            <SearchOutlined />
            <input aria-label="搜索 case 或 query" placeholder="搜索 case_id / query" />
          </label>
          <Button size="small">重置</Button>
        </div>
        {rowsError ? (
          <Alert
            type="error"
            showIcon
            message={rowsError}
            action={<Button size="small" onClick={onReloadRows}>重试</Button>}
          />
        ) : (
          <Table<CsvBadcaseRow>
            className="self-evolution-eval-badcase-table"
            size="small"
            rowKey="caseId"
            columns={columns}
            dataSource={rows}
            loading={rowsLoading}
            pagination={false}
            rowClassName={(row) => row.caseId === selectedCaseId ? "is-selected" : ""}
            scroll={{ x: 1052 }}
            onRow={(row) => ({
              onClick: () => onSelectCase(row.caseId),
            })}
          />
        )}
      </div>
      {selectedRow && (
        <div className="self-evolution-eval-case-result">
          <div className="self-evolution-eval-section-title">
            <Text strong>{`${selectedRow.caseId} 执行结果`}</Text>
          </div>
          <dl>
            <dt>Score</dt>
            <dd>
              <span className="is-low-score">{selectedRow.score.toFixed(2)}</span>
              <Tag className={`self-evolution-eval-reason is-${selectedRow.failureTone}`}>{selectedRow.failureType}</Tag>
            </dd>
            <dt>失败原因</dt>
            <dd>{selectedRow.failureReason}</dd>
            <dt>Defect</dt>
            <dd>{selectedRow.defect}</dd>
            <dt>Reason</dt>
            <dd>{selectedRow.reason}</dd>
          </dl>
          <div className="self-evolution-eval-case-actions">
            <Button type="primary">查看智能链路</Button>
            <Button icon={<FileSearchOutlined />}>查看原始 trace</Button>
          </div>
        </div>
      )}
    </section>
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
  const flowRows = useMemo(() => buildFlowRows(detail), [detail]);
  const rowsByRound = useMemo(() => {
    const grouped = new Map<number, FlowRow[]>();
    flowRows.forEach((row) => {
      grouped.set(row.round, [...(grouped.get(row.round) || []), row]);
    });
    return Array.from(grouped.entries());
  }, [flowRows]);

  return (
    <section className="self-evolution-eval-flow-panel" aria-label="智能执行流程">
      <div className="self-evolution-eval-panel-title">
        <Text strong>智能执行流程</Text>
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
  const docs = getTraceDocs(node);
  const metadata = node?.metadata || {};
  const inputData = getNodeDataRecord(node?.input);

  if (!node) {
    return (
      <section className="self-evolution-eval-inspector-panel is-empty" aria-label="节点详情">
        <div className="self-evolution-eval-panel-title">
          <Text strong>节点详情</Text>
        </div>
        <div className="self-evolution-eval-inspector-empty">
          <FileSearchOutlined />
          <Text strong>点击左侧流程节点查看详情</Text>
          <Paragraph>详情会根据当前 Badcase 关联的 trace JSON 展示节点输入、输出、召回文档和 metadata。</Paragraph>
        </div>
      </section>
    );
  }

  return (
    <section className="self-evolution-eval-inspector-panel" aria-label="节点详情">
      <div className="self-evolution-eval-panel-title">
        <Text strong>{`节点详情：${getNodeTitle(node)}`}</Text>
        <Tag color={getStatusColor(node.status)}>{node.status}</Tag>
      </div>
      <div className="self-evolution-eval-inspector-body">
        <div className="self-evolution-eval-inspector-section">
          <h4>1. 节点信息</h4>
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
          <h4>2. 输入</h4>
          {renderPayloadBlock("输入", node.input?.summary || inputData !== undefined ? { summary: node.input?.summary, data: inputData } : undefined)}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>3. 输出</h4>
          {renderPayloadBlock("输出", node.output)}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>4. 召回文档</h4>
          {docs.length ? (
            docs.map((doc, index) => (
              <article key={doc.key} className="self-evolution-eval-doc-card">
                <FileTextOutlined />
                <div>
                  <strong>{`Doc #${index + 1} ${doc.title}`}</strong>
                  <span>{`score: ${doc.score?.toFixed(2) || "-"} · ${doc.ref}`}</span>
                  <p>{doc.text}</p>
                </div>
                {doc.score !== undefined && <Tag color="blue">{`相关度: ${doc.score.toFixed(2)}`}</Tag>}
              </article>
            ))
          ) : (
            <Paragraph className="self-evolution-eval-empty">该节点暂无可展示召回文档。</Paragraph>
          )}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>5. Metadata</h4>
          {renderMetadataTiles(metadata)}
        </div>
        <div className="self-evolution-eval-inspector-section is-warning">
          <h4>6. 观测判断</h4>
          <p>{selectedRow.failureReason}</p>
        </div>
      </div>
    </section>
  );
}

function EvalTracePanel({ detail, selectedRow }: { detail: TraceDetailObservation; selectedRow: CsvBadcaseRow }) {
  const [selectedNode, setSelectedNode] = useState<TraceNode | undefined>();
  useEffect(() => {
    setSelectedNode(undefined);
  }, [detail, selectedRow.caseId]);
  const roundCount = Math.max(detail.root.children.length, detail.summary.roundCount || 0);
  return (
    <section className="self-evolution-eval-trace-card" aria-label="Agentic RAG 观测详情">
      <div className="self-evolution-eval-trace-title">
        <Title level={3}>{`Agentic RAG 观测详情 · ${selectedRow.caseId}`}</Title>
      </div>
      <div className="self-evolution-eval-trace-meta-grid">
        <TraceMetaCard label="trace_id" value={getShortTraceId(detail.traceId)} />
        <TraceMetaCard label="scene" value="eval" />
        <TraceMetaCard label="execution_mode" value="agentic_rag" />
        <TraceMetaCard label="status" value={selectedRow.score < 0.5 ? "低分" : detail.status} status />
        <TraceMetaCard label="latency" value={formatDuration(detail.summary.latencyMs)} />
      </div>
      <div className="self-evolution-eval-trace-summary">
        <span><strong>{roundCount || "-"}</strong><em>决策轮次</em></span>
        <span><strong>{detail.summary.toolCallCount ?? "-"}</strong><em>工具调用</em></span>
        <span><strong>{detail.summary.retrievalCount ?? "-"}</strong><em>知识库检索</em></span>
        <span><strong>{detail.summary.rerankCount ?? "-"}</strong><em>重排次数</em></span>
        <span className="is-status"><strong>{detail.status}</strong><em>完成状态</em></span>
      </div>
      <div className="self-evolution-eval-trace-grid">
        <TraceFlowPanel detail={detail} selectedNodeId={selectedNode?.id} onSelectNode={setSelectedNode} />
        <TraceInspectorPanel selectedRow={selectedRow} node={selectedNode} />
      </div>
    </section>
  );
}

function EvalObservationDashboard({
  data,
  notice,
  isFallback,
  threadId,
  onBack,
  isMenuCollapsed,
  toggleMenu,
}: {
  data: unknown;
  notice?: string;
  isFallback?: boolean;
  threadId?: string;
  onBack: () => void;
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
}) {
  const summary = useMemo(() => normalizeEvalReportSummary(data), [data]);
  const [badcaseReloadToken, setBadcaseReloadToken] = useState(0);
  const [badcaseState, setBadcaseState] = useState<EvalBadcaseListState>({
    loading: false,
    loaded: false,
  });
  const rows = useMemo(() => normalizeBadcaseRows(badcaseState.data), [badcaseState.data]);
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

    axiosInstance
      .get(
        `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/eval-reports/${encodeURIComponent(summary.reportId)}/bad-cases`,
        {
          params: { page_size: EVAL_BADCASE_PAGE_SIZE },
          signal: controller.signal,
        },
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
          error: getLocalizedErrorMessage(error, "Badcase 列表加载失败，请稍后重试。"),
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
      setTraceState({ loading: false, data: undefined, error: traceId ? undefined : "当前 Badcase 未提供 trace_id。" });
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
            error: "当前 trace 接口暂无数据。",
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
          error: getLocalizedErrorMessage(error, "观测详情加载失败，请稍后重试。"),
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
          {isFallback && <Tag color="gold">暂无数据</Tag>}
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
            <section className="self-evolution-eval-trace-card" aria-label="Agentic RAG 观测详情">
              <Spin />
            </section>
          ) : detail ? (
            <EvalTracePanel detail={detail} selectedRow={selectedRow} />
          ) : (
            <section className="self-evolution-eval-trace-card">
              <Empty description={traceState.error || "当前 Badcase 暂无观测详情"} />
            </section>
          )
        ) : (
          <section className="self-evolution-eval-trace-card">
            <Empty description="当前 Badcase 暂无观测详情" />
          </section>
        )}
      </div>
    </div>
  );
}

function AbMetricChart({ metrics }: { metrics: AbMetricRow[] }) {
  return (
    <div className="self-evolution-abtest-chart" aria-label="A/B 指标柱状图">
      <div className="self-evolution-abtest-chart-axis">
        <span>1.00</span>
        <span>0.75</span>
        <span>0.50</span>
        <span>0.25</span>
        <span>0.00</span>
      </div>
      <div className="self-evolution-abtest-chart-groups">
        {metrics.map((metric) => {
          const delta = metric.meanB - metric.meanA;
          return (
            <div key={metric.key} className="self-evolution-abtest-chart-group">
              <strong className={delta >= 0 ? "is-up" : "is-down"}>{formatDeltaPercent(metric.meanA, metric.meanB)}</strong>
              <div className="self-evolution-abtest-bars">
                <span className="is-a" style={{ height: `${Math.max(6, metric.meanA * 100)}%` }} />
                <span className="is-b" style={{ height: `${Math.max(6, metric.meanB * 100)}%` }} />
              </div>
              <em>{metric.label}</em>
            </div>
          );
        })}
      </div>
      <div className="self-evolution-abtest-chart-legend">
        <span><i className="is-a" />A 评测（基线）</span>
        <span><i className="is-b" />B 评测（优化后）</span>
      </div>
    </div>
  );
}

function AbReportPanel({
  rows,
  selectedCaseId,
  onSelectCase,
}: {
  rows: AbCaseRow[];
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
}) {
  const columns: ColumnsType<AbCaseRow> = [
    {
      title: "Case",
      dataIndex: "caseId",
      key: "caseId",
      width: 94,
      render: (value: string) => <Text className="self-evolution-abtest-case-link">{value}</Text>,
    },
    {
      title: "Query",
      dataIndex: "query",
      key: "query",
      width: 230,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "A Score",
      dataIndex: "aScore",
      key: "aScore",
      width: 80,
      render: (value: number) => value.toFixed(2),
    },
    {
      title: "B Score",
      dataIndex: "bScore",
      key: "bScore",
      width: 80,
      render: (value: number) => value.toFixed(2),
    },
    {
      title: "变化",
      dataIndex: "delta",
      key: "delta",
      width: 76,
      render: (value: number, row) => <span className={`self-evolution-abtest-delta is-${row.tone}`}>{formatDeltaScore(value)}</span>,
    },
    { title: "结论", dataIndex: "conclusion", key: "conclusion", width: 140 },
    {
      title: "操作",
      key: "action",
      width: 112,
      render: (_, row) => (
        <Button size="small" type={row.caseId === selectedCaseId ? "primary" : "default"} onClick={() => onSelectCase(row.caseId)}>
          查看 A/B Trace
        </Button>
      ),
    },
  ];

  return (
    <section className="self-evolution-abtest-report-card" aria-label="自进化执行编排">
      <div className="self-evolution-abtest-report-head">
        <div>
          <Title level={3}>自进化执行编排</Title>
          <Text>当前阶段：A/B 测试报告</Text>
          <Text>对齐样本 100 · 主指标 答案正确性 · 保护指标 文档召回 / 上下文召回</Text>
        </div>
        <Tag color="orange">inconclusive</Tag>
      </div>
      <div className="self-evolution-abtest-report-id">
        <strong>abtest_20260507_112323_3dec8237</strong>
      </div>
      <AbMetricChart metrics={fallbackAbMetricRows} />
      <div className="self-evolution-abtest-metric-table" aria-label="A/B 指标表">
        <div className="self-evolution-abtest-table-row is-head">
          <span>指标</span>
          <span>mean A</span>
          <span>mean B</span>
          <span>Δmean</span>
          <span>B 胜率</span>
          <span>sign p</span>
        </div>
        {fallbackAbMetricRows.map((metric) => (
          <div key={metric.key} className="self-evolution-abtest-table-row">
            <span>{metric.label}</span>
            <span>{formatPercent(metric.meanA)}</span>
            <span>{formatPercent(metric.meanB)}</span>
            <strong className={metric.meanB >= metric.meanA ? "is-up" : "is-down"}>{formatDeltaPercent(metric.meanA, metric.meanB)}</strong>
            <span>{formatPercent(metric.winRate)}</span>
            <span>{metric.signP || "-"}</span>
          </div>
        ))}
      </div>
      <div className="self-evolution-abtest-case-panel">
        <div className="self-evolution-eval-section-title">
          <Text strong>变化 case 列表</Text>
          <span>前端样例，等待第五步报告接口替换</span>
        </div>
        <Table<AbCaseRow>
          className="self-evolution-abtest-case-table"
          size="small"
          rowKey="caseId"
          columns={columns}
          dataSource={rows}
          pagination={{ pageSize: 3, size: "small", showSizeChanger: false }}
          rowClassName={(row) => row.caseId === selectedCaseId ? "is-selected" : ""}
          scroll={{ x: 820 }}
          onRow={(row) => ({
            onClick: () => onSelectCase(row.caseId),
          })}
        />
      </div>
    </section>
  );
}

function AbSummaryStrip({ observation }: { observation: AbCompareObservation }) {
  const a = observation.a;
  const b = observation.b;
  return (
    <div className="self-evolution-abtest-summary-strip" aria-label="A/B Trace 汇总">
      <span className="self-evolution-abtest-summary-group">
        <span className="is-side">A</span>
        <span><strong>{getDetailRoundCount(a)}</strong><em>轮决策</em></span>
        <span><strong>{a.summary.toolCallCount ?? "-"}</strong><em>工具调用</em></span>
        <span><strong>{a.summary.retrievalCount ?? "-"}</strong><em>次检索</em></span>
        <strong>{formatDuration(a.summary.latencyMs)}</strong>
      </span>
      <i>→</i>
      <span className="self-evolution-abtest-summary-group">
        <span className="is-side is-b">B</span>
        <span><strong>{getDetailRoundCount(b)}</strong><em>轮决策</em></span>
        <span><strong>{b.summary.toolCallCount ?? "-"}</strong><em>工具调用</em></span>
        <span><strong>{b.summary.retrievalCount ?? "-"}</strong><em>次检索</em></span>
        <strong>{formatDuration(b.summary.latencyMs)}</strong>
      </span>
    </div>
  );
}

function AbTraceStep({
  row,
  variant,
}: {
  row: FlowRow;
  variant: "a" | "b";
}) {
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
          <span>returned_docs: <strong>{variant === "a" ? 0 : Math.max(1, getAbReturnedDocs(row.node))}</strong></span>
          <span>max_score: <strong>{(getAbMaxScore(row.node) ?? (variant === "a" ? 0.18 : 0.31)).toFixed(2)}</strong></span>
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
  const rowsByRound = useMemo(() => {
    const grouped = new Map<number, FlowRow[]>();
    buildFlowRows(detail).forEach((row) => {
      grouped.set(row.round, [...(grouped.get(row.round) || []), row]);
    });
    return Array.from(grouped.entries()).slice(0, 4);
  }, [detail]);
  const score = variant === "a" ? selectedCase.aScore : selectedCase.bScore;

  return (
    <section className={`self-evolution-abtest-trace-column is-${variant}`} aria-label={`${title} Trace`}>
      <div className="self-evolution-abtest-column-title">
        <Text strong>{title}</Text>
        <span>{getTraceMode(detail)}</span>
      </div>
      <div className="self-evolution-abtest-algo-grid">
        <span><em>算法版本</em><strong>{variant === "a" ? "baseline-v1" : "candidate-v2"}</strong></span>
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
              <AbTraceStep key={`${variant}-${row.key}`} row={row} variant={variant} />
            ))}
          </div>
        ))}
      </div>
      <div className={`self-evolution-abtest-column-note is-${variant === "a" ? "danger" : "warning"}`}>
        {variant === "a"
          ? "未召回关键文档，答案缺少申请材料和时效依据。"
          : "召回到相关文档，但证据片段仍不完整，需要继续优化检索策略。"}
      </div>
    </section>
  );
}

function AbDiffPanel({
  observation,
}: {
  observation: AbCompareObservation;
}) {
  const aNode = getSearchNode(observation.a);
  const bNode = getSearchNode(observation.b);
  const aScore = getAbMaxScore(aNode) ?? 0.18;
  const bScore = getAbMaxScore(bNode) ?? 0.31;
  return (
    <section className="self-evolution-abtest-diff-panel" aria-label="选中差异节点">
      <Text strong>选中差异节点：Round 2 · kb_search</Text>
      <div className="self-evolution-abtest-diff-grid">
        <article>
          <Text strong>A 输出（baseline-v1）</Text>
          <dl>
            <dt>returned_docs</dt><dd>{getAbReturnedDocs(aNode)}</dd>
            <dt>max_score</dt><dd>{aScore.toFixed(2)}</dd>
            <dt>判断</dt><dd className="is-bad">未命中相关流程</dd>
          </dl>
        </article>
        <article>
          <Text strong>B 输出（candidate-v2）</Text>
          <dl>
            <dt>returned_docs</dt><dd>{Math.max(1, getAbReturnedDocs(bNode))}</dd>
            <dt>max_score</dt><dd>{bScore.toFixed(2)}</dd>
            <dt>判断</dt><dd className="is-warn">命中相关文档但覆盖不完整</dd>
          </dl>
        </article>
      </div>
    </section>
  );
}

function AbTraceComparePanel({
  observation,
  selectedCase,
}: {
  observation: AbCompareObservation;
  selectedCase: AbCaseRow;
}) {
  return (
    <section className="self-evolution-abtest-compare-card" aria-label="Agentic A/B Trace 对比">
      <div className="self-evolution-abtest-compare-head">
        <Title level={3}>{`Agentic A/B Trace 对比 · ${selectedCase.caseId}`}</Title>
        <div>
          <Tag>{`Query: ${selectedCase.query}`}</Tag>
          <Tag>{`Report ID: abtest_...8237`}</Tag>
          <Tag color="orange">状态：需继续分析</Tag>
        </div>
      </div>
      <AbSummaryStrip observation={observation} />
      <div className="self-evolution-abtest-trace-columns">
        <AbTraceColumn title="A 基线算法" variant="a" detail={observation.a} selectedCase={selectedCase} />
        <AbTraceColumn title="B 优化后算法" variant="b" detail={observation.b} selectedCase={selectedCase} />
      </div>
      <AbDiffPanel observation={observation} />
      <div className="self-evolution-abtest-conclusion">
        <Text strong>观测结论：</Text>
        <span>B 版本通过补充检索 query 召回到相关文档，使答案正确性小幅提升；但上下文证据仍不足，当前 case 仍需继续分析。</span>
      </div>
    </section>
  );
}

function AbtestObservationDashboard({
  data,
  notice,
  isFallback,
  loading,
  threadId,
  onBack,
  onReload,
  isMenuCollapsed,
  toggleMenu,
}: {
  data: unknown;
  notice?: string;
  isFallback?: boolean;
  loading: boolean;
  threadId?: string;
  onBack: () => void;
  onReload: () => void;
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
}) {
  const rows = useMemo(() => normalizeAbCaseRows(data), [data]);
  const observation = useMemo(() => normalizeTraceObservation(data) || normalizeTraceObservation(traceCompareFixture), [data]);
  const compareObservation = observation?.kind === "compare" ? observation : undefined;
  const [selectedCaseId, setSelectedCaseId] = useState(rows[0]?.caseId || "");
  const selectedCase = rows.find((row) => row.caseId === selectedCaseId) || rows[0];

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
          {isFallback && <Tag color="gold">样例数据</Tag>}
          <Button icon={<ReloadOutlined />} loading={loading} onClick={onReload}>刷新</Button>
        </div>
      </header>
      {notice && !loading && <Alert type="warning" showIcon message={notice} />}
      {loading && !data ? (
        <div className="self-evolution-observation-page-loading">
          <Spin />
          <Text>正在加载 A/B 观测数据...</Text>
        </div>
      ) : compareObservation && selectedCase ? (
        <div className="self-evolution-abtest-dashboard-grid">
          <AbReportPanel rows={rows} selectedCaseId={selectedCase.caseId} onSelectCase={setSelectedCaseId} />
          <AbTraceComparePanel observation={compareObservation} selectedCase={selectedCase} />
        </div>
      ) : (
        <section className="self-evolution-observation-json-card" aria-label="原始 A/B 观测数据">
          <div className="self-evolution-observation-data-head">
            <div>
              <Text strong>原始数据</Text>
              <span>当前结构不是 A/B Trace 对比结构，先按 JSON 展示</span>
            </div>
          </div>
          <pre>{stringifyResultPayload(data)}</pre>
        </section>
      )}
    </div>
  );
}

export function SelfEvolutionObservationPage() {
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
      setState({ loading: false, loaded: true, error: "观测路由参数不完整。" });
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
            data: fallbackObservationData[resultKind],
            notice: resultKind === "eval-reports"
              ? "当前接口暂无第二步 CSV 数据。"
              : "当前接口暂无观测数据，先展示你提供的样例数据。",
            isFallback: true,
          });
          return;
        }
        setState({ loading: false, loaded: true, data: response.data });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        const errorMessage = getLocalizedErrorMessage(error, "观测数据加载失败，请稍后重试。");
        setState({
          loading: false,
          loaded: true,
          data: fallbackObservationData[resultKind],
          notice: resultKind === "eval-reports"
            ? errorMessage
            : `观测接口暂不可用，先展示前端样例数据。${errorMessage}`,
          isFallback: true,
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
        isFallback={state.isFallback}
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
        isFallback={state.isFallback}
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
            <Title level={3}>Step 5 · A/B 观测</Title>
            <Paragraph>Case A/B Trace 对比与结构化数据表</Paragraph>
          </div>
        </div>
        <div className="self-evolution-observation-page-meta">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          {resultKind && <Tag color="blue">{resultKind}</Tag>}
          {state.isFallback && <Tag color="gold">样例数据</Tag>}
          <Button icon={<ReloadOutlined />} loading={state.loading} onClick={reload}>刷新</Button>
        </div>
      </header>
      <main className="self-evolution-observation-page-body" aria-live="polite">
        {state.notice && !state.loading && <Alert type="warning" showIcon message={state.notice} />}
        {!resultKind ? (
          <Alert type="warning" showIcon message="未知观测类型" description={`当前路径中的 kind=${kind || "-"}，支持 eval、eval-reports、abtest、abtests。`} />
        ) : state.loading && !state.loaded ? (
          <div className="self-evolution-observation-page-loading">
            <Spin />
            <Text>正在加载观测数据...</Text>
          </div>
        ) : state.error ? (
          <Alert
            type="error"
            showIcon
            message="观测数据加载失败"
            description={state.error}
            action={<Button size="small" onClick={reload}>重试</Button>}
          />
        ) : isEmpty ? (
          <Empty description="当前线程暂无观测数据" />
        ) : (
          <section className="self-evolution-observation-json-card" aria-label="原始观测数据">
            <div className="self-evolution-observation-data-head">
              <div>
                <Text strong>原始数据</Text>
                <span>当前结构不是 Trace 观测结构，先按 JSON 展示</span>
              </div>
            </div>
            <pre>{stringifyResultPayload(state.data)}</pre>
          </section>
        )}
      </main>
    </div>
  );
}
