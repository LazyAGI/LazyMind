import { useCallback, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { CLOUD_SYNC_TIMEOUT_MS } from "../constants/options";
import type { DataSourceSummary, DocumentStatusRow } from "../constants/types";
import { dataSourceScanApi } from "../api/clients";
import {
  getFirstScanBinding,
  inferSourceKind,
  type ScanV2Binding,
  type ScanV2Summary,
} from "../utils/scanAccessors";
import { mapScanDocumentToDetail } from "../mappers/scanDocument";
import { buildDetailSummaryFromSource } from "../mappers/scanSource";
import { shouldPollDocumentStatus } from "../utils/scanTree";
import { useSyncPicker } from "./useSyncPicker";

const DETAIL_STATUS_POLL_INTERVAL_MS = 3000;
const DETAIL_STATUS_POLL_TIMEOUT_MS = CLOUD_SYNC_TIMEOUT_MS;

interface UseScanSourceSyncFlowOptions {
  t: TFunction;
  sourceId?: string;
  enabled?: boolean;
  onSyncComplete?: () => void;
}

export function useScanSourceSyncFlow({
  t,
  sourceId = "",
  enabled = true,
  onSyncComplete,
}: UseScanSourceSyncFlowOptions) {
  const [detailSource, setDetailSource] = useState<DataSourceSummary | undefined>();
  const [documents, setDocuments] = useState<DocumentStatusRow[]>([]);
  const [detailLoading, setDetailLoading] = useState(false);
  const [lastSync, setLastSync] = useState(t("admin.dataSourceNeverSynced"));
  const [lastOperation, setLastOperation] = useState<{
    syncedCount: number;
    ignoredCount: number;
    checkedCount: number;
    time: string;
  } | null>(null);
  const [resetSyncStateToken, setResetSyncStateToken] = useState(0);

  const syncPollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const syncPollingActiveRef = useRef(false);
  const syncPollSeqRef = useRef(0);
  const detailRefreshSeqRef = useRef(0);
  const documentRefreshSeqRef = useRef(0);
  const onSyncCompleteRef = useRef(onSyncComplete);

  onSyncCompleteRef.current = onSyncComplete;

  const stopSyncPolling = useCallback(() => {
    syncPollingActiveRef.current = false;
    syncPollSeqRef.current += 1;
    documentRefreshSeqRef.current += 1;
    if (syncPollTimerRef.current) {
      clearTimeout(syncPollTimerRef.current);
      syncPollTimerRef.current = null;
    }
  }, []);

  useEffect(
    () => () => {
      stopSyncPolling();
    },
    [stopSyncPolling],
  );

  useEffect(() => {
    stopSyncPolling();
    setDetailSource(undefined);
    setDocuments([]);
    setLastOperation(null);
    detailRefreshSeqRef.current += 1;
    documentRefreshSeqRef.current += 1;
  }, [sourceId, stopSyncPolling]);

  const refreshDetailFromServer = useCallback(
    async ({
      setLoading = false,
      showError = true,
      resetSyncState = false,
    }: {
      setLoading?: boolean;
      showError?: boolean;
      resetSyncState?: boolean;
    } = {}): Promise<DocumentStatusRow[] | null> => {
      if (!sourceId) {
        return [];
      }

      if (setLoading) {
        setDetailLoading(true);
      }

      const requestSeq = detailRefreshSeqRef.current + 1;
      detailRefreshSeqRef.current = requestSeq;
      const requestOptions = showError
        ? undefined
        : ({ silentError: true } as never);

      try {
        const client = dataSourceScanApi;
        const sourceResponse = await client.getSource(
          { sourceId },
          requestOptions,
        );
        const source = sourceResponse.data.source;
        const binding = getFirstScanBinding(
          sourceResponse.data.bindings as ScanV2Binding[] | undefined,
        );
        const sourceType = inferSourceKind(source, binding);
        const documentsResponse = await client.listSourceDocuments({
          sourceId,
          page: 1,
          pageSize: 200,
        }, requestOptions);
        const summaryResponse = await client
          .getSourceSummary(
            { sourceId },
            { silentError: true } as never,
          )
          .catch(() => null);
        const nextDocuments = (documentsResponse.data.items || []).map((item) =>
          mapScanDocumentToDetail(item, t, sourceType),
        );
        if (detailRefreshSeqRef.current !== requestSeq) {
          return null;
        }

        const nextSource = buildDetailSummaryFromSource(
          source,
          (summaryResponse?.data || sourceResponse.data.summary) as
            | ScanV2Summary
            | undefined,
          nextDocuments,
          binding,
          undefined,
        );

        setDetailSource(nextSource);
        setDocuments(nextDocuments);
        setLastSync(nextSource.lastSync || t("admin.dataSourceNeverSynced"));
        if (resetSyncState) {
          setResetSyncStateToken((token) => token + 1);
        }

        return nextDocuments;
      } catch (error) {
        if (showError && detailRefreshSeqRef.current === requestSeq) {
          console.error("Failed to refresh scan source detail", error);
        }
        return null;
      } finally {
        if (setLoading && detailRefreshSeqRef.current === requestSeq) {
          setDetailLoading(false);
        }
      }
    },
    [sourceId, t],
  );

  const refreshDocumentsFromServer = useCallback(
    async ({
      showError = false,
    }: {
      showError?: boolean;
    } = {}): Promise<DocumentStatusRow[] | null> => {
      if (!sourceId) {
        return [];
      }

      const requestSeq = documentRefreshSeqRef.current + 1;
      documentRefreshSeqRef.current = requestSeq;
      const requestOptions = showError
        ? undefined
        : ({ silentError: true } as never);

      try {
        const client = dataSourceScanApi;
        const documentsResponse = await client.listSourceDocuments({
          sourceId,
          page: 1,
          pageSize: 200,
        }, requestOptions);
        const nextDocuments = (documentsResponse.data.items || []).map((item) =>
          mapScanDocumentToDetail(item, t),
        );
        if (documentRefreshSeqRef.current !== requestSeq) {
          return null;
        }

        setDocuments(nextDocuments);
        return nextDocuments;
      } catch (error) {
        if (showError && documentRefreshSeqRef.current === requestSeq) {
          console.error("Failed to refresh scan source documents", error);
        }
        return null;
      }
    },
    [sourceId, t],
  );

  const startSyncPolling = useCallback(
    (seedDocuments: DocumentStatusRow[] | null) => {
      if (!seedDocuments || !shouldPollDocumentStatus(seedDocuments)) {
        stopSyncPolling();
        return;
      }

      stopSyncPolling();
      syncPollingActiveRef.current = true;
      const pollSeq = syncPollSeqRef.current + 1;
      syncPollSeqRef.current = pollSeq;
      const startedAt = Date.now();

      const pollOnce = async () => {
        if (!syncPollingActiveRef.current || syncPollSeqRef.current !== pollSeq) {
          return;
        }

        if (Date.now() - startedAt >= DETAIL_STATUS_POLL_TIMEOUT_MS) {
          stopSyncPolling();
          onSyncCompleteRef.current?.();
          return;
        }

        const latestDocuments = await refreshDocumentsFromServer({
          showError: false,
        });
        if (!syncPollingActiveRef.current || syncPollSeqRef.current !== pollSeq) {
          return;
        }

        if (latestDocuments && !shouldPollDocumentStatus(latestDocuments)) {
          stopSyncPolling();
          onSyncCompleteRef.current?.();
          return;
        }

        syncPollTimerRef.current = setTimeout(pollOnce, DETAIL_STATUS_POLL_INTERVAL_MS);
      };

      syncPollTimerRef.current = setTimeout(pollOnce, DETAIL_STATUS_POLL_INTERVAL_MS);
    },
    [refreshDocumentsFromServer, stopSyncPolling],
  );

  useEffect(() => {
    if (!enabled || !sourceId) {
      return;
    }

    let cancelled = false;

    const loadDetail = async () => {
      const nextDocuments = await refreshDetailFromServer({
        setLoading: true,
        showError: !cancelled,
        resetSyncState: true,
      });
      if (cancelled || nextDocuments === null) {
        return;
      }
      startSyncPolling(nextDocuments);
    };

    void loadDetail();

    return () => {
      cancelled = true;
      detailRefreshSeqRef.current += 1;
    };
  }, [enabled, refreshDetailFromServer, sourceId, startSyncPolling]);

  const syncPicker = useSyncPicker({
    t,
    id: sourceId,
    routeSource: undefined,
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

  const {
    runSyncPipeline,
    syncSelectedDocIds,
    setSyncPickerOpen,
    ...syncPickerState
  } = syncPicker;

  const confirmSync = useCallback(async () => {
    const finished = await runSyncPipeline(syncSelectedDocIds);
    if (finished) {
      setSyncPickerOpen(false);
      onSyncCompleteRef.current?.();
    }
    return finished;
  }, [runSyncPipeline, setSyncPickerOpen, syncSelectedDocIds]);

  return {
    detailSource,
    detailLoading,
    lastSync,
    lastOperation,
    confirmSync,
    ...syncPickerState,
    runSyncPipeline,
    syncSelectedDocIds,
    setSyncPickerOpen,
  };
}
