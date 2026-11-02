export interface KnowledgeOperation {
  call_id: string;
  action: string;
  status: string;
  summary?: { total: number; succeeded: number; failed: number; skipped: number; unconfirmed: number };
  processing_summary?: Record<string, number>;
  items: Array<{
    input_index?: number;
    task_id?: string;
    processing_error?: { stage: string; code: string; message: string; task_id?: string; recovery: string };
    name: string;
    resource_ref: {
      kind: "knowledge_base" | "document" | "folder";
      knowledge_base_id: string;
      document_id?: string;
      folder_id?: string;
    };
    resource_status: string;
    processing_status?: string;
    cleanup_status?: string;
    error?: { code?: string; message?: string };
  }>;
}

export function mergeKnowledgeOperations(
  previous: KnowledgeOperation[] = [],
  incoming?: KnowledgeOperation,
): KnowledgeOperation[] {
  if (!incoming?.call_id || !Array.isArray(incoming.items)) return previous;
  const next = previous.filter(item => item.call_id !== incoming.call_id);
  return [...next, incoming];
}

export function knowledgeResourceHref(item: KnowledgeOperation["items"][number]) {
  const ref = item.resource_ref;
  if (!ref?.knowledge_base_id || item.resource_status === "deleted") return undefined;
  const base = `/lib/knowledge/detail/${encodeURIComponent(ref.knowledge_base_id)}`;
  if (ref.kind === "knowledge_base") return base;
  if (ref.kind === "document" && ref.document_id) {
    return `/lib/knowledge/knowledge/${encodeURIComponent(ref.knowledge_base_id)}/${encodeURIComponent(ref.document_id)}`;
  }
  if (ref.kind === "folder" && ref.folder_id) {
    return base;
  }
  return undefined;
}
