import type { RefObject } from "react";
import { Alert, Button, Skeleton } from "antd";
import { CloudOutlined, InfoCircleOutlined, LoginOutlined, ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import type { CloudUsageCapability, CloudUsageMeterUnit } from "./cloudUsageApi";
import { useCloudUsageSettings } from "./hooks/useCloudUsageSettings";

export default function CloudUsageSettings({ headingRef }: { headingRef?: RefObject<HTMLHeadingElement> }) {
  const { t } = useTranslation();
  const { state, plan, loginLoading, load, startLogin, numberFormat, hasMissingUsage, rows } = useCloudUsageSettings();

  return <div className="settings-cloud-usage">
    <header className="settings-detail-header">
      <div>
        <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.cloudUsage.title")}</h1>
        <p>{t("settingsPage.cloudUsage.description")}</p>
      </div>
      {state === "active" ? <Button icon={<ReloadOutlined />} onClick={() => void load()}>{t("settingsPage.cloudUsage.refresh")}</Button> : null}
    </header>

    {state === "loading" ? <LoadingState label={t("settingsPage.cloudUsage.loading")} /> : null}
    {state === "signed_out" ? <SignedOutState loading={loginLoading} onLogin={() => void startLogin()} /> : null}
    {state === "inactive" ? <InactiveState /> : null}
    {state === "error" ? <ErrorState onRetry={() => void load()} /> : null}
    {state === "active" && plan?.status === "active" ? (
      <ActiveUsage rows={rows} hasMissingUsage={hasMissingUsage} numberFormat={numberFormat} />
    ) : null}
  </div>;
}

function LoadingState({ label }: { label: string }) {
  return <div className="settings-cloud-usage-loading" role="status" aria-live="polite" aria-label={label}>
    <Skeleton active paragraph={{ rows: 7 }} />
  </div>;
}

function SignedOutState({ loading, onLogin }: { loading: boolean; onLogin: () => void }) {
  const { t } = useTranslation();
  return <section className="settings-cloud-usage-empty" aria-labelledby="cloud-usage-signed-out-title">
    <span className="settings-cloud-usage-empty-icon" aria-hidden="true"><CloudOutlined /></span>
    <h2 id="cloud-usage-signed-out-title">{t("settingsPage.cloudUsage.signedOutTitle")}</h2>
    <p>{t("settingsPage.cloudUsage.signedOutDescription")}</p>
    <Button type="primary" icon={<LoginOutlined />} loading={loading} onClick={onLogin} aria-label={t("settingsPage.cloudUsage.signIn")}>
      {t("settingsPage.cloudUsage.signIn")}
    </Button>
  </section>;
}

function InactiveState() {
  const { t } = useTranslation();
  return <section className="settings-cloud-usage-empty" aria-labelledby="cloud-usage-inactive-title">
    <span className="settings-cloud-usage-empty-icon is-neutral" aria-hidden="true"><CloudOutlined /></span>
    <h2 id="cloud-usage-inactive-title">{t("settingsPage.cloudUsage.inactiveTitle")}</h2>
    <p>{t("settingsPage.cloudUsage.inactiveDescription")}</p>
  </section>;
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation();
  return <Alert
    className="settings-cloud-usage-error"
    type="error"
    showIcon
    message={t("settingsPage.cloudUsage.loadErrorTitle")}
    description={t("settingsPage.cloudUsage.loadErrorDescription")}
    action={<Button icon={<ReloadOutlined />} onClick={onRetry} aria-label={t("common.retry")}>{t("common.retry")}</Button>}
  />;
}

function ActiveUsage({ rows, hasMissingUsage, numberFormat }: {
  rows: ReturnType<typeof useCloudUsageSettings>["rows"];
  hasMissingUsage: boolean;
  numberFormat: Intl.NumberFormat;
}) {
  const { t } = useTranslation();

  return <>
    {hasMissingUsage ? <div className="settings-cloud-usage-notice">
      <InfoCircleOutlined aria-hidden="true" />
      <span role="status">{t("settingsPage.cloudUsage.missingUsage")}</span>
    </div> : null}

    <section className="settings-cloud-usage-data" aria-labelledby="cloud-usage-table-heading">
      <div className="settings-cloud-usage-data-heading">
        <div>
          <h2 id="cloud-usage-table-heading">{t("settingsPage.cloudUsage.currentPeriod")}</h2>
          <p>{t("settingsPage.cloudUsage.currentPeriodDescription")}</p>
        </div>
        <span>{t("settingsPage.cloudUsage.modelCount", { count: rows.length })}</span>
      </div>
      <div className="settings-cloud-usage-table-scroll">
        <table className="settings-cloud-usage-table" aria-label={t("settingsPage.cloudUsage.title")}>
          <thead><tr>
            <th scope="col">{t("settingsPage.cloudUsage.model")}</th>
            <th scope="col">{t("settingsPage.cloudUsage.quota")}</th>
            <th scope="col">{t("settingsPage.cloudUsage.used")}</th>
            <th scope="col">{t("settingsPage.cloudUsage.remaining")}</th>
          </tr></thead>
          <tbody>{rows.map(({ usage, ...quota }) => {
            return <tr key={quota.publicModelKey}>
              <td><code>{quota.publicModelKey}</code><span>{capabilityLabel(quota.capability, t)}</span></td>
              <td>{formatAmount(quota.periodicQuota, quota.meterUnit, numberFormat, t)}</td>
              <td>{usage ? formatAmount(usage.usedAmount, usage.meterUnit, numberFormat, t) : "—"}</td>
              <td><strong>{usage ? formatAmount(usage.remainingAmount, usage.meterUnit, numberFormat, t) : "—"}</strong></td>
            </tr>;
          })}</tbody>
        </table>
      </div>
    </section>
  </>;
}

function formatAmount(value: number, unit: CloudUsageMeterUnit, formatter: Intl.NumberFormat, t: (key: string) => string) {
  return <><span className="settings-cloud-usage-number">{formatter.format(value)}</span><small>{unitLabel(unit, t)}</small></>;
}

function unitLabel(unit: CloudUsageMeterUnit, t: (key: string) => string) {
  return t(`settingsPage.cloudUsage.units.${unit}`);
}

function capabilityLabel(capability: CloudUsageCapability, t: (key: string) => string) {
  return t(`settingsPage.cloudUsage.capabilities.${capability}`);
}
