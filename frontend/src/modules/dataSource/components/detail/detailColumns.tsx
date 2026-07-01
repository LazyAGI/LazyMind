import { Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { BookOutlined } from "@ant-design/icons";
import type { TFunction } from "i18next";
import type {
  DocumentStatusRow,
  SourceStateValue,
  SyncStateValue,
} from "../../constants/types";
import { getFileUpdateMeta } from "../../utils/status";
import { buildDocumentStatusDetail, getSyncStateMeta } from "../../utils/sourceState";
import { getParseStatusMeta } from "../../utils/parseStatusMeta";
import { getDirectoryLabel, getDocumentType } from "../../utils/detailHelpers";

const { Text } = Typography;

export function buildDetailColumns(
  t: TFunction,
  sourceNameForPath: string,
): ColumnsType<DocumentStatusRow> {
  return [
    {
      title: t("admin.dataSourceDetailTableDocName"),
      dataIndex: "name",
      key: "name",
      width: 360,
      render: (_value, record) => (
        <div className="data-source-detail-doc">
          <div className="data-source-detail-doc-name">
            <BookOutlined />
            <span>{record.name}</span>
          </div>
          <div className="data-source-detail-doc-path">{record.path}</div>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceDetailTableTags"),
      dataIndex: "tags",
      key: "tags",
      width: 160,
      render: (tags: string[]) =>
        tags.length ? (
          <div className="data-source-detail-tags">
            {tags.map((tag) => (
              <Tag key={tag}>{tag}</Tag>
            ))}
          </div>
        ) : (
          "-"
        ),
    },
    {
      title: t("admin.dataSourceDetailTableDirectory"),
      dataIndex: "path",
      key: "path",
      width: 160,
      render: (path: string) => getDirectoryLabel(path, sourceNameForPath),
    },
    {
      title: t("admin.dataSourceDetailTableUpdateState"),
      dataIndex: "updateState",
      key: "updateState",
      width: 240,
      render: (_value, record) => {
        const sourceState: SourceStateValue = record.sourceState || "UNCHANGED";
        const syncState: SyncStateValue = record.syncState || "IDLE";
        const updateMeta = getFileUpdateMeta(record.updateState, t);
        const syncMeta = getSyncStateMeta(
          syncState,
          {
            nextSyncAt: record.nextSyncAt,
            lastError: record.lastError,
            knowledgeBasePresent: record.knowledgeBasePresent,
            sourceState,
          },
          t,
        );
        const detail =
          record.syncDetail ||
          buildDocumentStatusDetail(
            {
              source_state: sourceState,
              sync_state: syncState,
              next_sync_at: record.nextSyncAt,
              last_error: record.lastError,
              knowledge_base_present: record.knowledgeBasePresent,
              update_type: record.updateState.toUpperCase(),
              has_update: record.updateState !== "unchanged",
            },
            t,
          );
        const shouldShowSyncState = syncState !== "IDLE";
        return (
          <div className="data-source-detail-update-state">
            <span
              className={`data-source-update-chip data-source-update-chip-${record.updateState}`}
            >
              <span className="data-source-update-chip-dot" />
              {updateMeta.text}
            </span>
            {shouldShowSyncState ? (
              <Tag color={syncMeta.color} style={{ marginInlineEnd: 0 }}>
                {syncMeta.text}
              </Tag>
            ) : null}
            <Text type="secondary" title={detail}>
              {detail}
            </Text>
          </div>
        );
      },
    },
    {
      title: t("admin.dataSourceDetailTableParseStatus"),
      dataIndex: "parseStatus",
      key: "parseStatus",
      width: 140,
      render: (parseStatus: DocumentStatusRow["parseStatus"], record) => {
        const meta = getParseStatusMeta(parseStatus, t);
        return (
          <Tag
            color={
              parseStatus === "parsed"
                ? "success"
                : parseStatus === "reindexing" || parseStatus === "downloading"
                  ? "processing"
                  : parseStatus === "pending"
                    ? "default"
                    : parseStatus === "duplicate"
                      ? "warning"
                      : parseStatus === "canceled"
                        ? "warning"
                        : "error"
            }
            title={record.lastError || record.syncDetail}
          >
            {meta.text}
          </Tag>
        );
      },
    },
    {
      title: t("admin.dataSourceDetailTableDocType"),
      dataIndex: "name",
      key: "docType",
      width: 120,
      render: (name: string) => getDocumentType(name),
    },
    {
      title: t("admin.dataSourceDetailTableSize"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (size: string) => (
        <Text className="data-source-detail-size" type="secondary">
          {size}
        </Text>
      ),
    },
    {
      title: t("admin.dataSourceDetailTableUpdatedAt"),
      dataIndex: "updatedAt",
      key: "updatedAt",
      width: 180,
    },
  ];
}
