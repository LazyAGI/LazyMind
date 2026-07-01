import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import "./detail.scss";
import DataSourceDetailView from "@/modules/dataSource/common/components/DataSourceDetailView";
import DataSourceSyncPickerModal from "@/modules/dataSource/common/components/DataSourceSyncPickerModal";
import { useDataSourceDetail } from "./hooks/useDataSourceDetail";
import { useSyncPicker } from "./hooks/useSyncPicker";
import { buildDetailColumns } from "./components/detail/detailColumns";

export default function DataSourceDetail() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const {
    id,
    routeSource,
    keyword,
    setKeyword,
    detailSource,
    documents,
    setDocuments,
    displayDocuments,
    detailLoading,
    documentLoading,
    lastSync,
    setLastSync,
    lastOperation,
    setLastOperation,
    resetSyncStateToken,
    refreshDetailFromServer,
    startSyncPolling,
    stopSyncPolling,
  } = useDataSourceDetail(t);

  const {
    syncSelectedDocIds,
    setSyncSelectedDocIds,
    syncPickerOpen,
    setSyncPickerOpen,
    syncTreeLoading,
    syncSubmitting,
    syncKeyword,
    setSyncKeyword,
    syncTreeData,
    filteredSyncNodeKeys,
    selectableSyncFileKeys,
    hasFilteredSelected,
    syncTreeRequestSeqRef,
    syncTreeInitialLoadRef,
    openSyncPicker,
    loadSyncTreeChildren,
    runSyncPipeline,
  } = useSyncPicker({
    t,
    id,
    routeSource,
    detailSource,
    documents,
    setDocuments,
    setLastSync,
    setLastOperation,
    stopSyncPolling,
    startSyncPolling,
    refreshDetailFromServer,
    resetSyncStateToken,
  });

  const sourceNameForPath = detailSource?.name || t("admin.dataSourceFallbackName");
  const columns = buildDetailColumns(t, sourceNameForPath);

  return (
    <DataSourceDetailView
      t={t}
      detailSource={detailSource ?? null}
      detailLoading={detailLoading}
      documentLoading={documentLoading}
      lastSync={lastSync}
      lastOperation={lastOperation}
      keyword={keyword}
      setKeyword={setKeyword}
      filteredDocuments={displayDocuments}
      columns={columns}
      onBack={() => navigate("/data-sources")}
      onOpenSyncPicker={() => {
        void openSyncPicker();
      }}
      syncPickerModal={
        <DataSourceSyncPickerModal
          t={t}
          open={syncPickerOpen}
          syncSubmitting={syncSubmitting}
          selectedCount={syncSelectedDocIds.length}
          syncKeyword={syncKeyword}
          setSyncKeyword={setSyncKeyword}
          hasFilteredSelected={hasFilteredSelected}
          filteredSyncNodeKeys={filteredSyncNodeKeys}
          setSyncSelectedDocIds={setSyncSelectedDocIds}
          syncTreeLoading={syncTreeLoading}
          syncTreeData={syncTreeData}
          checkedTreeKeys={syncSelectedDocIds}
          selectableSyncFileKeys={selectableSyncFileKeys}
          onLoadSyncTreeNode={loadSyncTreeChildren}
          onCancel={() => {
            if (!syncSubmitting) {
              syncTreeRequestSeqRef.current += 1;
              syncTreeInitialLoadRef.current = false;
              setSyncPickerOpen(false);
            }
          }}
          onOk={() => {
            void (async () => {
              const finished = await runSyncPipeline(syncSelectedDocIds);
              if (finished) {
                setSyncPickerOpen(false);
              }
            })();
          }}
        />
      }
    />
  );
}
