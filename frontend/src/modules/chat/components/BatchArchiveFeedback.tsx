import { useEffect, useRef, useState } from "react";
import { Button, message } from "antd";
import { CheckCircleFilled, CloseOutlined, ExclamationCircleFilled } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { extractErrorCode } from "@/components/request";
import { unarchiveConversation, type ConversationArchiveReceipt } from "@/modules/settings/recoveryApi";
import { emitConversationListRefresh } from "@/modules/chat/utils/conversationActivity";
import { emitConversationGroupsChanged } from "@/modules/chat/conversationOrganizer/api";
import "./BatchArchiveFeedback.scss";

interface BatchArchiveFeedbackOptions {
  count: number;
  receipts: ConversationArchiveReceipt[];
  onViewArchived: () => void;
}

let batchSequence = 0;

export function showBatchArchiveFeedback(options: BatchArchiveFeedbackOptions) {
  const key = `batch-archive:${++batchSequence}`;
  message.open({
    key,
    duration: 0,
    content: <BatchArchiveFeedback {...options} onClose={() => message.destroy(key)} />,
  });
}

function BatchArchiveFeedback({ count, receipts, onViewArchived, onClose }: BatchArchiveFeedbackOptions & { onClose: () => void }) {
  const { t } = useTranslation();
  const pending = useRef(receipts);
  const submitting = useRef(false);
  const [busy, setBusy] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [restored, setRestored] = useState(0);
  const [changed, setChanged] = useState(0);
  const [paused, setPaused] = useState(false);

  useEffect(() => {
    if (paused || attempted) return;
    const timer = window.setTimeout(onClose, 8_000);
    return () => window.clearTimeout(timer);
  }, [attempted, onClose, paused]);

  const undo = async () => {
    if (submitting.current || !pending.current.length) return;
    submitting.current = true;
    setBusy(true);
    setAttempted(true);
    const failed: ConversationArchiveReceipt[] = [];
    let succeeded = 0;
    let conflicts = 0;
    for (const receipt of pending.current) {
      try {
        await unarchiveConversation(receipt.conversation_id, receipt.archived_at);
        succeeded++;
      } catch (error) {
        const status = (error as { response?: { status?: number } })?.response?.status;
        if (extractErrorCode(error) === "2004815" || status === 404) conflicts++;
        else failed.push(receipt);
      }
    }
    pending.current = failed;
    setRestored((value) => value + succeeded);
    setChanged((value) => value + conflicts);
    submitting.current = false;
    setBusy(false);
    if (succeeded) {
      emitConversationListRefresh();
      emitConversationGroupsChanged();
    }
    if (!failed.length && !changed && !conflicts) {
      onClose();
      message.success(t("chat.batchArchiveUndone", { count: restored + succeeded }));
    }
  };

  const canUndo = receipts.length === count && pending.current.length > 0;
  return (
    <span className="batch-archive-feedback" onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setPaused(false); }}>
      {attempted && !busy ? <ExclamationCircleFilled className="batch-archive-feedback-warning" /> : <CheckCircleFilled className="batch-archive-feedback-success" />}
      <span role="status">
        {!attempted ? t("chat.batchArchiveSuccess", { count }) : busy
          ? t("chat.batchArchiveUndoing")
          : pending.current.length ? t("chat.batchArchiveUndoPartial", { restored, failed: pending.current.length })
            : t("chat.batchArchiveUndone", { count: restored })}
        {changed > 0 && !busy && <span className="batch-archive-feedback-detail">{t("chat.batchArchiveUndoChanged", { count: changed })}</span>}
      </span>
      {canUndo && <Button type="link" size="small" loading={busy} aria-busy={busy}
        aria-label={t(attempted && !busy ? "chat.batchArchiveUndoRetry" : "settingsPage.recovery.undo")} onClick={() => void undo()}>
        {t(attempted && !busy ? "chat.batchArchiveUndoRetry" : "settingsPage.recovery.undo")}
      </Button>}
      <Button type="link" size="small" onClick={onViewArchived}>{t("settingsPage.recovery.viewArchived")}</Button>
      <Button type="text" size="small" icon={<CloseOutlined />} disabled={busy} onClick={onClose} aria-label={t("common.close")} />
    </span>
  );
}
