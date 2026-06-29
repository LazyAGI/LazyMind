export type TFunction = (key: string, options?: Record<string, unknown>) => string;

export type TracePayloadPreview = {
  kind?: string;
  summary?: string;
  data?: unknown;
};

export type TraceNode = {
  id: string;
  name: string;
  type: string;
  status: string;
  latencyMs?: number;
  input?: TracePayloadPreview;
  output?: TracePayloadPreview;
  metadata?: Record<string, unknown>;
  children: TraceNode[];
};

export type TraceSummary = {
  status: string;
  latencyMs?: number;
  roundCount?: number;
  toolCallCount?: number;
  retrievalCount?: number;
  rerankCount?: number;
  nodeCount: number;
};

export type TraceDetailObservation = {
  traceId: string;
  query: string;
  status: string;
  summary: TraceSummary;
  root: TraceNode;
};

export type TraceObservation =
  | {
    kind: "detail";
    detail: TraceDetailObservation;
  }
  | {
    kind: "compare";
    query: string;
    a: TraceDetailObservation;
    b: TraceDetailObservation;
  };

export type TraceObservationViewProps = {
  observation: TraceObservation;
  title: string;
};

export type FlatTraceNode = {
  node: TraceNode;
  depth: number;
};

export type MetricItem = {
  key: string;
  label: string;
  value: string;
};

export type TraceDocPreview = {
  key: string;
  title: string;
  text: string;
  score?: number;
  ref?: string;
};
