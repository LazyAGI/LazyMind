import { useRef, useState } from "react";
import { Alert, Button, Modal } from "antd";
import { useTranslation } from "react-i18next";
import { applySettingsChange, checkSettingsChange } from "./api";
import type { SettingsChangeImpact, SettingsChangeKey, SettingsChangeResult } from "./api";
import { useSettingsDraft } from "./SettingsNavigationGuard";

export function useSettingsChange(onSaved: (result: SettingsChangeResult) => void) {
  const { t } = useTranslation();
  const busy = useRef(false);
  const [saving, setSaving] = useState<SettingsChangeKey | null>(null);
  const [pending, setPending] = useState<SettingsChangeImpact | null>(null);
  const [failure, setFailure] = useState<"check" | "save" | null>(null);
  useSettingsDraft({ dirty: false, saving: saving !== null });

  const change = async (key: SettingsChangeKey, enabled: boolean, confirmed?: SettingsChangeImpact) => {
    if (busy.current) return;
    busy.current = true;
    setSaving(key);
    setFailure(null);
    let stage: "check" | "save" = "check";
    try {
      const impact = confirmed || await checkSettingsChange({ key, enabled });
      if (!confirmed && impact.tasks.length) {
        setPending(impact);
        return;
      }
      stage = "save";
      const result = await applySettingsChange({ key, enabled, confirmed_task_ids: confirmed?.tasks.map((task) => task.id) });
      if (!result.applied) {
        setPending(result.impact);
        return;
      }
      setPending(null);
      onSaved(result);
    } catch {
      setPending(confirmed || { key, enabled, tasks: [] });
      setFailure(stage);
    } finally {
      busy.current = false;
      setSaving(null);
    }
  };
  const cancel = () => { setPending(null); setFailure(null); };
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
        if (pending) void change(pending.key, pending.enabled, failure ? undefined : pending);
      }}>{t(failure ? "settingsPage.retry" : "settingsPage.confirmDisable")}</Button>
    </>}
  >
    {failure ? <Alert type="error" showIcon message={t(`settingsPage.change.${failure}Failed`)} /> : <>
      <p>{t("settingsPage.change.description")}</p>
      <ul>{pending?.tasks.map((task) => <li key={task.id}>{task.title || t("settingsPage.change.untitled")}</li>)}</ul>
      <p>{t("settingsPage.change.consequence")}</p>
    </>}
  </Modal>;
  return { requestChange: (key: SettingsChangeKey, enabled: boolean) => void change(key, enabled), saving, dialog };
}
