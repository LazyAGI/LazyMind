import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal } from "antd";
import { useTranslation } from "react-i18next";
import { applySettingsChange } from "./api";
import { listTasks } from "@/modules/taskCenter/api";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { getCloudConnectionItems } from "@/modules/dataSource/mappers/cloudConnection";
import type { SettingsChangeRequest, SettingsChangeKey, SettingsChangeResult } from "./api";
import { useSettingsDraft } from "./SettingsNavigationGuard";

const githubCheckOptions = { silentError: true, timeout: 15_000 };

export function useSettingsChange(onSaved: (result: SettingsChangeResult) => void, onConnectGitHub: () => void) {
  const { t } = useTranslation();
  const busy = useRef(false);
  const mounted = useRef(true);
  const [saving, setSaving] = useState<SettingsChangeKey | null>(null);
  const [pending, setPending] = useState<SettingsChangeRequest | null>(null);
  const [failure, setFailure] = useState<"check" | "save" | "github" | null>(null);
  const [githubRequired, setGitHubRequired] = useState(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  useSettingsDraft({ dirty: false, saving: saving !== null });

  const save = async (change: SettingsChangeRequest) => {
    if (busy.current) return;
    busy.current = true;
    setSaving(change.key);
    let checkingGitHub = change.key === "developer_mode_active" && change.enabled;
    try {
      if (checkingGitHub) {
        const response = await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet(
          { provider: "github", status: null }, githubCheckOptions,
        );
        if (!mounted.current) return;
        if (!getCloudConnectionItems(response.data).some(account => account.provider === "github" && account.status === "ACTIVE")) {
          setPending(change);
          setFailure(null);
          setGitHubRequired(true);
          return;
        }
        checkingGitHub = false;
      }
      const result = await applySettingsChange(change);
      setPending(null);
      setFailure(null);
      onSaved(result);
    } catch {
      setPending(change);
      setFailure(checkingGitHub ? "github" : "save");
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
  const cancel = () => { setPending(null); setFailure(null); setGitHubRequired(false); };
  const enablingDeveloper = pending?.key === "developer_mode_active" && pending.enabled;
  const dialog = <Modal
    open={pending !== null}
    title={failure ? t("settingsPage.change.failedTitle") : githubRequired
      ? t("settingsPage.developer.githubGuideTitle") : enablingDeveloper
      ? t("settingsPage.confirm.developerTitle", { action: t("settingsPage.enable") })
      : t("settingsPage.change.title")}
    onCancel={saving ? undefined : cancel}
    closable={!saving}
    maskClosable={!saving}
    keyboard={!saving}
    footer={<>
      <Button disabled={Boolean(saving)} onClick={cancel}>{t(githubRequired ? "settingsPage.developer.githubGuideCancel" : "settingsPage.cancel")}</Button>
      <Button danger={!failure && !enablingDeveloper} type="primary" loading={Boolean(saving)} onClick={() => {
        if (githubRequired) { cancel(); onConnectGitHub(); return; }
        if (pending) {
          if (failure === "check") void checkDeveloperTasks(pending);
          else void save(pending);
        }
      }}>{t(githubRequired ? "settingsPage.developer.githubGuideConnect" : failure ? "settingsPage.retry" : enablingDeveloper ? "settingsPage.confirmEnable" : "settingsPage.confirmDisable")}</Button>
    </>}
  >
    {githubRequired ? <p>{t("settingsPage.developer.githubGuideDescription")}</p> : failure ?
      <Alert type="error" showIcon message={t(failure === "github" ? "settingsPage.developer.githubCheckFailed" : failure === "check" ? "settingsPage.change.taskCheckFailed" : "settingsPage.change.saveFailed")} /> :
      <p>{t(enablingDeveloper ? "settingsPage.confirm.developerEnableContent" : pending?.key === "developer_mode_active" ? "settingsPage.change.developerConsequence" : pending?.key === "document_directories_enabled" ? "settingsPage.controls.documentDirectories.summary" : "settingsPage.change.consequence")}</p>
    }
  </Modal>;
  return { requestChange, saving, dialog };
}
