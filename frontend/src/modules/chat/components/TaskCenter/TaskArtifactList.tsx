import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Tooltip } from "antd";
import { DownOutlined, FileTextOutlined, RightOutlined } from "@ant-design/icons";
import type { PublicArtifact } from "@/modules/chat/types/ordinaryTask";
import { FilePreviewDrawer } from "../WorkflowPanel/FilePreviewDrawer";
import { downloadArtifactFile } from "@/modules/chat/utils/artifactFileActions";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";

function usableURL(raw?: string) {
  if (!raw) return "";
  if (/^\/(?:api\/core\/|static-files\/)/.test(raw) && !raw.includes("\\")) {
    return raw.startsWith("/static-files/") ? resolveCoreAssetUrl(raw) : raw;
  }
  try {
    const parsed = new URL(raw);
    return ["https:", "http:"].includes(parsed.protocol) && !parsed.username && !parsed.password ? raw : "";
  } catch { return ""; }
}

function fileSize(size: number | null) {
  if (size === null || !Number.isFinite(size) || size < 0) return null;
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function ArtifactRow({ artifact, onReload }: { artifact: PublicArtifact; onReload?: () => Promise<unknown> }) {
  const { t } = useTranslation();
  const [preview, setPreview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const ready = artifact.state === "ready";
  const previewUrl = usableURL(artifact.preview_url);
  const openUrl = usableURL(artifact.open_url);
  const downloadUrl = usableURL(artifact.download_url);
  const canPreview = ready && artifact.capabilities.preview && (Boolean(previewUrl) || artifact.inline_content !== undefined);
  const canDownload = ready && artifact.capabilities.download && (Boolean(downloadUrl) || artifact.inline_content !== undefined);
  const execute = async (action: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true); setError(false);
    try { await action(); } catch {
      globalThis.performance?.mark?.("lazymind.task_display.artifact_action_failed");
      if (mounted.current) setError(true);
    }
    finally { if (mounted.current) setBusy(false); }
  };
  const download = async () => {
    let blobUrl: string | undefined;
    try {
      if (!downloadUrl && artifact.inline_content !== undefined) {
        blobUrl = URL.createObjectURL(new Blob([artifact.inline_content], { type: artifact.content_type || "text/plain" }));
      }
      await downloadArtifactFile(downloadUrl || blobUrl || "", artifact.name);
    } finally {
      // Give the browser download navigation time to consume an inline URL.
      if (blobUrl) window.setTimeout(() => URL.revokeObjectURL(blobUrl!), 1000);
    }
  };
  return <li className={`ordinary-artifact-row is-${artifact.state}`}>
    <div className="ordinary-artifact-info">
      <Tooltip title={artifact.name} placement="topLeft" trigger={["hover", "focus"]} styles={{ body: { overflowWrap: "anywhere" } }}>
        {canPreview ? <button type="button" className="ordinary-artifact-name" onClick={() => setPreview(true)}>{artifact.name}</button>
          : <strong className="ordinary-artifact-name" tabIndex={0}>{artifact.name}</strong>}
      </Tooltip>
      <span>{artifact.content_type || t("taskCenter.ordinaryUnknownType")} · {fileSize(artifact.size_bytes) ?? t("taskCenter.ordinaryUnknownSize")}</span>
      {!ready && <span role="status">{t(`taskCenter.ordinaryArtifactState_${artifact.state}`)}</span>}
    </div>
    <div className="ordinary-artifact-actions">
      {canPreview && <button type="button" onClick={() => setPreview(true)} aria-label={`${t("taskCenter.ordinaryPreview")} ${artifact.name}`}>{t("taskCenter.ordinaryPreview")}</button>}
      {ready && artifact.capabilities.open && openUrl && <a href={openUrl} target="_blank" rel="noopener noreferrer" aria-label={`${t("common.open")} ${artifact.name}`}>{t("common.open")}</a>}
      {canDownload && <button type="button" disabled={busy} onClick={() => void execute(download)} aria-label={`${t("taskCenter.download")} ${artifact.name}`}>{t("taskCenter.download")}</button>}
    </div>
    {error && <span className="ordinary-artifact-error" role="alert">{t("taskCenter.ordinaryArtifactActionFailed")}</span>}
    {(error || artifact.state === "unavailable" || artifact.state === "failed") && onReload && <button type="button" disabled={busy} onClick={() => void execute(onReload)}>{t("taskCenter.ordinaryReload")}</button>}
    {preview && <FilePreviewDrawer open filename={artifact.name} url={previewUrl} content={artifact.inline_content} onClose={() => setPreview(false)} />}
  </li>;
}

export function TaskArtifactList({ artifacts, final = false, onReload }: {
  artifacts: PublicArtifact[]; final?: boolean; onReload?: () => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const headingId = useId();
  const listId = useId();
  const [expanded, setExpanded] = useState(true);
  const unique = [...new Map(artifacts.map(artifact => [artifact.artifact_id, artifact])).values()];
  if (!unique.length) return null;
  const heading = <><FileTextOutlined aria-hidden="true" />
    <span>{t(final ? "taskCenter.ordinaryFinalArtifacts" : "taskCenter.ordinaryStageArtifacts")}</span>
    <span className="ordinary-section-count">· {unique.length}</span>
  </>;
  return <section className="ordinary-activity-section ordinary-artifact-section" aria-labelledby={headingId}>
    <h3 className="ordinary-section-heading" id={headingId}>
      {final ? <button type="button" className="ordinary-artifact-toggle" aria-expanded={expanded} aria-controls={listId}
        onClick={() => setExpanded(value => !value)}>
        {heading}
        {expanded ? <DownOutlined className="ordinary-artifact-chevron" aria-hidden="true" />
          : <RightOutlined className="ordinary-artifact-chevron" aria-hidden="true" />}
      </button> : heading}
    </h3>
    <ul className="ordinary-artifact-list" id={listId} hidden={final && !expanded}>{unique.map(artifact => <ArtifactRow key={artifact.artifact_id} artifact={artifact} onReload={onReload} />)}</ul>
  </section>;
}
