import type { Conversation } from "@/api/generated/chatbot-client";

export const CONVERSATION_RELATION_SIDECHAT = "sidechat";
export const CONVERSATION_RELATION_FORK = "fork";

export type ConversationRelationType =
  | typeof CONVERSATION_RELATION_SIDECHAT
  | typeof CONVERSATION_RELATION_FORK;

export interface ConversationRelationFields {
  parent_conversation_id?: string | null;
  relation_type?: string | null;
  source_history_id?: string | null;
  source_seq?: number | null;
  selected_text?: string | null;
  parent_display_name?: string | null;
}

export type ConversationWithRelation =
  Conversation & ConversationRelationFields;
type ConversationRelationInput = ConversationRelationFields | Conversation;

export interface ConversationRelation {
  parentConversationId: string;
  parentDisplayName: string;
  relationType: ConversationRelationType | null;
}

function normalizeRelationType(
  value?: string | null,
): ConversationRelationType | null {
  const normalized = value?.trim().toLowerCase();
  if (normalized === CONVERSATION_RELATION_SIDECHAT) {
    return CONVERSATION_RELATION_SIDECHAT;
  }
  if (normalized === CONVERSATION_RELATION_FORK) {
    return CONVERSATION_RELATION_FORK;
  }
  return null;
}

export function getConversationRelation(
  conversation?: ConversationRelationInput | null,
): ConversationRelation | null {
  const relationFields = conversation as ConversationRelationFields | undefined;
  const parentConversationId = relationFields?.parent_conversation_id?.trim();
  if (!parentConversationId) {
    return null;
  }
  return {
    parentConversationId,
    parentDisplayName:
      relationFields?.parent_display_name?.trim() || parentConversationId,
    relationType: normalizeRelationType(relationFields?.relation_type),
  };
}

export function isChildConversation(
  conversation?: ConversationRelationInput | null,
) {
  return Boolean(
    (conversation as ConversationRelationFields | undefined)
      ?.parent_conversation_id?.trim(),
  );
}

// Right-side sidechat entry points can consume this helper without duplicating
// relationship rules. A child conversation cannot create another sidechat.
export function canStartSidechat(
  conversation?: ConversationRelationInput | null,
) {
  return !(
    conversation as ConversationRelationFields | undefined
  )?.parent_conversation_id?.trim();
}
