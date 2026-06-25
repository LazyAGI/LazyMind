import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Empty,
  Pagination,
  Skeleton,
  Tabs,
  Tag,
} from "antd";
import { FileSearchOutlined } from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  getResourceVersion,
  listResourceVersions,
  type ResourceVersionRecord,
  type ResourceVersionType,
} from "../resourceVersionApi";
import { buildDiffLinesWithInline, buildUnifiedDiffLines, formatDateTime } from "../shared";
import { DiffLineContent } from "./DiffLineContent";

interface ResourceVersionDrawerProps {
  open: boolean;
  resourceId: string;
  resourceName: string;
  resourceType: ResourceVersionType;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
}

const defaultPageSize = 20;

const changeSourceColorMap: Record<string, string> = {
  auto_apply: "blue",
  direct_save: "green",
  draft_confirm: "purple",
  internal_direct: "default",
  review_accept: "gold",
};

const getChangeSourceLabel = (
  changeSource: string,
  t: ResourceVersionDrawerProps["t"],
) => {
  const normalized = changeSource.trim();
  const labelMap: Record<string, string> = {
    auto_apply: t("admin.memoryVersionChangeSourceAutoApply"),
    direct_save: t("admin.memoryVersionChangeSourceDirectSave"),
    draft_confirm: t("admin.memoryVersionChangeSourceDraftConfirm"),
    internal_direct: t("admin.memoryVersionChangeSourceInternalDirect"),
    review_accept: t("admin.memoryVersionChangeSourceReviewAccept"),
  };

  return labelMap[normalized] || normalized || "-";
};

const getResourceTypeLabel = (
  resourceType: ResourceVersionType,
  t: ResourceVersionDrawerProps["t"],
) => {
  if (resourceType === "skill") {
    return t("admin.memoryVersionResourceSkill");
  }
  if (resourceType === "memory") {
    return t("admin.memoryVersionResourceMemory");
  }
  return t("admin.memoryVersionResourcePreference");
};

const getContentLines = (content: string) =>
  (content || "-").split("\n").map((text, index) => ({
    id: `${index}-${text}`,
    text: text || " ",
  }));

function VersionContentPanel({
  label,
  content,
}: {
  label: string;
  content: string;
}) {
  const lines = useMemo(() => getContentLines(content), [content]);

  return (
    <div className="memory-version-content-panel">
      <div className="memory-version-content-panel-head">{label}</div>
      <div className="memory-version-content-code">
        {lines.map((line, index) => (
          <div key={line.id} className="memory-version-content-line">
            <span>{index + 1}</span>
            <code>{line.text}</code>
          </div>
        ))}
      </div>
    </div>
  );
}

