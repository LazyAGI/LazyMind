import { useRef, useState } from "react";
import { Alert, Button, Modal } from "antd";
import { useTranslation } from "react-i18next";
import { applySettingsChange } from "./api";
import type { SettingsChangeRequest, SettingsChangeKey, SettingsChangeResult } from "./api";
import { useSettingsDraft } from "./SettingsNavigationGuard";

export function useSettingsChange(onSaved: (result: SettingsChangeResult) => void) {
  const { t } = useTranslation();
  const busy = useRef(false);
  const [saving, setSaving] = useState<SettingsChangeKey | null>(null);
  const [pending, setPending] = useState<SettingsChangeRequest | null>(null);
  const [failure, setFailure] = useState(false);
  useSettingsDraft({ dirty: false, saving: saving !== null });

  const save = async (change: SettingsChangeRequest) => {
    if (busy.current) return;
    busy.current = true;
    setSaving(change.key);
    try {
      const result = await applySettingsChange(change);
      setPending(null);
      setFailure(false);
      onSaved(result);
    } catch {
      setPending(change);
      setFailure(true);
    } finally {
      busy.current = false;
      setSaving(null);
    }
  };
  const requestChange = (key: SettingsChangeKey, enabled: boolean) => {
    if (busy.current || pending) return;
    if (enabled) void save({ key, enabled });
    else setPending({ key, enabled });
  };
  const cancel = () => { setPending(null); setFailure(false); };
  const dialog = <Modal
    open={pending !== null}
    title={t(failure ? "settingsPage.change.failedTitle" : "settingsPage.change.title")}
    onCancel={saving ? undefined : cancel}
    closable={!saving}
    maskClosable={!saving}
    keyboard={!saving}
    footer={<>
      <Button disabled={Boolean(saving)} onClick={cancel}>{t("settingsPage.cancel")}</Button>
      <Button danger={!failure} type="primary" loading={Boolean(saving)} onClick={() => {
        if (pending) void save(pending);
      }}>{t(failure ? "settingsPage.retry" : "settingsPage.confirmDisable")}</Button>
    </>}
  >
    {failure ? <Alert type="error" showIcon message={t("settingsPage.change.saveFailed")} /> :
      <p>{t("settingsPage.change.consequence")}</p>
    }
  </Modal>;
  return { requestChange, saving, dialog };
}
