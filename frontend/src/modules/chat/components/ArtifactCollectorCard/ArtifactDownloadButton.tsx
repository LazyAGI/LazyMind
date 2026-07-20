import { useCallback, useEffect, useRef, useState } from "react";
import { Button, Popover, Tooltip } from "antd";
import type { TooltipRef } from "antd/es/tooltip";
import { DownloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import ArtifactCollectorCard from ".";

interface Props {
  sessionId: string;
  historyId?: string;
}

export default function ArtifactDownloadButton({ sessionId, historyId }: Props) {
  const { t } = useTranslation();
  const popoverRef = useRef<TooltipRef>(null);
  const [open, setOpen] = useState(false);
  const totalArtifactCount = useTaskCenterStore((state) =>
    sessionId ? (state.artifactsByConversation[sessionId] ?? []).length : 0,
  );

  const realign = useCallback(() => {
    window.requestAnimationFrame(() => popoverRef.current?.forceAlign());
  }, []);

  useEffect(() => setOpen(false), [sessionId, historyId]);
  useEffect(() => {
    if (totalArtifactCount === 0) setOpen(false);
  }, [totalArtifactCount]);

  if (!sessionId || !historyId || totalArtifactCount === 0) return null;

  return (
    <Popover
      ref={popoverRef}
      trigger="click"
      placement="topLeft"
      autoAdjustOverflow
      open={open}
      onOpenChange={setOpen}
      destroyOnHidden
      overlayClassName="artifact-collector-popover"
      content={
        <ArtifactCollectorCard
          sessionId={sessionId}
          historyId={historyId}
          onClose={() => setOpen(false)}
          onLayoutChange={realign}
        />
      }
    >
      <span className="artifact-download-trigger">
        <Tooltip title={t("chat.artifactDownloadButton")}>
          <Button className="tool-btn" icon={<DownloadOutlined />} />
        </Tooltip>
      </span>
    </Popover>
  );
}
