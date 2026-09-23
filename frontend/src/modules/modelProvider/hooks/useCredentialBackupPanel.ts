import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { deriveCredentialBackupView, type CredentialBackupStatus } from "../credentialBackupModel";

export function useCredentialBackupPanel(status: CredentialBackupStatus) {
  const { t, i18n } = useTranslation();
  const view = useMemo(() => deriveCredentialBackupView(status), [status]);
  const lastSucceededTime = view.lastSucceededAt ? Date.parse(view.lastSucceededAt) : Number.NaN;
  const lastSucceeded = Number.isFinite(lastSucceededTime)
    ? new Intl.DateTimeFormat(i18n.resolvedLanguage || i18n.language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(lastSucceededTime))
    : t("modelProvider.credentialBackup.neverSucceeded");

  return { t, view, lastSucceeded };
}
