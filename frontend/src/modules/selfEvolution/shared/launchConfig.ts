import { FIXED_EVAL_SET } from "./constants";
import type { ExtraEvalStrategy } from "./types";

export function getExtraEvalStrategy(selectedEvalSet?: string): ExtraEvalStrategy | undefined {
  if (!selectedEvalSet) return undefined;
  return selectedEvalSet === FIXED_EVAL_SET ? "generate" : "skip";
}
