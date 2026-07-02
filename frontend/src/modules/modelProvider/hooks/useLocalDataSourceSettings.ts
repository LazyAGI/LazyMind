import { useCallback, useEffect, useState } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "@/modules/dataSource/api/clients";
import {
  getLocalFSChatSetting,
  updateLocalFSChatSetting,
} from "@/modules/dataSource/api/localFsChat";
import type { ScanV2Source } from "@/modules/dataSource/utils/scanAccessors";
import { inferSourceKind } from "@/modules/dataSource/utils/scanAccessors";
import { isAdminRole } from "@/modules/dataSource/utils/role";

export function useLocalDataSourceSettings() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [localScanChatEnabled, setLocalScanChatEnabled] = useState(false);
  const [localScanChatSaving, setLocalScanChatSaving] = useState(false);
  const [localSourceCount, setLocalSourceCount] = useState(0);
  const canCreateLocalSource = isAdminRole(AgentAppsAuth.getUserInfo()?.role);

  const refreshLocalSourceCount = useCallback(async () => {
    try {
      const response = await dataSourceScanApi.listSources({ page: 1, pageSize: 200 });
      const items = (response.data.items || []) as ScanV2Source[];
      setLocalSourceCount(items.filter((item) => inferSourceKind(item) === "local").length);
    } catch (error) {
      console.error("Failed to refresh local source count", error);
    }
  }, []);

  const refreshSettings = useCallback(async () => {
    setLoading(true);
    try {
      const [localSetting] = await Promise.all([
        getLocalFSChatSetting().catch(() => ({ enabled: false })),
        refreshLocalSourceCount(),
      ]);
      setLocalScanChatEnabled(Boolean(localSetting.enabled));
    } finally {
      setLoading(false);
    }
  }, [refreshLocalSourceCount]);

  const handleToggleLocalScanChat = async (chatEnabled: boolean) => {
    if (localScanChatSaving) {
      return;
    }
    if (!canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }

    const previousValue = localScanChatEnabled;
    setLocalScanChatSaving(true);
    setLocalScanChatEnabled(chatEnabled);

    try {
      const setting = await updateLocalFSChatSetting(chatEnabled);
      setLocalScanChatEnabled(Boolean(setting.enabled));
      message.success(
        chatEnabled
          ? t("modelProvider.cloudDocuments.localScanChatEnabledSuccess")
          : t("modelProvider.cloudDocuments.localScanChatDisabledSuccess"),
      );
    } catch (error) {
      setLocalScanChatEnabled(previousValue);
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    } finally {
      setLocalScanChatSaving(false);
    }
  };

  useEffect(() => {
    void refreshSettings();
  }, [refreshSettings]);

  return {
    t,
    loading,
    canCreateLocalSource,
    localScanChatEnabled,
    localScanChatSaving,
    localSourceCount,
    handleToggleLocalScanChat,
    refreshSettings,
  };
}
