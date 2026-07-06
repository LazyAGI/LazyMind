import type { TFunction } from "i18next";
import type { DatabaseConnectionItem } from "../api/databaseConnections";
import type { DataSourceItem } from "../constants/types";
import { formatDateTime } from "../utils/format";

export function mapDatabaseConnectionToDataSource(
  connection: DatabaseConnectionItem,
  t: TFunction,
): DataSourceItem {
  const verified = Boolean(connection.is_verified);
  const hasError = Boolean(connection.last_check_error?.trim());
  const address = `${connection.host}:${connection.port}/${connection.database_name}`;
  const updatedAt = formatDateTime(connection.update_time || connection.create_time);

  return {
    id: `database:${connection.id}`,
    name: connection.display_name || connection.database_name,
    type: "database",
    knowledgeBase: "-",
    description: connection.description || address,
    target: address,
    syncMode: "manual",
    scheduleLabel: "-",
    status: hasError ? "error" : "active",
    connectionState: verified ? "connected" : hasError ? "error" : "pending",
    lastSync: "-",
    nextSync: "-",
    documentCount: 0,
    parsedDocumentCount: 0,
    addCount: 0,
    deleteCount: 0,
    changeCount: 0,
    permissions: [t("admin.dataSourcePermissionReadOnly")],
    conflictPolicy: "versioned",
    enabled: verified,
    scopeMode: "all",
    selectedFiles: [],
    fileCandidates: [],
    logs: [
      {
        id: `database-log-${connection.id}-${connection.update_time || connection.create_time}`,
        time: updatedAt,
        result: hasError ? "failed" : verified ? "success" : "warning",
        title: verified
          ? t("admin.dataSourceDatabaseVerified")
          : hasError
            ? t("admin.dataSourceDatabaseConnectionError")
            : t("admin.dataSourceDatabasePending"),
        description: connection.last_check_error || t("admin.dataSourceDatabaseReadonlyQueryLog"),
      },
    ],
    warning: connection.last_check_error || undefined,
    oauthConnection: null,
    scanManaged: false,
    storageUsed: "-",
    databaseConnectionId: connection.id,
    databaseConnection: connection,
  };
}
