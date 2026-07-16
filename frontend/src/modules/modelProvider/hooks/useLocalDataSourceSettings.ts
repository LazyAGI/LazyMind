import { useCallback, useEffect, useRef, useState } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import type {
  BindingChatSettingEntry,
  SourceChatSettingEntry,
} from "@/api/generated/scan-client";
import { dataSourceScanApi } from "@/modules/dataSource/api/clients";
import type { ScanV2Source } from "@/modules/dataSource/utils/scanAccessors";
import { inferSourceKind } from "@/modules/dataSource/utils/scanAccessors";
import { isAdminRole } from "@/modules/dataSource/utils/role";

export type LocalChatSettingSource = SourceChatSettingEntry & {
  bindings: BindingChatSettingEntry[];
};

const isLocalBinding = (binding: BindingChatSettingEntry) =>
  binding.connector_type.toLowerCase() === "local_fs" ||
  binding.target_type.toLowerCase() === "local_path";

const getEnabledBindingIds = (sources: LocalChatSettingSource[]) =>
  sources.flatMap((source) =>
    source.bindings
      .filter((binding) => binding.chat_enabled)
      .map((binding) => binding.binding_id),
  );

export function useLocalDataSourceSettings() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [localSourceCount, setLocalSourceCount] = useState(0);
  const [localChatSources, setLocalChatSources] = useState<LocalChatSettingSource[]>([]);
  const [chatSettingsLoading, setChatSettingsLoading] = useState(false);
  const [chatSettingsLoadFailed, setChatSettingsLoadFailed] = useState(false);
  const [chatSettingsSaving, setChatSettingsSaving] = useState(false);
  const [chatSettingsModalOpen, setChatSettingsModalOpen] = useState(false);
  const [selectedBindingIds, setSelectedBindingIds] = useState<string[]>([]);
  const chatSettingsRequestSeqRef = useRef(0);
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

  const refreshBindingChatSettings = useCallback(async () => {
    const requestSeq = chatSettingsRequestSeqRef.current + 1;
    chatSettingsRequestSeqRef.current = requestSeq;
    setChatSettingsLoading(true);
    setChatSettingsLoadFailed(false);

    try {
      const response = await dataSourceScanApi.listBindingChatSettings();
      if (chatSettingsRequestSeqRef.current !== requestSeq) {
        return null;
      }

      const sources = (response.data.sources || [])
        .map((source) => ({
          ...source,
          bindings: (source.bindings || []).filter(isLocalBinding),
        }))
        .filter((source) => source.bindings.length > 0);

      setLocalChatSources(sources);
      return sources;
    } catch {
      if (chatSettingsRequestSeqRef.current === requestSeq) {
        setChatSettingsLoadFailed(true);
      }
      return null;
    } finally {
      if (chatSettingsRequestSeqRef.current === requestSeq) {
        setChatSettingsLoading(false);
      }
    }
  }, []);

  const refreshSettings = useCallback(async () => {
    setLoading(true);
    try {
      await Promise.all([
        refreshLocalSourceCount(),
        refreshBindingChatSettings(),
      ]);
    } finally {
      setLoading(false);
    }
  }, [refreshBindingChatSettings, refreshLocalSourceCount]);

  const handleOpenChatSettings = async () => {
    setSelectedBindingIds(getEnabledBindingIds(localChatSources));
    setChatSettingsModalOpen(true);
    const sources = await refreshBindingChatSettings();
    if (sources) {
      setSelectedBindingIds(getEnabledBindingIds(sources));
    }
  };

  const handleRetryChatSettings = async () => {
    const sources = await refreshBindingChatSettings();
    if (sources) {
      setSelectedBindingIds(getEnabledBindingIds(sources));
    }
  };

  const handleSaveChatSettings = async () => {
    if (chatSettingsSaving) return;
    if (!canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }

    const selectedIds = new Set(selectedBindingIds);
    const changedBindings = localChatSources
      .flatMap((source) => source.bindings)
      .filter(
        (binding) =>
          binding.chat_enabled !== selectedIds.has(binding.binding_id),
      );

    if (changedBindings.length === 0) {
      setChatSettingsModalOpen(false);
      return;
    }

    setChatSettingsSaving(true);
    try {
      await Promise.all(
        changedBindings.map((binding) =>
          dataSourceScanApi.updateBindingChatSetting({
            bindingId: binding.binding_id,
            updateBindingChatSettingRequest: {
              chat_enabled: selectedIds.has(binding.binding_id),
            },
          }),
        ),
      );
      const sources = await refreshBindingChatSettings();
      if (sources) {
        setSelectedBindingIds(getEnabledBindingIds(sources));
      }
      setChatSettingsModalOpen(false);
      message.success(t("modelProvider.cloudDocuments.localChatDirectoriesSaveSuccess"));
    } catch {
      const sources = await refreshBindingChatSettings();
      if (sources) {
        setSelectedBindingIds(getEnabledBindingIds(sources));
      }
    } finally {
      setChatSettingsSaving(false);
    }
  };

  useEffect(() => {
    void refreshSettings();
  }, [refreshSettings]);

  useEffect(
    () => () => {
      chatSettingsRequestSeqRef.current += 1;
    },
    [],
  );

  return {
    t,
    loading,
    canCreateLocalSource,
    localSourceCount,
    localChatSources,
    chatSettingsLoading,
    chatSettingsLoadFailed,
    chatSettingsSaving,
    chatSettingsModalOpen,
    selectedBindingIds,
    setSelectedBindingIds,
    setChatSettingsModalOpen,
    handleOpenChatSettings,
    handleRetryChatSettings,
    handleSaveChatSettings,
    refreshSettings,
  };
}
