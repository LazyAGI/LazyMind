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

  const sourceText = t(
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
      <Link
        className="conversation-relation-banner__back"
        to={getChatConversationPath(relation.parentConversationId)}
      >
        {t("chat.returnToParentConversation")}
      </Link>
    </section>
  );
}
