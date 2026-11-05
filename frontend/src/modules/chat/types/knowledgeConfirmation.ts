export interface KnowledgeResourcePreview {
  name: string;
  path: string;
  resource_ref?: { kind: "knowledge_base" | "folder" | "document"; knowledge_base_id?: string };
  document_count?: number;
  folder_count?: number;
}

export interface KnowledgeConfirmation {
  confirmation_id: string;
  arguments_digest: string;
  snapshot: string;
  action: "knowledge.create" | "knowledge.folder.create" | "knowledge.resource.move" | "knowledge.resource.delete";
  status: "pending" | "approved" | "rejected" | "superseded" | "expired";
  steering_segment: number;
  expires_at?: number;
  preview: {
    title?: string;
    impact?: string;
    destination?: KnowledgeResourcePreview;
    resources?: KnowledgeResourcePreview[];
    document_count?: number;
    folder_count?: number;
    creation?: {
      name?: string;
      description?: string;
      tags?: string[];
      scene?: string;
      processing_level?: string;
      paths?: string[];
    };
  };
}

export interface LegacyKnowledgeAsk {
  knowledge_management?: Pick<KnowledgeConfirmation, "confirmation_id" | "arguments_digest" | "snapshot">;
  knowledge_management_preview?: KnowledgeConfirmation["preview"];
}

export function legacyKnowledgeConfirmation(
  ask?: LegacyKnowledgeAsk,
  answered?: boolean,
  answers?: Record<number, { value?: unknown }>,
): KnowledgeConfirmation | undefined {
  if (!ask?.knowledge_management) return;
  const preview = ask.knowledge_management_preview ?? {};
  const action = preview.creation?.name ? "knowledge.create"
    : preview.creation?.paths ? "knowledge.folder.create"
      : preview.destination ? "knowledge.resource.move" : "knowledge.resource.delete";
  return {
    ...ask.knowledge_management,
    preview,
    action,
    status: !answered ? "expired" : answers?.[0]?.value === "__ask_user_yes__" ? "approved" : "rejected",
    steering_segment: 0,
  };
}
