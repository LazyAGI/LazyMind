import { useEffect, useState, type FC } from "react";
import { Button } from "antd";
import { useTranslation } from "react-i18next";
import DataSourceSyncPickerModal from "@/modules/dataSource/common/components/DataSourceSyncPickerModal";
import { useScanSourceSyncFlow } from "@/modules/dataSource/hooks/useScanSourceSyncFlow";
import { resolveScanSourceIdByDatasetId } from "@/modules/dataSource/utils/resolveScanSourceIdByDatasetId";

interface Props {
  datasetId: string;
  documentParsingEnabled: boolean | null;
  onSyncComplete?: () => void;
}

const KnowledgeBaseSyncNow: FC<Props> = ({
  datasetId,
  documentParsingEnabled,
  onSyncComplete,
}) => {
  const { t } = useTranslation();
  const [sourceId, setSourceId] = useState<string>();
  const [sourceResolving, setSourceResolving] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setSourceResolving(true);
    setSourceId(undefined);

    void resolveScanSourceIdByDatasetId(datasetId)
      .then((resolvedSourceId) => {
        if (!cancelled) {
          setSourceId(resolvedSourceId || undefined);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setSourceResolving(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [datasetId]);

  const syncFlow = useScanSourceSyncFlow({
    t,
    sourceId,
    enabled: Boolean(sourceId),
    onSyncComplete,
  });
  const isDocumentParsingUnavailable = documentParsingEnabled !== true;

  if (!sourceResolving && !sourceId) {
    return null;
  }

  return (
    <>
      <Button
        type="primary"
        ghost
        loading={
          sourceResolving || syncFlow.detailLoading || syncFlow.syncSubmitting
        }
        disabled={
          isDocumentParsingUnavailable ||
          sourceResolving ||
          !sourceId ||
          syncFlow.detailLoading
        }
        title={isDocumentParsingUnavailable
          ? documentParsingEnabled === false
            ? "文档解析已暂停，请在设置中重新启用"
            : "正在读取文档解析状态"
          : undefined}
        onClick={() => {
          void syncFlow.openSyncPicker();
        }}
      >
        {t("admin.dataSourceDetailSyncNow")}
      </Button>

      <DataSourceSyncPickerModal
        t={t}
        open={syncFlow.syncPickerOpen}
        syncSubmitting={syncFlow.syncSubmitting}
        selectedCount={syncFlow.syncSelectedDocIds.length}
        syncKeyword={syncFlow.syncKeyword}
        setSyncKeyword={syncFlow.setSyncKeyword}
        hasFilteredSelected={syncFlow.hasFilteredSelected}
        filteredSyncNodeKeys={syncFlow.filteredSyncNodeKeys}
        setSyncSelectedDocIds={syncFlow.setSyncSelectedDocIds}
        syncTreeLoading={syncFlow.syncTreeLoading}
        syncTreeData={syncFlow.syncTreeData}
        checkedTreeKeys={syncFlow.syncSelectedDocIds}
        selectableSyncFileKeys={syncFlow.selectableSyncFileKeys}
        onLoadSyncTreeNode={syncFlow.loadSyncTreeChildren}
        onCancel={() => {
          if (!syncFlow.syncSubmitting) {
            syncFlow.syncTreeRequestSeqRef.current += 1;
            syncFlow.syncTreeInitialLoadRef.current = false;
            syncFlow.setSyncPickerOpen(false);
          }
        }}
        onOk={() => {
          void syncFlow.confirmSync();
        }}
      />
    </>
  );
};

export default KnowledgeBaseSyncNow;
