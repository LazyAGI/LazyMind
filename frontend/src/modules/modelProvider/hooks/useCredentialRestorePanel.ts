import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { deriveCredentialRestoreView, type CredentialRestoreMode, type CredentialRestoreStatus } from "../credentialRestoreModel";

export type CredentialRestorePanelProps = {
  loading: boolean;
  status: CredentialRestoreStatus;
  onCancel: () => void;
  onRefresh: () => void;
  onStart: (mode: CredentialRestoreMode, resolution?: "fail" | "replace_local" | "save_copy") => void;
};

export function useCredentialRestorePanel({ status, onStart }: Pick<CredentialRestorePanelProps, "status" | "onStart">) {
  const { t } = useTranslation();
  const [confirming, setConfirming] = useState(false);
  const [mode, setMode] = useState<CredentialRestoreMode>("trusted_device");
  const view = useMemo(
    () => deriveCredentialRestoreView({ ...status, status: confirming ? "confirming" : status.status }),
    [confirming, status],
  );
  const progress = view.totalRecords > 0
    ? Math.min(100, Math.round((view.completedRecords / view.totalRecords) * 100))
    : 0;

  const confirmRestore = () => {
    setConfirming(false);
    onStart(mode);
  };

  return {
    t, view, progress, confirming, mode, setMode, confirmRestore,
    openConfirmation: () => setConfirming(true),
    closeConfirmation: () => setConfirming(false),
    saveCopy: () => onStart(mode, "save_copy"),
    replaceLocal: () => onStart(mode, "replace_local"),
  };
}