function ResourceVersionDetail({
  detail,
  loading,
  error,
  t,
  onRetry,
}: {
  detail: ResourceVersionRecord | null;
  loading: boolean;
  error: string;
  t: ResourceVersionDrawerProps["t"];
  onRetry: () => void;
}) {
  const diffLines = useMemo(() => {
    if (!detail) {
      return [];
    }
    return detail.diff
      ? buildUnifiedDiffLines(detail.diff)
      : buildDiffLinesWithInline(detail.beforeContent, detail.afterContent);
  }, [detail]);

  if (loading) {
    return (
      <div className="memory-version-detail-card">
        <Skeleton active paragraph={{ rows: 8 }} />
      </div>
    );
  }

  if (error) {
    return (
      <Alert
        showIcon
        type="error"
        message={error}
        action={
          <Button size="small" onClick={onRetry}>
            {t("common.retry")}
          </Button>
        }
      />
    );
  }

  if (!detail) {
    return (
      <div className="memory-version-detail-empty">
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryVersionSelectEmpty")}
        />
      </div>
    );
  }

  return (
    <div className="memory-version-detail-card">
      <div className="memory-version-detail-summary">
        <div>
          <span>{t("admin.memoryVersionChangeSource")}</span>
          <strong>{getChangeSourceLabel(detail.changeSource, t)}</strong>
        </div>
        <div>
          <span>{t("admin.memoryVersionRange")}</span>
          <strong>
            v{detail.fromVersion} - v{detail.toVersion}
          </strong>
        </div>
        <div>
          <span>{t("admin.memoryVersionChangedAt")}</span>
          <strong>{formatDateTime(detail.createdAt)}</strong>
        </div>
      </div>

      <Tabs
        className="memory-version-detail-tabs"
        items={[
          {
            key: "diff",
            label: t("admin.memoryVersionTabDiff"),
            children: (
              <div className="memory-version-diff" aria-label={t("admin.memoryVersionTabDiff")}>
                {diffLines.length ? (
                  diffLines.map((line, index) => (
                    <div
                      key={`${index}-${line.type}-${line.text}`}
                      className={`memory-diff-line is-${line.type}`}
                    >
                      <span className="memory-diff-prefix">
                        {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                      </span>
                      <DiffLineContent line={line} />
                    </div>
                  ))
                ) : (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={t("admin.memoryVersionDiffEmpty")}
                  />
                )}
              </div>
            ),
          },
          {
            key: "before",
            label: t("admin.memoryVersionTabBefore"),
            children: (
              <VersionContentPanel
                label={t("admin.memoryVersionTabBefore")}
                content={detail.beforeContent}
              />
            ),
          },
          {
            key: "after",
            label: t("admin.memoryVersionTabAfter"),
            children: (
              <VersionContentPanel
                label={t("admin.memoryVersionTabAfter")}
                content={detail.afterContent}
              />
            ),
          },
        ]}
      />
    </div>
  );
}

export default function ResourceVersionDrawer({
  open,
  resourceId,
  resourceName,
  resourceType,
  t,
  onClose,
}: ResourceVersionDrawerProps) {
  const [items, setItems] = useState<ResourceVersionRecord[]>([]);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(defaultPageSize);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [detail, setDetail] = useState<ResourceVersionRecord | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [detailReloadKey, setDetailReloadKey] = useState(0);

  useEffect(() => {
    if (!open) {
      return;
    }
    setPage(1);
    setSelectedId("");
    setDetail(null);
    setDetailError("");
  }, [open, resourceId, resourceType]);

  useEffect(() => {
    if (!open || !resourceId) {
      return undefined;
    }

    let ignore = false;
    setLoading(true);
    setErrorMessage("");
    void (async () => {
      try {
        const result = await listResourceVersions({
          resourceType,
          resourceId,
          page,
          pageSize,
        });
        if (ignore) {
          return;
        }
        setItems(result.items);
        setTotal(result.total);
        setSelectedId((current) => current || result.items[0]?.id || "");
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load resource versions failed:", error);
        setErrorMessage(
          getLocalizedErrorMessage(error, t("admin.memoryVersionLoadFailed")) ||
            t("admin.memoryVersionLoadFailed"),
        );
        setItems([]);
        setTotal(0);
      } finally {
        if (!ignore) {
          setLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [open, page, pageSize, reloadKey, resourceId, resourceType, t]);

  useEffect(() => {
    if (!open || !selectedId) {
      setDetail(null);
      setDetailError("");
      return undefined;
    }

    let ignore = false;
    const listRecord = items.find((item) => item.id === selectedId) || null;
    setDetail(listRecord);
    setDetailLoading(true);
    setDetailError("");
    void (async () => {
      try {
        const nextDetail = await getResourceVersion(selectedId);
        if (ignore) {
          return;
        }
        setDetail(nextDetail);
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load resource version detail failed:", error);
        setDetailError(
          getLocalizedErrorMessage(error, t("admin.memoryVersionDetailLoadFailed")) ||
            t("admin.memoryVersionDetailLoadFailed"),
        );
      } finally {
        if (!ignore) {
          setDetailLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [detailReloadKey, items, open, selectedId, t]);

  const title = (
    <div className="memory-version-drawer-title">
      <span>{t("admin.memoryVersionHistoryTitle")}</span>
      <strong>{resourceName || resourceId}</strong>
    </div>
  );

  return (
    <Drawer
      destroyOnHidden
      width="min(980px, calc(100vw - 32px))"
      open={open}
      title={title}
      className="memory-version-drawer"
      onClose={onClose}
      extra={
        <Tag bordered={false} className="memory-version-resource-tag">
          {getResourceTypeLabel(resourceType, t)}
        </Tag>
      }
    >
      <div className="memory-version-drawer-body">
        <aside className="memory-version-list-panel" aria-label={t("admin.memoryVersionList")}>
          <div className="memory-version-list-head">
            <span>{t("admin.memoryVersionList")}</span>
            <strong>{t("common.totalItems", { total })}</strong>
          </div>

          {errorMessage ? (
            <Alert
              showIcon
              type="error"
              message={errorMessage}
              action={
                <Button size="small" onClick={() => setReloadKey((value) => value + 1)}>
                  {t("common.retry")}
                </Button>
              }
            />
          ) : loading ? (
            <div className="memory-version-list-skeleton">
              <Skeleton active paragraph={{ rows: 10 }} />
            </div>
          ) : items.length ? (
            <div className="memory-version-list">
              {items.map((item) => {
                const active = selectedId === item.id;
                const label = getChangeSourceLabel(item.changeSource, t);

                return (
                  <button
                    key={item.id}
                    type="button"
                    className={`memory-version-list-item${active ? " is-active" : ""}`}
                    onClick={() => setSelectedId(item.id)}
                  >
                    <span className="memory-version-list-item-main">
                      <strong>
                        v{item.fromVersion} - v{item.toVersion}
                      </strong>
                      <span>{formatDateTime(item.createdAt)}</span>
                    </span>
                    <Tag color={changeSourceColorMap[item.changeSource] || "default"}>
                      {label}
                    </Tag>
                  </button>
                );
              })}
            </div>
          ) : (
            <div className="memory-version-list-empty">
              <Empty
                image={<FileSearchOutlined />}
                description={t("admin.memoryVersionEmpty")}
              />
            </div>
          )}

          {total > pageSize ? (
            <Pagination
              size="small"
              current={page}
              pageSize={pageSize}
              total={total}
              showSizeChanger
              pageSizeOptions={[10, 20, 30]}
              onChange={(nextPage, nextPageSize) => {
                setPage(nextPage);
                setPageSize(nextPageSize);
                setSelectedId("");
                setDetail(null);
              }}
            />
          ) : null}
        </aside>

        <section className="memory-version-detail-panel">
          <ResourceVersionDetail
            detail={detail}
            loading={detailLoading}
            error={detailError}
            t={t}
            onRetry={() => setDetailReloadKey((value) => value + 1)}
          />
        </section>
      </div>
    </Drawer>
  );
}
