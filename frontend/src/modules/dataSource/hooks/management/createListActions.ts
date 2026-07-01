import { message } from "antd";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../../api/clients";
import {
  getLocalFSChatSetting,
  updateLocalFSChatSetting,
} from "../../api/localFsChat";
import { normalizeDataSourceStatus } from "../../utils/status";
import {
  getFirstScanBinding,
  getScanSourceId,
  type ScanV2Binding,
  type ScanV2Source,
} from "../../utils/scanAccessors";
import { mapScanSourceToDataSource } from "../../mappers/scanSourceToDataSource";
import type { ManagementContext, RefreshSourcesOptions } from "./context";

export function createListActions(ctx: ManagementContext) {
  const { t } = ctx;

  const refreshSources = async (
    showSuccessMessage = false,
    options?: RefreshSourcesOptions,
  ) => {
    const client = dataSourceScanApi;
    const nextPage = Math.max(1, options?.page ?? ctx.sourceListPage);
    const nextPageSize = Math.max(
      1,
      options?.pageSize ?? ctx.sourceListPageSize,
    );
    const keyword = `${options?.keyword ?? ctx.assetSearchValue}`.trim();
    const requestSeq = ctx.sourceListRequestSeqRef.current + 1;
    ctx.sourceListRequestSeqRef.current = requestSeq;

    ctx.setScanLoading(true);
    try {
      const [sourcesResponse, nextLocalFSChatSetting] = await Promise.all([
        client.listSources({
          keyword: keyword || undefined,
          page: nextPage,
          pageSize: nextPageSize,
        }),
        getLocalFSChatSetting().catch((error) => {
          console.error("Failed to refresh local fs chat setting", error);
          return { enabled: ctx.localScanChatEnabled };
        }),
      ]);
      const sourceList = (sourcesResponse.data.items || []) as ScanV2Source[];
      const visibleSourceList = sourceList.filter(
        (source) => normalizeDataSourceStatus(source.status) !== "deleted",
      );
      const previousSourceMap = new Map(
        ctx.sources.map((item) => [item.id, item]),
      );
      const nextSources = await Promise.all(
        visibleSourceList.map(async (source) => {
          const sourceId = getScanSourceId(source);
          const fallback = previousSourceMap.get(sourceId);
          try {
            const [detailResponse, summaryResponse] = await Promise.all([
              client.getSource({ sourceId }),
              client.getSourceSummary({ sourceId }).catch(() => null),
            ]);
            const detailSource = {
              ...source,
              ...detailResponse.data.source,
              summary: summaryResponse?.data || source.summary,
            };
            const bindings = (detailResponse.data.bindings || []) as ScanV2Binding[];
            return mapScanSourceToDataSource(
              detailSource,
              t,
              fallback,
              getFirstScanBinding(bindings),
              bindings,
            );
          } catch (error) {
            console.error("Failed to load source detail", error);
            return mapScanSourceToDataSource(source, t, fallback);
          }
        }),
      );
      if (ctx.sourceListRequestSeqRef.current !== requestSeq) {
        return;
      }
      ctx.setLocalScanChatEnabled(Boolean(nextLocalFSChatSetting.enabled));
      ctx.setSources(nextSources);
      ctx.setSourceListPage(nextPage);
      ctx.setSourceListPageSize(nextPageSize);
      ctx.setSourceListTotal(Number(sourcesResponse.data.total || 0));

      if (showSuccessMessage) {
        message.success(t("admin.dataSourceListRefreshed"));
      }
    } catch (error) {
      if (showSuccessMessage) {
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      } else {
        console.error("Failed to refresh local sources", error);
      }
    } finally {
      if (ctx.sourceListRequestSeqRef.current === requestSeq) {
        ctx.setScanLoading(false);
      }
    }
  };

  const handleToggleLocalScanChat = async (chatEnabled: boolean) => {
    if (ctx.localScanChatSaving) {
      return;
    }
    if (!ctx.canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }

    const previousValue = ctx.localScanChatEnabled;
    ctx.setLocalScanChatSaving(true);
    ctx.setLocalScanChatEnabled(chatEnabled);

    try {
      const setting = await updateLocalFSChatSetting(chatEnabled);
      ctx.setLocalScanChatEnabled(Boolean(setting.enabled));
      message.success(
        chatEnabled
          ? t("admin.dataSourceLocalScanChatEnabledSuccess")
          : t("admin.dataSourceLocalScanChatDisabledSuccess"),
      );
    } catch (error) {
      ctx.setLocalScanChatEnabled(previousValue);
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    } finally {
      ctx.setLocalScanChatSaving(false);
    }
  };

  return { refreshSources, handleToggleLocalScanChat };
}
