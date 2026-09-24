import { Button, Tooltip } from "antd";
import { FileTextOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import { openConversationArtifactPanel } from "@/modules/chat/constants/chat";

interface Props {
  sessionId: string;
  historyId?: string;
  currentExportIds?: string[];
}

export default function ArtifactDownloadButton({ sessionId, historyId, currentExportIds }: Props) {
  void currentExportIds;
  const { t } = useTranslation();
  const totalArtifactCount = useTaskCenterStore((state) =>
    sessionId ? (state.artifactsByConversation[sessionId] ?? []).length : 0,
  );

  if (!sessionId || !historyId || totalArtifactCount === 0) return null;

  return (
    <Tooltip title={t("chat.artifactPanelOpenMenu")}>
      <Button
        className="tool-btn"
        icon={<FileTextOutlined />}
        onClick={() => openConversationArtifactPanel({ conversationId: sessionId })}
      />
    </Tooltip>
  );
}
