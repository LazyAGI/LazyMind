import { CloudUploadOutlined, LockOutlined, ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Switch } from "antd";

import type { CredentialBackupStatus } from "../credentialBackupModel";

import { useCredentialBackupPanel } from "../hooks/useCredentialBackupPanel";

type CredentialBackupPanelProps = {
  available: boolean;
  loading: boolean;
  status: CredentialBackupStatus;
  onRetry: () => void;
  onToggle: (enabled: boolean) => void;
};

export function CredentialBackupPanel({ available, loading, status, onRetry, onToggle }: CredentialBackupPanelProps) {
  const { t, view, lastSucceeded } = useCredentialBackupPanel(status);

  return (
    <section className={`credential-backup-panel is-${view.summary}`} aria-labelledby="credential-backup-title">
      <div className="credential-backup-heading">
        <div className="credential-backup-title-wrap">
          <span className="credential-backup-icon" aria-hidden="true"><CloudUploadOutlined /></span>
          <div>
            <h2 id="credential-backup-title">{t("modelProvider.credentialBackup.title")}</h2>
            <p>{t("modelProvider.credentialBackup.description")}</p>
          </div>
        </div>
        <div className="credential-backup-toggle">
          <span>{view.enabled ? t("common.enabled") : t("common.disabled")}</span>
          <Switch
            aria-label={t("modelProvider.credentialBackup.toggleAria")}
            checked={view.enabled}
            disabled={!available || loading}
            loading={loading}
            onChange={onToggle}
          />
        </div>
      </div>

      {!available ? (
        <Alert
          action={<Button icon={<ReloadOutlined />} size="small" onClick={onRetry}>{t("common.retry")}</Button>}
          message={t("modelProvider.credentialBackup.unavailableTitle")}
          description={t("modelProvider.credentialBackup.unavailableDescription")}
          showIcon
          type="warning"
        />
      ) : (
        <>
          <dl className="credential-backup-stats" aria-live="polite">
            <div><dt>{t("modelProvider.credentialBackup.backedUpCount")}</dt><dd>{view.backedUp}</dd></div>
            <div><dt>{t("modelProvider.credentialBackup.pendingCount")}</dt><dd>{view.pending}</dd></div>
            <div><dt>{t("modelProvider.credentialBackup.failedCount")}</dt><dd>{view.failed}</dd></div>
            <div className="credential-backup-last"><dt>{t("modelProvider.credentialBackup.lastSucceeded")}</dt><dd>{lastSucceeded}</dd></div>
          </dl>
          <p className="credential-backup-security-note"><LockOutlined />{t("modelProvider.credentialBackup.securityNote")}</p>
          {view.summary === "failed" ? (
            <Alert message={t("modelProvider.credentialBackup.failedTitle")} description={t("modelProvider.credentialBackup.failedDescription")} showIcon type="error" />
          ) : view.summary === "pending" ? (
            <Alert message={t("modelProvider.credentialBackup.pendingTitle")} description={t("modelProvider.credentialBackup.pendingDescription")} showIcon type="info" />
          ) : null}
        </>
      )}
    </section>
  );
}
