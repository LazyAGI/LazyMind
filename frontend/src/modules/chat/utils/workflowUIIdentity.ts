/** UI definitions belong to an immutable package revision and a locale, not a workflow name. */
export function workflowUICacheKey(workflowId: string, revisionId: string | undefined, language: string): string {
  return JSON.stringify([workflowId, revisionId || '', language]);
}
