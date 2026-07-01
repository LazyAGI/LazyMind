import type { TFunction } from "i18next";
import type { DataSourceSummary, DocumentStatusRow } from "./types";

export function buildFallbackSources(
  t: TFunction,
): Record<string, DataSourceSummary & { storageUsed: string }> {
  return {
    "source-feishu-rd": {
      id: "source-feishu-rd",
      name: t("admin.dataSourceDemoData.sources.feishuRdName"),
      target: "Wiki://space_rd_platform",
      documentCount: 1284,
      status: "active",
      lastSync: "2026-04-13 10:24",
      addCount: 18,
      deleteCount: 2,
      changeCount: 41,
      storageUsed: "452.8 MB",
    },
    "source-local-ops": {
      id: "source-local-ops",
      name: t("admin.dataSourceDemoData.sources.localOpsName"),
      target: "/mnt/team-share/ops-docs",
      documentCount: 764,
      status: "active",
      lastSync: "2026-04-13 08:12",
      addCount: 5,
      deleteCount: 0,
      changeCount: 9,
      storageUsed: "218.6 MB",
    },
  };
}

export function buildDocumentStatusMap(
  t: TFunction,
): Record<string, { storageUsed: string; documents: DocumentStatusRow[] }> {
  return {
    "source-feishu-rd": {
      storageUsed: "452.8 MB",
      documents: [
        {
          id: "fs-1",
          name: t("admin.dataSourceDemoData.docs.feishuDevDocName"),
          path: t("admin.dataSourceDemoData.docs.feishuDevDocPath"),
          size: "1.4 MB",
          tags: [
            t("admin.dataSourceDemoData.tags.integration"),
            t("admin.dataSourceDemoData.tags.feishu"),
          ],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.changedReparsed"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 10:21",
          updatedAt: "2026-04-13 10:24",
        },
        {
          id: "fs-2",
          name: t("admin.dataSourceDemoData.docs.oauthSpecName"),
          path: t("admin.dataSourceDemoData.docs.oauthSpecPath"),
          size: "856 KB",
          tags: ["OAuth", t("admin.dataSourceDemoData.tags.api")],
          updateState: "new",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.newVectorIndexed"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 09:52",
          updatedAt: "2026-04-13 09:58",
        },
        {
          id: "fs-3",
          name: t("admin.dataSourceDemoData.docs.permissionFlowName"),
          path: t("admin.dataSourceDemoData.docs.permissionFlowPath"),
          size: "122 KB",
          tags: [t("admin.dataSourceDemoData.tags.permission")],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.permissionReindexing"),
          parseStatus: "reindexing",
          sourceUpdatedAt: "2026-04-13 09:40",
          updatedAt: "2026-04-13 09:41",
        },
        {
          id: "fs-4",
          name: t("admin.dataSourceDemoData.docs.legacyConnectionName"),
          path: t("admin.dataSourceDemoData.docs.legacyConnectionPath"),
          size: "730 KB",
          tags: [t("admin.dataSourceDemoData.tags.archive")],
          updateState: "unchanged",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.duplicateVersioned"),
          parseStatus: "duplicate",
          sourceUpdatedAt: "2026-04-11 23:55",
          updatedAt: "2026-04-12 02:01",
        },
      ],
    },
    "source-local-ops": {
      storageUsed: "218.6 MB",
      documents: [
        {
          id: "ops-1",
          name: t("admin.dataSourceDemoData.docs.inspectionManualName"),
          path: t("admin.dataSourceDemoData.docs.inspectionManualPath"),
          size: "2.1 MB",
          tags: [t("admin.dataSourceDemoData.tags.inspection"), "SOP"],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.changedReparsed"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 08:09",
          updatedAt: "2026-04-13 08:12",
        },
        {
          id: "ops-2",
          name: t("admin.dataSourceDemoData.docs.dutyScheduleName"),
          path: t("admin.dataSourceDemoData.docs.dutySchedulePath"),
          size: "414 KB",
          tags: [t("admin.dataSourceDemoData.tags.schedule")],
          updateState: "new",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.newIndexDone"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 08:00",
          updatedAt: "2026-04-13 08:05",
        },
        {
          id: "ops-3",
          name: t("admin.dataSourceDemoData.docs.incidentReviewName"),
          path: t("admin.dataSourceDemoData.docs.incidentReviewPath"),
          size: "96 KB",
          tags: [t("admin.dataSourceDemoData.tags.review")],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.rechunking"),
          parseStatus: "reindexing",
          sourceUpdatedAt: "2026-04-13 07:53",
          updatedAt: "2026-04-13 07:58",
        },
        {
          id: "ops-4",
          name: t("admin.dataSourceDemoData.docs.topologyArchiveName"),
          path: t("admin.dataSourceDemoData.docs.topologyArchivePath"),
          size: "8.2 MB",
          tags: [
            t("admin.dataSourceDemoData.tags.topology"),
            t("admin.dataSourceDemoData.tags.history"),
          ],
          updateState: "deleted",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.sourceDeletedCleanup"),
          parseStatus: "deleted",
          sourceUpdatedAt: "2026-04-12 21:10",
          updatedAt: "2026-04-12 21:16",
        },
      ],
    },
  };
}
