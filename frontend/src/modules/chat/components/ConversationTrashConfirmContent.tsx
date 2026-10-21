import { useTranslation } from "react-i18next";
import "./ConversationTrashConfirmContent.scss";

export default function ConversationTrashConfirmContent({ name, showForkNotice = false }: {
  name?: string;
  showForkNotice?: boolean;
}) {
  const { t } = useTranslation();
  const displayName = name?.trim() || t("settingsPage.recovery.unnamedConversation");

  return <div className="conversation-trash-confirm-content">
    <p className="conversation-trash-confirm-name" title={displayName}>{displayName}</p>
    <p>{t("settingsPage.recovery.moveToTrashDescription")}</p>
    {showForkNotice && <p>{t("chat.fork.deleteNotice")}</p>}
  </div>;
}
