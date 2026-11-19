import { useEffect, useState, useSyncExternalStore } from "react";
import { Button, Progress } from "antd";
import { CloseOutlined } from "@ant-design/icons";
import { isActivePdfJob, type PdfRenderJob } from "@/modules/knowledge/api/pdfArtifacts";

// Progress ticks belong to the notice, not the document workspace. Keep the
// latest snapshot while the notice is hidden so reopening it stays up to date.
export function createPdfTranslationProgress() {
  let snapshot: PdfRenderJob | undefined;
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    update: (job: PdfRenderJob) => { snapshot = job; listeners.forEach(listener => listener()); },
  };
}
const emptyProgress = createPdfTranslationProgress();

interface Props {
  progress?: ReturnType<typeof createPdfTranslationProgress>;
  documentId: string;
  job?: PdfRenderJob;
  preparing: boolean;
  detail: string;
  providerLabel: string;
  onCancel: () => void;
}

export default function PdfTranslationNotice({ documentId, job: initialJob, preparing, detail, providerLabel, onCancel, progress = emptyProgress }: Props) {
  const snapshot = useSyncExternalStore(progress.subscribe, progress.getSnapshot, progress.getSnapshot);
  const job = snapshot?.id === initialJob?.id && isActivePdfJob(initialJob) ? snapshot : initialJob;
  const storageKey = `pdf-translation-dismissed:${documentId}`;
  const [dismissed, setDismissed] = useState("");
  useEffect(() => {
    try { setDismissed(sessionStorage.getItem(storageKey) || ""); } catch { setDismissed(""); }
  }, [storageKey]);
  const noticeId = `${job?.id}:${job?.status}`;
  const terminal = job?.status === "FAILED" || job?.status === "CANCELLED";
  if (!preparing && (!job || job.kind !== "TRANSLATION_PDF" || job.status === "READY" || terminal && dismissed === noticeId)) return null;
  return <div role="status" className={`pdf-processing-card pdf-processing-card--translation is-${preparing ? "waiting_dependency" : job?.status.toLowerCase()}`}>
    <div>
      <strong>{preparing ? "正在准备翻译" : job?.status === "FAILED" ? "翻译失败" : job?.status === "CANCELLED" ? "翻译已取消" : "正在翻译文档"}</strong>
      <span>{terminal && !preparing ? (job?.status === "FAILED" ? "任务已停止，可重试" : "任务已停止") : detail}</span>
      {!preparing && isActivePdfJob(job) ? <Button type="link" danger size="small" onClick={onCancel}>取消</Button> : null}
      {!preparing && terminal ? <Button type="text" size="small" icon={<CloseOutlined />} aria-label="关闭翻译提示" onClick={() => {
        setDismissed(noticeId);
        try { sessionStorage.setItem(storageKey, noticeId); } catch { /* Dismiss still works without storage. */ }
      }} /> : null}
    </div>
    <Progress percent={preparing ? 0 : job?.progress || 0} status={job?.status === "FAILED" ? "exception" : "active"} size="small" />
    <small>{providerLabel}</small>
    {job?.error_message ? <div className="pdf-processing-card__error">{job.error_message}</div> : null}
  </div>;
}
