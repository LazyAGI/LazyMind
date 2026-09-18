import { ClockCircleOutlined, CloudSyncOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { Alert, Button, Modal, Progress, Radio, Space, Tag } from "antd";
import { useCredentialRestorePanel, type CredentialRestorePanelProps } from "../hooks/useCredentialRestorePanel";

export function CredentialRestorePanel({ loading, status, onCancel, onRefresh, onStart }: CredentialRestorePanelProps) {
  const { t, view, progress, confirming, mode, setMode, confirmRestore, openConfirmation, closeConfirmation, saveCopy, replaceLocal } = useCredentialRestorePanel({ status, onStart });

  return (
    <section className={`credential-restore-panel is-${view.state}`} aria-labelledby="credential-restore-title">
      <div className="credential-restore-heading">
        <div className="credential-restore-title-wrap">
          <span className="credential-restore-icon" aria-hidden="true"><CloudSyncOutlined /></span>
          <div>
            <div className="credential-restore-title-line">
              <h2 id="credential-restore-title">{t("modelProvider.credentialRestore.title")}</h2>
              {view.backupCount > 0 ? <Tag>{t("modelProvider.credentialRestore.backupCount", { count: view.backupCount })}</Tag> : null}
            </div>
            <p>{t("modelProvider.credentialRestore.description")}</p>
          </div>
        </div>
        <Button loading={loading} onClick={onRefresh}>{t("common.refresh")}</Button>
      </div>

      {view.state === "unavailable" ? (
        <Alert
          action={<Button size="small" onClick={onRefresh}>{t("common.retry")}</Button>}
          description={t("modelProvider.credentialRestore.unavailableDescription")}
          message={t("modelProvider.credentialRestore.unavailableTitle")}
          showIcon
          type="warning"
        />
      ) : view.state === "empty" ? (
        <div className="credential-restore-empty" role="status">
          <SafetyCertificateOutlined aria-hidden="true" />
          <div><strong>{t("modelProvider.credentialRestore.emptyTitle")}</strong><p>{t("modelProvider.credentialRestore.emptyDescription")}</p></div>
        </div>
      ) : view.state === "progress" ? (
        <div className="credential-restore-progress" aria-live="polite">
          <div className="credential-restore-progress-copy">
            <strong>{t("modelProvider.credentialRestore.progressTitle")}</strong>
            <span>{t("modelProvider.credentialRestore.progressCount", { completed: view.completedRecords, total: view.totalRecords })}</span>
          </div>
          <Progress percent={progress} showInfo={false} size="small" status="active" />
          <Button disabled={loading} size="small" onClick={onCancel}>{t("common.cancel")}</Button>
        </div>
      ) : view.state === "completed" ? (
        <Alert
          description={status.temporaryExpiresAt
            ? t("modelProvider.credentialRestore.temporaryCompletedDescription")
            : t("modelProvider.credentialRestore.trustedCompletedDescription")}
          message={t("modelProvider.credentialRestore.completedTitle")}
          showIcon
          type="success"
        />
      ) : view.state === "conflict" ? (
        <Alert
          action={(
            <Space wrap>
              <Button size="small" onClick={saveCopy}>{t("modelProvider.credentialRestore.saveCopy")}</Button>
              <Button danger size="small" onClick={replaceLocal}>{t("modelProvider.credentialRestore.replaceLocal")}</Button>
            </Space>
          )}
          description={t("modelProvider.credentialRestore.restoreConflictDescription")}
          message={t("modelProvider.credentialRestore.restoreConflict")}
          showIcon
          type="warning"
        />
      ) : view.state === "failed" ? (
        <Alert
          action={<Button size="small" onClick={openConfirmation}>{t("common.retry")}</Button>}
          description={status.failureCode === "3080007"
            ? t("modelProvider.credentialRestore.recentAuthenticationRequired")
            : t("modelProvider.credentialRestore.failedDescription")}
          message={t("modelProvider.credentialRestore.failedTitle")}
          showIcon
          type="error"
        />
      ) : (
        <div className="credential-restore-ready">
          <p><SafetyCertificateOutlined aria-hidden="true" />{t("modelProvider.credentialRestore.defaultOffNote")}</p>
          <Button disabled={!view.canStart || loading} type="primary" onClick={openConfirmation}>
            {t("modelProvider.credentialRestore.restoreToThisComputer")}
          </Button>
        </div>
      )}

      <Modal
        centered
        destroyOnHidden
        maskClosable={!loading}
        okButtonProps={{ disabled: loading }}
        okText={t("modelProvider.credentialRestore.confirmRestore")}
        open={confirming}
        title={t("modelProvider.credentialRestore.confirmTitle")}
        width={560}
        onCancel={closeConfirmation}
        onOk={confirmRestore}
      >
        <p className="credential-restore-trust-copy">{t("modelProvider.credentialRestore.recoveryTrustBoundary")}</p>
        <Radio.Group className="credential-restore-mode-list" value={mode} onChange={(event) => setMode(event.target.value)}>
          <Radio value="trusted_device">
            <span><strong>{t("modelProvider.credentialRestore.trustedDevice")}</strong><Tag color="blue">{t("modelProvider.credentialRestore.recommended")}</Tag></span>
            <small>{t("modelProvider.credentialRestore.trustedDeviceDescription")}</small>
          </Radio>
          <Radio value="temporary">
            <span><strong>{t("modelProvider.credentialRestore.temporaryUse")}</strong><ClockCircleOutlined aria-hidden="true" /></span>
            <small>{t("modelProvider.credentialRestore.temporaryUseDescription")}</small>
          </Radio>
        </Radio.Group>
      </Modal>
    </section>
  );
}
