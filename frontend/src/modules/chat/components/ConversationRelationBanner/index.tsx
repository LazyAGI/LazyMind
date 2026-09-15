import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";

import { getChatConversationPath } from "@/modules/chat/constants/chat";
import {
  CONVERSATION_RELATION_FORK,
  type ConversationRelation,
} from "@/modules/chat/utils/conversationRelation";

import "./index.scss";

interface ConversationRelationBannerProps {
  relation?: ConversationRelation | null;
}

export default function ConversationRelationBanner({
  relation,
}: ConversationRelationBannerProps) {
  const { t } = useTranslation();
  if (!relation) {
    return null;
  }

  const sourceText = relation.canLocate === false && !relation.parentDisplayName ? t("chat.fork.sourceUnavailable") : t(
    relation.relationType === CONVERSATION_RELATION_FORK
      ? "chat.conversationForkedFrom"
      : "chat.conversationSourceFrom",
    { parent: relation.parentDisplayName },
  );

  return (
    <section
      className="conversation-relation-banner"
      aria-label={t("chat.conversationRelationBannerLabel")}
    >
      <span className="conversation-relation-banner__source" title={sourceText}>
        {sourceText}
      </span>
      {relation.canLocate !== false && <Link
        className="conversation-relation-banner__back"
        to={getChatConversationPath(relation.parentConversationId) + (relation.sourceHistoryId ? `?anchor_history_id=${encodeURIComponent(relation.sourceHistoryId)}` : "")}
      >
        {t(relation.sourceHistoryId ? "chat.fork.locateSource" : "chat.returnToParentConversation")}
      </Link>}
      {relation.sourceStatus && relation.sourceStatus !== "available" && <span>{t(`chat.fork.source.${relation.sourceStatus}`)}</span>}
    </section>
  );
}
