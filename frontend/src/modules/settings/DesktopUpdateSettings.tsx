import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { Alert, Button, Progress, Skeleton, Space } from "antd";
import { DownloadOutlined, FolderOpenOutlined, ReloadOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { desktopUpdateStatus, desktopUpdateCheck, desktopUpdateDownload, desktopUpdateCancel, desktopUpdateReveal, onDesktopUpdateState, type DesktopUpdateState } from "@/runtime/desktopUpdates";
import "./desktopUpdates.scss";

function useDesktopUpdate() {
  const [state, setState] = useState<DesktopUpdateState | null>();
  const [actionError, setActionError] = useState(false);
  const [busy, setBusy] = useState(false);
  const mounted = useRef(true);
  const apply = useCallback((next: DesktopUpdateState | null) => {
    if (!mounted.current) return;
    setState((previous) => !next ? previous ?? null : previous && previous.revision > next.revision ? previous : next);
  }, []);
  useEffect(() => {
    mounted.current = true;
    const unsubscribe = onDesktopUpdateState(apply);
    void desktopUpdateStatus().then(apply).catch(() => { if (mounted.current) setActionError(true); });
    return () => { mounted.current = false; unsubscribe(); };
  }, [apply]);
  const perform = async (operation: () => Promise<DesktopUpdateState | void>) => {
    setActionError(false); setBusy(true);
    try { const next = await operation(); if (next) apply(next); }
    catch { if (mounted.current) setActionError(true); }
    finally { if (mounted.current) setBusy(false); }
  };
  return { state, busy, actionError, perform };
}

function byteSize(value = 0) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = value > 0 ? Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024))) : 0;
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value / 1024 ** index)} ${units[index]}`;
}

export default function DesktopUpdateSettings({ headingRef }: { headingRef?: RefObject<HTMLHeadingElement> }) {
  const { t } = useTranslation();
  const { state, busy, actionError, perform } = useDesktopUpdate();
  const transferring = state?.phase === "downloading" || state?.phase === "cancelling";
  const canDownload = Boolean(state?.releaseId && (state.phase === "available" || state.phase === "error"));
  const percent = state?.totalBytes ? Math.floor(Math.min(100, 100 * (state.downloadedBytes || 0) / state.totalBytes)) : 0;
  const errorText = actionError ? t("desktopUpdates.reasons.check_failed") : state?.reason
    ? t(`desktopUpdates.reasons.${state.reason}`, { defaultValue: t("desktopUpdates.reasons.check_failed") }) : t("desktopUpdates.reasons.check_failed");

  return <div className="desktop-update-settings">
    <header className="settings-detail-header"><div>
      <h1 ref={headingRef} tabIndex={-1}>{t("desktopUpdates.title")}</h1>
      <p>{t("desktopUpdates.description")}</p>
    </div></header>
    {state === undefined && !actionError && <Skeleton active paragraph={{ rows: 2 }} />}
    {state === null && <p>{t("desktopUpdates.desktopOnly")}</p>}
    {(actionError || state?.phase === "error" || state?.phase === "blocked") && <Alert type="warning" showIcon message={errorText} />}
    {state && <div className="settings-detail-group">
      <div className="settings-detail-row"><div>
        <strong>{t("desktopUpdates.currentVersion", { version: state.currentVersion })}</strong>
        {state.phase === "disabled" && <p>{t("desktopUpdates.reasons.not_configured")}</p>}
        {state.phase === "up_to_date" && <p role="status">{t("desktopUpdates.upToDate")}</p>}
        {state.phase === "checking" && <p role="status">{t("desktopUpdates.checking")}</p>}
      </div>
        <Button icon={<ReloadOutlined />} loading={state.phase === "checking"} disabled={transferring || busy || state.phase === "disabled"}
          onClick={() => void perform(desktopUpdateCheck)} aria-label={t("desktopUpdates.check")}>{t("desktopUpdates.check")}</Button>
      </div>
      {state.nextVersion && <div className="settings-detail-row"><div>
        <strong>{t("desktopUpdates.nextVersion", { version: state.nextVersion })}</strong>
        {(state.route?.length || 0) > 1 && <p>{t("desktopUpdates.bridge", { count: state.route!.length, version: state.targetVersion })}</p>}
        {state.totalBytes ? <p>{t("desktopUpdates.downloadSize", { size: byteSize(state.totalBytes) })}</p> : null}
      </div><Space wrap>
        {canDownload && <Button type="primary" icon={<DownloadOutlined />} loading={busy} aria-label={t("desktopUpdates.download")}
          onClick={() => void perform(() => desktopUpdateDownload(state.releaseId!))}>{t("desktopUpdates.download")}</Button>}
        {transferring && <Button disabled={state.phase === "cancelling"} aria-label={t("desktopUpdates.cancel")}
          onClick={() => void perform(desktopUpdateCancel)}>{t("desktopUpdates.cancel")}</Button>}
        {state.phase === "downloaded" && <Button icon={<FolderOpenOutlined />} aria-label={t("desktopUpdates.reveal")}
          onClick={() => void perform(desktopUpdateReveal)}>{t("desktopUpdates.reveal")}</Button>}
      </Space></div>}
      {transferring && <div className="desktop-update-progress" role="status" aria-live="polite">
        <Progress percent={percent} status="active" aria-label={t("desktopUpdates.progress")} />
        <p>{state.phase === "cancelling" ? t("desktopUpdates.cancelling") : t("desktopUpdates.transferred", { received: byteSize(state.downloadedBytes), total: byteSize(state.totalBytes) })}</p>
      </div>}
      {state.phase === "downloaded" && <p className="desktop-update-ready" role="status">{t("desktopUpdates.downloaded")}</p>}
    </div>}
    {actionError && !state && <Button onClick={() => void perform(desktopUpdateCheck)}>{t("desktopUpdates.check")}</Button>}
  </div>;
}

export function DesktopUpdateNotice() {
  const { t } = useTranslation();
  const { state, busy, perform } = useDesktopUpdate();
  if (!state || !["available", "downloading", "cancelling", "downloaded"].includes(state.phase)) return null;
  return <div className="desktop-update-notice" role="status" aria-live="polite">
    <span>{state.phase === "available" ? t("desktopUpdates.available", { version: state.nextVersion })
      : state.phase === "downloaded" ? t("desktopUpdates.noticeDownloaded")
        : state.phase === "cancelling" ? t("desktopUpdates.cancelling") : t("desktopUpdates.downloading")}</span>
    <Space size={12} wrap>
      {state.phase === "available" && state.releaseId && <Button size="small" loading={busy} aria-label={t("desktopUpdates.download")}
        onClick={() => void perform(() => desktopUpdateDownload(state.releaseId!))}>{t("desktopUpdates.download")}</Button>}
      <Link to="/settings?section=updates">{t("desktopUpdates.details")}</Link>
    </Space>
  </div>;
}
