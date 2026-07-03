import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { message } from "antd";
import type { TFunction } from "i18next";
import { useLocation, useParams } from "react-router-dom";
import { getLocalizedErrorMessage } from "@/components/request";
import { CLOUD_SYNC_TIMEOUT_MS } from "../constants/options";
import { buildFallbackSources, buildDocumentStatusMap } from "../constants/detailDemoData";
import type {
  DataSourceDetailState,
  DataSourceSummary,
  DocumentStatusRow,
} from "../constants/types";
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

const DETAIL_STATUS_POLL_INTERVAL_MS = 3000;
const DETAIL_STATUS_POLL_TIMEOUT_MS = CLOUD_SYNC_TIMEOUT_MS;
const DETAIL_SEARCH_DEBOUNCE_MS = 300;

export function useDataSourceDetail(t: TFunction) {
  const { id = "" } = useParams();
  const location = useLocation();
  const [keyword, setKeyword] = useState("");

  const routeState = location.state as DataSourceDetailState | null;
  const routeSource = routeState?.source;
  const fallbackSources = useMemo(() => buildFallbackSources(t), [t]);
  const documentStatusMap = useMemo(() => buildDocumentStatusMap(t), [t]);
  const fallbackSource = fallbackSources[id];
  const initialSource = routeSource
    ? {
        ...routeSource,
        storageUsed:
          routeSource.storageUsed ||
          documentStatusMap[routeSource.id]?.storageUsed ||
          fallbackSource?.storageUsed ||
          "0 B",
      }
    : fallbackSource;

  const initialDocumentsSeed =
    routeSource?.documents ||
    (initialSource && documentStatusMap[initialSource.id]?.documents) ||
    [];
  const [detailSource, setDetailSource] = useState<DataSourceSummary | undefined>(
    initialSource,
  );
  const [documents, setDocuments] = useState<DocumentStatusRow[]>(initialDocumentsSeed);
  const [displayDocuments, setDisplayDocuments] =
    useState<DocumentStatusRow[]>(initialDocumentsSeed);
  const [detailLoading, setDetailLoading] = useState(true);
  const [documentLoading, setDocumentLoading] = useState(false);
  const [lastSync, setLastSync] = useState(
    initialSource?.lastSync || t("admin.dataSourceNeverSynced"),
  );
  const [lastOperation, setLastOperation] = useState<{
    syncedCount: number;
    ignoredCount: number;
    checkedCount: number;
    time: string;
  } | null>(null);
  // Token bumped whenever a server refresh requests a sync-state reset; the
  // sync picker hook subscribes to it to clear its own selection/picker state.
  const [resetSyncStateToken, setResetSyncStateToken] = useState(0);

  const syncPollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const syncPollingActiveRef = useRef(false);
  const syncPollSeqRef = useRef(0);
  const detailRefreshSeqRef = useRef(0);
  const documentRefreshSeqRef = useRef(0);
  const documentSearchSeqRef = useRef(0);
  const keywordRef = useRef("");

  const stopSyncPolling = useCallback(() => {
    syncPollingActiveRef.current = false;
    syncPollSeqRef.current += 1;
    documentRefreshSeqRef.current += 1;
    if (syncPollTimerRef.current) {
      clearTimeout(syncPollTimerRef.current);
      syncPollTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    stopSyncPolling();
    setDetailSource(initialSource);
    setDocuments(initialDocumentsSeed);
    setDisplayDocuments(initialDocumentsSeed);
    setKeyword("");
    setDocumentLoading(false);
    detailRefreshSeqRef.current += 1;
    documentRefreshSeqRef.current += 1;
    documentSearchSeqRef.current += 1;
    setLastOperation(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, routeSource?.id, routeSource?.lastSync, stopSyncPolling, t]);

  useEffect(() => {
    setLastSync(detailSource?.lastSync || t("admin.dataSourceNeverSynced"));
  }, [detailSource?.lastSync, t]);

  useEffect(
    () => () => {
      stopSyncPolling();
    },
    [stopSyncPolling],
  );

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
      if (!id) {
        return [];
      }

      if (setLoading) {
        setDetailLoading(true);
      }

      const requestSeq = detailRefreshSeqRef.current + 1;
      detailRefreshSeqRef.current = requestSeq;

      try {
        const client = dataSourceScanApi;
        const sourceResponse = await client.getSource({ sourceId: id });
        const source = {
          ...sourceResponse.data.source,
          tenant_id: routeSource?.tenantId,
        };
        const binding = getFirstScanBinding(
          sourceResponse.data.bindings as ScanV2Binding[] | undefined,
        );
        const sourceType = inferSourceKind(source, binding);
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          page: 1,
          pageSize: 200,
        });
        const summaryResponse = await client
          .getSourceSummary({ sourceId: id })
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
        if (!keywordRef.current.trim()) {
          setDisplayDocuments(nextDocuments);
        }
        setLastSync(nextSource.lastSync || t("admin.dataSourceNeverSynced"));
        if (resetSyncState) {
          setResetSyncStateToken((token) => token + 1);
        }

        return nextDocuments;
      } catch (error) {
        if (showError && detailRefreshSeqRef.current === requestSeq) {
          message.error(
            getLocalizedErrorMessage(error, t("common.requestFailed")) ||
              t("common.requestFailed"),
          );
        }
        return null;
      } finally {
        if (setLoading && detailRefreshSeqRef.current === requestSeq) {
          setDetailLoading(false);
        }
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [id, routeSource?.bindingId, routeSource?.tenantId, t],
  );

  const refreshDocumentsFromServer = useCallback(
    async ({
      showError = false,
    }: {
      showError?: boolean;
    } = {}): Promise<DocumentStatusRow[] | null> => {
      if (!id) {
        return [];
      }

      const requestSeq = documentRefreshSeqRef.current + 1;
      documentRefreshSeqRef.current = requestSeq;

      try {
        const client = dataSourceScanApi;
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          page: 1,
          pageSize: 200,
        });
        const nextDocuments = (documentsResponse.data.items || []).map((item) =>
          mapScanDocumentToDetail(item, t),
        );
        if (documentRefreshSeqRef.current !== requestSeq) {
          return null;
        }

        setDocuments(nextDocuments);
        if (!keywordRef.current.trim()) {
          setDisplayDocuments(nextDocuments);
        }

        return nextDocuments;
      } catch (error) {
        if (showError && documentRefreshSeqRef.current === requestSeq) {
          message.error(
            getLocalizedErrorMessage(error, t("common.requestFailed")) ||
              t("common.requestFailed"),
          );
        }
        return null;
      }
    },
    [id, t],
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
          return;
        }

        syncPollTimerRef.current = setTimeout(pollOnce, DETAIL_STATUS_POLL_INTERVAL_MS);
      };

      syncPollTimerRef.current = setTimeout(pollOnce, DETAIL_STATUS_POLL_INTERVAL_MS);
    },
    [refreshDocumentsFromServer, stopSyncPolling],
  );

  useEffect(() => {
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
  }, [refreshDetailFromServer, startSyncPolling]);

  useEffect(() => {
    keywordRef.current = keyword;
    const normalizedKeyword = keyword.trim();
    const requestSeq = documentSearchSeqRef.current + 1;
    documentSearchSeqRef.current = requestSeq;

    if (!normalizedKeyword) {
      setDisplayDocuments(documents);
      setDocumentLoading(false);
      return;
    }

    const timerId = setTimeout(async () => {
      if (!id) {
        return;
      }

      setDocumentLoading(true);
      try {
        const client = dataSourceScanApi;
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          bindingId: detailSource?.bindingId || routeSource?.bindingId,
          keyword: normalizedKeyword,
          page: 1,
          pageSize: 200,
        });
        if (documentSearchSeqRef.current !== requestSeq) {
          return;
        }
        setDisplayDocuments(
          (documentsResponse.data.items || []).map((item) =>
            mapScanDocumentToDetail(item, t),
          ),
        );
      } catch (error) {
        if (documentSearchSeqRef.current !== requestSeq) {
          return;
        }
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      } finally {
        if (documentSearchSeqRef.current === requestSeq) {
          setDocumentLoading(false);
        }
      }
    }, DETAIL_SEARCH_DEBOUNCE_MS);

    return () => {
      clearTimeout(timerId);
      documentSearchSeqRef.current += 1;
    };
  }, [detailSource?.bindingId, id, keyword, routeSource?.bindingId, t]);

  useEffect(() => {
    if (!keywordRef.current.trim()) {
      setDisplayDocuments(documents);
    }
  }, [documents]);

  return {
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
  };
}
