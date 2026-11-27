type FlushEdits = () => Promise<boolean>;
const editorsBySession = new Map<string, Set<FlushEdits>>();

/** Share mounted panel editors with controls outside the panel, scoped to the run. */
export function registerWorkflowEditFlush(sessionId: string, flush: FlushEdits): () => void {
  const editors = editorsBySession.get(sessionId) ?? new Set<FlushEdits>();
  editors.add(flush);
  editorsBySession.set(sessionId, editors);
  return () => {
    editors.delete(flush);
    if (!editors.size) editorsBySession.delete(sessionId);
  };
}

export async function flushWorkflowEdits(sessionId: string): Promise<boolean> {
  const results = await Promise.all([...editorsBySession.get(sessionId) ?? []].map(flush => flush()));
  return results.every(Boolean);
}
