import type { TraceObservationViewProps } from "./trace/types";
import { TraceComparePanel } from "./trace/TraceComparePanel";
import { TraceDetailWorkspace } from "./trace/TraceDetailPanel";

export type {
  TraceDetailObservation,
  TraceObservation,
} from "./trace/types";
export { normalizeTraceObservation } from "./trace/normalize";

export function TraceObservationView({ observation, title }: TraceObservationViewProps) {
  if (observation.kind === "compare") {
    return <TraceComparePanel observation={observation} title={title} />;
  }

  return <TraceDetailWorkspace detail={observation.detail} title={title} />;
}
