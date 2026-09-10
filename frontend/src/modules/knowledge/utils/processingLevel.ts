export type ProcessingLevel = "stored" | "parsed" | "chunked" | "indexed";

export const PROCESSING_LEVEL_ORDER: ProcessingLevel[] = [
  "stored",
  "parsed",
  "chunked",
  "indexed",
];

export function effectiveProcessingLevel(
  level?: ProcessingLevel,
): ProcessingLevel {
  return level || "indexed";
}

export function isProcessingLevelDowngrade(
  current: ProcessingLevel,
  next: ProcessingLevel,
) {
  return (
    PROCESSING_LEVEL_ORDER.indexOf(next) <
    PROCESSING_LEVEL_ORDER.indexOf(current)
  );
}

export function processingLevelSupportsSegments(level?: ProcessingLevel) {
  return ["chunked", "indexed"].includes(effectiveProcessingLevel(level));
}
