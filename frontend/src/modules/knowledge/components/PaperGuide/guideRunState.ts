const activeStages = [
  "queued",
  "waiting_source",
  "analyzing",
  "writing",
  "reviewing",
];

export function isGuideRunActive(stage?: string): boolean {
  return stage !== undefined && activeStages.includes(stage);
}
