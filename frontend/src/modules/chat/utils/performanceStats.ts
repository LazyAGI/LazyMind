import { RoleTypes } from "@/modules/chat/constants/common";

export interface RunPerformanceMetrics {
  schema_version?: number;
  turn_seq?: number;
  steps?: number;
  model_steps?: number;
  tool_steps?: number;
  model_ms?: number;
  tool_ms?: number;
  model?: string;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  cached_tokens?: number;
  reasoning_tokens?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  prompt_cache_hit_tokens?: number;
  cache_hit_rate?: number;
  provider_usages?: Array<Record<string, unknown>>;
  ttft_ms?: number;
  tok_s?: number;
  max_input_tokens?: number;
  context_ratio?: number;
}

export interface SessionPerformanceStats {
  turns: number;
  steps: number;
  model?: string;
  modelMs: number;
  toolMs: number;
  promptTokens?: number;
  completionTokens?: number;
  sessionCacheHitRate?: number;
  turnCacheHitRate?: number;
  ttftMs?: number;
  tokS?: number;
  contextRatio?: number;
}

type MetricsRow = {
  seq?: number;
  metrics: RunPerformanceMetrics;
};

type PerformanceMessage = {
  role?: string;
  seq?: number;
  run_terminal?: unknown;
  performance_metrics?: RunPerformanceMetrics;
  answers?: Array<{ run_terminal?: unknown; performance_metrics?: RunPerformanceMetrics }>;
};

function asFiniteNumber(value: unknown): number | undefined {
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function inputTokens(metrics: RunPerformanceMetrics): number | undefined {
  return asFiniteNumber(metrics.input_tokens) ?? asFiniteNumber(metrics.prompt_tokens);
}

function outputTokens(metrics: RunPerformanceMetrics): number | undefined {
  return asFiniteNumber(metrics.output_tokens) ?? asFiniteNumber(metrics.completion_tokens);
}

function cachedTokens(metrics: RunPerformanceMetrics): number | undefined {
  return asFiniteNumber(metrics.cached_tokens) ?? asFiniteNumber(metrics.prompt_cache_hit_tokens);
}

function weightedCacheHitRate(rows: RunPerformanceMetrics[]): number | undefined {
  const observable = rows.filter((row) => cachedTokens(row) != null);
  const cached = observable.reduce((total, row) => total + (cachedTokens(row) ?? 0), 0);
  const input = observable.reduce((total, row) => total + (inputTokens(row) ?? 0), 0);
  if (observable.length && input > 0) return cached / input;
  const last = rows[rows.length - 1];
  return last ? asFiniteNumber(last.cache_hit_rate) : undefined;
}

export function metricsFromRunTerminal(terminal: unknown): RunPerformanceMetrics | undefined {
  if (!terminal || typeof terminal !== "object") return undefined;
  const metrics = (terminal as { metrics?: RunPerformanceMetrics }).metrics;
  if (!metrics || typeof metrics !== "object") return undefined;
  return metrics;
}

export function foldSessionPerformanceStats(
  messageList: PerformanceMessage[],
): SessionPerformanceStats | undefined {
  const rows: MetricsRow[] = [];
  const seqs = new Set<number>();
  for (const item of messageList) {
    if (item?.role !== RoleTypes.ASSISTANT) continue;
    const seq = asFiniteNumber(item.seq);
    const collected: RunPerformanceMetrics[] = [];
    if (item.performance_metrics && typeof item.performance_metrics === "object") {
      collected.push(item.performance_metrics);
    }
    const own = metricsFromRunTerminal(item.run_terminal);
    if (own) collected.push(own);
    for (const answer of item.answers || []) {
      if (answer.performance_metrics && typeof answer.performance_metrics === "object") {
        collected.push(answer.performance_metrics);
      }
      const nested = metricsFromRunTerminal(answer.run_terminal);
      if (nested) collected.push(nested);
    }
    if (collected.length === 0) continue;
    if (seq != null) seqs.add(seq);
    for (const metrics of collected) {
      rows.push({ seq: seq ?? asFiniteNumber(metrics.turn_seq), metrics });
    }
  }
  if (rows.length === 0) return undefined;

  const metricsRows = rows.map((row) => row.metrics);
  const sum = (pick: (row: RunPerformanceMetrics) => number | undefined) =>
    metricsRows.reduce((total, row) => total + (pick(row) ?? 0), 0);
  const last = metricsRows[metricsRows.length - 1];
  const promptTokens = sum(inputTokens);
  const completionTokens = sum(outputTokens);
  const modelMs = sum((row) => asFiniteNumber(row.model_ms));
  const ttftValues = metricsRows
    .map((row) => asFiniteNumber(row.ttft_ms))
    .filter((value): value is number => value != null);
  const latestContext = [...metricsRows].reverse().find((row) => asFiniteNumber(row.context_ratio) != null);
  const currentTurnSeq = rows.reduce<number | undefined>((latest, row) => {
    if (row.seq == null) return latest;
    return latest == null || row.seq > latest ? row.seq : latest;
  }, undefined);
  const turnRows = currentTurnSeq == null
    ? [last]
    : rows.filter((row) => row.seq === currentTurnSeq).map((row) => row.metrics);

  return {
    turns: seqs.size || rows.length,
    steps: sum((row) => asFiniteNumber(row.steps)),
    model: last.model,
    modelMs,
    toolMs: sum((row) => asFiniteNumber(row.tool_ms)),
    promptTokens: promptTokens || undefined,
    completionTokens: completionTokens || undefined,
    sessionCacheHitRate: weightedCacheHitRate(metricsRows),
    turnCacheHitRate: weightedCacheHitRate(turnRows),
    ttftMs: ttftValues.length ? ttftValues.reduce((a, b) => a + b, 0) / ttftValues.length : undefined,
    tokS: completionTokens && modelMs > 0 ? completionTokens / (modelMs / 1000) : undefined,
    contextRatio: asFiniteNumber(latestContext?.context_ratio),
  };
}
