import { useRef, useState } from "react";
import { Alert, Button, Modal } from "antd";
import { useTranslation } from "react-i18next";
import { applySettingsChange } from "./api";
import { listTasks } from "@/modules/taskCenter/api";
import type { SettingsChangeRequest, SettingsChangeKey, SettingsChangeResult } from "./api";
import { useSettingsDraft } from "./SettingsNavigationGuard";

export function useSettingsChange(onSaved: (result: SettingsChangeResult) => void) {
  const { t } = useTranslation();
  const busy = useRef(false);
  const [saving, setSaving] = useState<SettingsChangeKey | null>(null);
  const [pending, setPending] = useState<SettingsChangeRequest | null>(null);
  const [failure, setFailure] = useState<"check" | "save" | null>(null);
  useSettingsDraft({ dirty: false, saving: saving !== null });

  const save = async (change: SettingsChangeRequest) => {
    if (busy.current) return;
    busy.current = true;
    setSaving(change.key);
    try {
      const result = await applySettingsChange(change);
      setPending(null);
      setFailure(null);
      onSaved(result);
    } catch {
      setPending(change);
      setFailure("save");
    } finally {
      busy.current = false;
      setSaving(null);
    }
  };
  const checkDeveloperTasks = async (change: SettingsChangeRequest) => {
    if (busy.current) return;
    busy.current = true;
    setSaving(change.key);
    let hasRunningTasks: boolean;
    try {
      const tasks = await listTasks({ status: "running", page_size: 1 });
      hasRunningTasks = tasks.total > 0;
    } catch {
      setPending(change);
      setFailure("check");
      return;
    } finally {
      busy.current = false;
      setSaving(null);
    }
    setFailure(null);
    if (hasRunningTasks) setPending(change);
    else {
      setPending(null);
      await save(change);
    }
  };
  const requestChange = (key: SettingsChangeKey, enabled: boolean) => {
    if (busy.current || pending) return;
    if (enabled && key === "developer_mode_active") setPending({ key, enabled });
    else if (enabled) void save({ key, enabled });
    else if (key === "developer_mode_active") void checkDeveloperTasks({ key, enabled });
    else setPending({ key, enabled });
  };
  const cancel = () => { setPending(null); setFailure(null); };
  const enablingDeveloper = pending?.key === "developer_mode_active" && pending.enabled;
  const dialog = <Modal
    open={pending !== null}
    title={failure ? t("settingsPage.change.failedTitle") : enablingDeveloper
      ? t("settingsPage.confirm.developerTitle", { action: t("settingsPage.enable") })
      : t("settingsPage.change.title")}
    onCancel={saving ? undefined : cancel}
    closable={!saving}
    maskClosable={!saving}
    keyboard={!saving}
    footer={<>
      <Button disabled={Boolean(saving)} onClick={cancel}>{t("settingsPage.cancel")}</Button>
      <Button danger={!failure && !enablingDeveloper} type="primary" loading={Boolean(saving)} onClick={() => {
        if (pending) {
          if (failure === "check") void checkDeveloperTasks(pending);
          else void save(pending);
        }
      }}>{t(failure ? "settingsPage.retry" : enablingDeveloper ? "settingsPage.confirmEnable" : "settingsPage.confirmDisable")}</Button>
    </>}
  >
    {failure ? <Alert type="error" showIcon message={t(failure === "check" ? "settingsPage.change.taskCheckFailed" : "settingsPage.change.saveFailed")} /> :
      <p>{t(enablingDeveloper ? "settingsPage.confirm.developerEnableContent" : pending?.key === "developer_mode_active" ? "settingsPage.change.developerConsequence" : "settingsPage.change.consequence")}</p>
    }
  </Modal>;
  return { requestChange, saving, dialog };
}
