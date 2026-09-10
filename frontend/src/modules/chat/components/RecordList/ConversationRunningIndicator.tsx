import { LoadingOutlined, QuestionCircleOutlined } from "@ant-design/icons";
import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { useConversationRunningStore } from "@/modules/chat/store/conversationRunning";

export default function ConversationRunningIndicator({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation();
  const status = useConversationRunningStore((state) => state.entries[conversationId]?.status);
  if (!status || status === "idle") return null;
  const label = t(status === "running" ? "chat.conversationRunning" : "chat.conversationStatusUnavailable");
  return (
    <Tooltip title={label}>
      <span className={`record-running-indicator record-running-indicator--${status}`} role="img" aria-label={label} tabIndex={0}>
        {status === "running" ? <LoadingOutlined aria-hidden="true" /> : <QuestionCircleOutlined aria-hidden="true" />}
      </span>
    </Tooltip>
  );
}
