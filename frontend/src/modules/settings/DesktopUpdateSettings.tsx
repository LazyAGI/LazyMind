import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { Alert, Button, Progress, Skeleton, Space } from "antd";
import { DownloadOutlined, FolderOpenOutlined, ReloadOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { desktopUpdateStatus, desktopUpdateCheck, desktopUpdateDownload, desktopUpdateCancel, desktopUpdateReveal, desktopUpdateResume, desktopUpdateInstall, desktopUpdateLater, onDesktopUpdateState, type DesktopUpdateState } from "@/runtime/desktopUpdates";
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
  const canDownload = Boolean(!state?.pendingDownload && !state?.pendingUpdate && !state?.updating && state?.releaseId && (state.phase === "available" || state.phase === "error"));
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
    {(actionError || state?.phase === "error" || state?.phase === "blocked" || state?.phase === "repair") && <Alert type="warning" showIcon message={errorText} />}
    {state && <div className="settings-detail-group">
      <div className="settings-detail-row"><div>
        <strong>{t("desktopUpdates.currentVersion", { version: state.currentVersion })}</strong>
        {state.phase === "disabled" && <p>{t("desktopUpdates.reasons.not_configured")}</p>}
        {state.phase === "up_to_date" && <p role="status">{t("desktopUpdates.upToDate")}</p>}
        {state.phase === "checking" && <p role="status">{t("desktopUpdates.checking")}</p>}
        {state.phase === "installed" && <p role="status">{t("desktopUpdates.installed", { version: state.currentVersion })}</p>}
      </div>
        <Button icon={<ReloadOutlined />} loading={state.phase === "checking"} disabled={transferring || busy || state.pendingDownload || state.pendingUpdate || state.updating || state.phase === "repair" || state.phase === "downloaded" || state.phase === "disabled"}
          onClick={() => void perform(desktopUpdateCheck)} aria-label={t("desktopUpdates.check")}>{t("desktopUpdates.check")}</Button>
      </div>
      {(state.nextVersion || state.pendingDownload) && <div className="settings-detail-row"><div>
        {state.nextVersion && <strong>{t("desktopUpdates.nextVersion", { version: state.nextVersion })}</strong>}
        {(state.pendingDownload || state.pendingUpdate) && <p role="status">{t(state.pendingUpdate ? "desktopUpdates.interruptedUpdate" : "desktopUpdates.interrupted")}</p>}
        {["preparing", "waiting", "countdown", "installing"].includes(state.phase) && <p role="status">{t(`desktopUpdates.${state.phase}`, { count: state.secondsRemaining })}</p>}
        {state.phase === "repair" && <p>{t("desktopUpdates.repair")}</p>}
        {(state.route?.length || 0) > 1 && <p>{t("desktopUpdates.bridge", { count: state.route!.length, version: state.targetVersion })}</p>}
        {state.totalBytes ? <p>{t("desktopUpdates.downloadSize", { size: byteSize(state.totalBytes) })}</p> : null}
      </div><Space wrap>
        {state.pendingUpdate && state.phase !== "repair" && <>
          <Button type="primary" loading={busy} onClick={() => void perform(() => desktopUpdateInstall(state.releaseId))}>{t("desktopUpdates.continueUpdate")}</Button>
          <Button onClick={() => void perform(desktopUpdateLater)}>{t("desktopUpdates.later")}</Button>
        </>}
        {state.pendingDownload && !state.pendingUpdate && <>
          <Button type="primary" loading={busy || state.phase === "checking"} onClick={() => void perform(desktopUpdateResume)}>{t("desktopUpdates.resumeDownload")}</Button>
          <Button onClick={() => void perform(desktopUpdateCancel)}>{t("desktopUpdates.cancelUpdate")}</Button>
        </>}
        {canDownload && <Button type="primary" icon={<DownloadOutlined />} loading={busy} aria-label={t(state.canInstall ? "desktopUpdates.install" : "desktopUpdates.download")}
          onClick={() => void perform(() => state.canInstall ? desktopUpdateInstall(state.releaseId!) : desktopUpdateDownload(state.releaseId!))}>{t(state.canInstall ? "desktopUpdates.install" : "desktopUpdates.download")}</Button>}
        {transferring && !state.updating && <Button disabled={state.phase === "cancelling"} aria-label={t("desktopUpdates.cancel")}
          onClick={() => void perform(desktopUpdateCancel)}>{t("desktopUpdates.cancel")}</Button>}
        {state.updating && state.phase !== "installing" && <Button loading={busy} onClick={() => void perform(desktopUpdateLater)}>{t("desktopUpdates.later")}</Button>}
        {state.phase === "downloaded" && state.canInstall && !state.updating && <Button type="primary" loading={busy} onClick={() => void perform(() => desktopUpdateInstall(state.releaseId))}>{t("desktopUpdates.install")}</Button>}
        {(state.phase === "downloaded" || state.packageAvailable) && <Button icon={<FolderOpenOutlined />} aria-label={t("desktopUpdates.reveal")}
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
  const { state, busy, actionError, perform } = useDesktopUpdate();
  if (!state || (!state.pendingDownload && !state.pendingUpdate && !["available", "downloading", "cancelling", "downloaded", "error", "blocked", "preparing", "waiting", "countdown", "installing", "installed", "repair"].includes(state.phase))) return null;
  const failed = state.phase === "error" || state.phase === "blocked" || state.phase === "repair";
  const failureText = actionError ? t("desktopUpdates.reasons.check_failed") : failed
    ? t(`desktopUpdates.reasons.${state.reason}`, { defaultValue: t("desktopUpdates.reasons.check_failed") }) : null;
  return <div className="desktop-update-notice" role="status" aria-live="polite">
    <span>{failureText || (state.pendingUpdate ? t("desktopUpdates.interruptedUpdate") : ["preparing", "waiting", "countdown", "installing", "installed"].includes(state.phase) ? t(`desktopUpdates.${state.phase}`, { count: state.secondsRemaining, version: state.currentVersion }) : state.pendingDownload ? t("desktopUpdates.interrupted") : state.phase === "available" ? t("desktopUpdates.available", { version: state.nextVersion })
      : state.phase === "downloaded" ? t("desktopUpdates.noticeDownloaded")
        : state.phase === "cancelling" ? t("desktopUpdates.cancelling") : t("desktopUpdates.downloading"))}</span>
    <Space size={12} wrap>
      {state.updating && state.phase !== "installing" && <Button size="small" loading={busy} onClick={() => void perform(desktopUpdateLater)}>{t("desktopUpdates.later")}</Button>}
      {state.pendingUpdate && state.phase !== "repair" && <Button size="small" loading={busy} onClick={() => void perform(() => desktopUpdateInstall(state.releaseId))}>{t("desktopUpdates.continueUpdate")}</Button>}
      {state.pendingDownload && !state.pendingUpdate && <>
        <Button size="small" loading={busy || state.phase === "checking"} onClick={() => void perform(desktopUpdateResume)}>{t("desktopUpdates.resumeDownload")}</Button>
        <Button size="small" onClick={() => void perform(desktopUpdateCancel)}>{t("desktopUpdates.cancelUpdate")}</Button>
      </>}
      {!state.pendingDownload && !state.pendingUpdate && !state.updating && ["available", "error"].includes(state.phase) && state.releaseId && <Button size="small" loading={busy} aria-label={t(state.canInstall ? "desktopUpdates.install" : "desktopUpdates.download")}
        onClick={() => void perform(() => state.canInstall ? desktopUpdateInstall(state.releaseId!) : desktopUpdateDownload(state.releaseId!))}>{t(state.canInstall ? "desktopUpdates.install" : "desktopUpdates.download")}</Button>}
      {!state.pendingDownload && !state.pendingUpdate && failed && state.phase !== "repair" && (!state.releaseId || state.phase === "blocked") && <Button size="small" loading={busy} onClick={() => void perform(desktopUpdateCheck)}>{t("desktopUpdates.check")}</Button>}
      <Link to="/settings?section=updates">{t("desktopUpdates.details")}</Link>
    </Space>
  </div>;
}
