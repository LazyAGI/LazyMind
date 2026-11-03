import { useEffect, useState } from "react";
import { Button, Progress } from "antd";
import { CloseOutlined } from "@ant-design/icons";
import { isActivePdfJob, type PdfRenderJob } from "@/modules/knowledge/api/pdfArtifacts";

interface Props {
  documentId: string;
  job?: PdfRenderJob;
  preparing: boolean;
  detail: string;
  providerLabel: string;
  onCancel: () => void;
}

export default function PdfTranslationNotice({ documentId, job, preparing, detail, providerLabel, onCancel }: Props) {
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
      <span>{detail}</span>
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
