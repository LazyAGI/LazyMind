import { CheckCircleOutlined, CloseCircleOutlined, LoadingOutlined, QuestionCircleOutlined, StopOutlined } from "@ant-design/icons";
import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { useConversationRunningStore } from "@/modules/chat/store/conversationRunning";

export default function ConversationRunningIndicator({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation();
  const entry = useConversationRunningStore((state) => state.entries[conversationId]);
  const status = entry?.status === "idle" ? (entry.terminalRead ? undefined : entry.terminalStatus) : entry?.status;
  if (!status) return null;
  const labels = {
    running: "chat.conversationRunning",
    unknown: "chat.conversationStatusUnavailable",
    completed: "chat.conversationCompleted",
    failed: "chat.conversationFailed",
    canceled: "chat.conversationCanceled",
  };
  const icons = {
    running: LoadingOutlined,
    unknown: QuestionCircleOutlined,
    completed: CheckCircleOutlined,
    failed: CloseCircleOutlined,
    canceled: StopOutlined,
  };
  const label = t(labels[status]);
  const Icon = icons[status];
  return (
    <Tooltip title={label}>
      <span className={`record-running-indicator record-running-indicator--${status}`} role="img" aria-label={label} tabIndex={0}>
        <Icon aria-hidden="true" />
      </span>
    </Tooltip>
  );
}
