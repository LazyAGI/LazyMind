import { useState, type ReactNode } from "react";
import { Button, Modal } from "antd";
import { ArrowLeftOutlined, StopOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { useThreadControls } from "../hooks/useThreadControls";

export function EvolutionTaskHeader({ controls, onBack, children }: {
  controls: ReturnType<typeof useThreadControls>;
  onBack: () => void;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const [confirming, setConfirming] = useState(false);
  const { thread, cancelState, canCancel } = controls;
  const summary = thread?.thread_payload?.model_at_creation;
  const displayStatus = thread?.cleanup_pending && thread.runtime_status === "failed"
    ? "cleanup_failed" : thread?.runtime_status === "cancelling" ? "cancelling" : thread?.status;
  const creationModel = summary
    ? t("selfEvolutionControls.creationModel", { model: `${summary.display_name} · ${summary.provider_name}`, source: t(`selfEvolutionControls.source.${summary.source}`) })
    : t("selfEvolutionControls.historyModelMissing");
  return <header className="self-evolution-task-header">
    <div className="self-evolution-task-navigation">
      <Button type="text" className="self-evolution-task-back" icon={<ArrowLeftOutlined aria-hidden />} onClick={onBack}>{t("selfEvolutionControls.back")}</Button>
      <div className="self-evolution-task-status" data-status={thread?.status_source === "live" ? displayStatus : "unknown"} role="status" aria-live="polite">
        <span className="self-evolution-task-status-dot" aria-hidden />
        {thread?.status_source !== "live" ? t("selfEvolutionControls.statusUnknown") : t(`selfEvolutionControls.status.${displayStatus}`, { defaultValue: displayStatus || "" })}
      </div>
    </div>
    {children && <div className="self-evolution-task-context">{children}</div>}
    <div className="self-evolution-task-model" title={creationModel}>
      <span className="self-evolution-task-model-label">{t("selfEvolutionControls.creationModelLabel")}</span>
      {summary ? <div className="self-evolution-task-model-value">
        <strong>{summary.display_name}</strong>
        <span>{summary.provider_name} · {t(`selfEvolutionControls.source.${summary.source}`)}</span>
      </div> : <span className="self-evolution-task-model-missing">{creationModel}</span>}
    </div>
    {canCancel && <Button danger block icon={<StopOutlined aria-hidden />} disabled={cancelState === "sending" || cancelState === "pending"} onClick={() => setConfirming(true)}>{t("selfEvolutionControls.terminate")}</Button>}
    {((thread?.status_source === "cached" && thread.observed_at) || cancelState !== "idle") && <div className="self-evolution-task-notice" aria-live="polite">
      {thread?.status_source === "cached" && thread.observed_at && <span>{t("selfEvolutionControls.lastObserved", { time: new Date(thread.observed_at).toLocaleString() })}</span>}
      {cancelState !== "idle" && <span>{t(`selfEvolutionControls.cancel.${cancelState}`)}</span>}
    </div>}
    <Modal title={t("selfEvolutionControls.confirmTitle")} open={confirming} okText={t("selfEvolutionControls.terminate")} cancelText={t("common.cancel")} okButtonProps={{ danger: true }} onCancel={() => setConfirming(false)} onOk={() => { setConfirming(false); void controls.cancel(); }}>
      <p>{t("selfEvolutionControls.confirmBody")}</p>
    </Modal>
  </header>;
}
