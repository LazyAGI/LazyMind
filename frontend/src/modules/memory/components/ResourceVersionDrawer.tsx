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
import {
  getSkillRevisionFile,
  listSkillRevisions,
  type SkillRevisionRecord,
} from "../skillApi";
import {
  buildDiffLinesWithInline,
  buildUnifiedDiffLines,
  formatDateTime,
  parseMarkdownFrontMatter,
} from "../shared";
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
  draft_commit: "purple",
  draft_confirm: "purple",
  create: "cyan",
  internal_direct: "default",
  review_accept: "gold",
  metadata_update: "geekblue",
};

const getChangeSourceLabel = (
  changeSource: string,
  t: ResourceVersionDrawerProps["t"],
) => {
  const normalized = changeSource.trim();
  const labelMap: Record<string, string> = {
    auto_apply: t("admin.memoryVersionChangeSourceAutoApply"),
    direct_save: t("admin.memoryVersionChangeSourceDirectSave"),
    draft_commit: t("admin.memoryVersionChangeSourceDraftConfirm"),
    draft_confirm: t("admin.memoryVersionChangeSourceDraftConfirm"),
    create: t("admin.memoryVersionChangeSourceCreate", { defaultValue: "Create" }),
    internal_direct: t("admin.memoryVersionChangeSourceInternalDirect"),
    review_accept: t("admin.memoryVersionChangeSourceReviewAccept"),
    metadata_update: t("admin.memoryVersionChangeSourceMetadataUpdate"),
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

function SkillRevisionDetail({
  revision,
  content,
  previousContent,
  loading,
  error,
  t,
  onRetry,
}: {
  revision: SkillRevisionRecord | null;
  content: string;
  previousContent: string;
  loading: boolean;
  error: string;
  t: ResourceVersionDrawerProps["t"];
  onRetry: () => void;
}) {
  const currentSkill = useMemo(
    () => parseMarkdownFrontMatter(content),
    [content],
  );
  const previousSkill = useMemo(
    () => parseMarkdownFrontMatter(previousContent),
    [previousContent],
  );
  const bodyContent = currentSkill?.content ?? content;
  const previousBodyContent = previousSkill?.content ?? previousContent;
  const diffLines = useMemo(
    () => buildDiffLinesWithInline(previousBodyContent, bodyContent),
    [bodyContent, previousBodyContent],
  );

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

  if (!revision) {
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
          <strong>{getChangeSourceLabel(revision.changeSource, t)}</strong>
        </div>
        <div>
          <span>{t("admin.memoryVersionRange")}</span>
          <strong>r{revision.revisionNo}</strong>
        </div>
        <div>
          <span>{t("admin.memoryVersionChangedAt")}</span>
          <strong>{formatDateTime(revision.createdAt)}</strong>
        </div>
      </div>

      <Tabs
        key={revision.revisionId}
        defaultActiveKey={revision.changeSource === "metadata_update" ? "metadata" : "content"}
        className="memory-version-detail-tabs"
        items={[
          {
            key: "content",
            label: t("admin.memoryVersionTabAfter"),
            children: (
              <VersionContentPanel label={t("admin.memoryVersionTabAfter")} content={bodyContent} />
            ),
          },
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
            key: "metadata",
            label: t("admin.memoryVersionTabMetadata"),
            children: (
              <div className="memory-version-detail-summary">
                <div>
                  <span>{t("admin.memoryName")}</span>
                  <strong>{currentSkill?.name || "-"}</strong>
                  {previousSkill?.name && previousSkill.name !== currentSkill?.name ? (
                    <small>{previousSkill.name} → {currentSkill?.name || "-"}</small>
                  ) : null}
                </div>
                <div>
                  <span>{t("admin.memoryDescription")}</span>
                  <strong>{currentSkill?.description || "-"}</strong>
                  {previousSkill?.description && previousSkill.description !== currentSkill?.description ? (
                    <small>{previousSkill.description} → {currentSkill?.description || "-"}</small>
                  ) : null}
                </div>
                <div>
                  <span>{t("admin.memoryCategory")}</span>
                  <strong>{currentSkill?.category || "-"}</strong>
                  {previousSkill?.category && previousSkill.category !== currentSkill?.category ? (
                    <small>{previousSkill.category} → {currentSkill?.category || "-"}</small>
                  ) : null}
                </div>
              </div>
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
  const [skillRevisions, setSkillRevisions] = useState<SkillRevisionRecord[]>([]);
  const [selectedSkillRevision, setSelectedSkillRevision] = useState<SkillRevisionRecord | null>(
    null,
  );
  const [skillRevisionContent, setSkillRevisionContent] = useState("");
  const [skillRevisionPreviousContent, setSkillRevisionPreviousContent] = useState("");
  const isSkillResource = resourceType === "skill";

  useEffect(() => {
    if (!open) {
      return;
    }
    setPage(1);
    setSelectedId("");
    setDetail(null);
    setSelectedId("");
    setDetail(null);
    setDetailError("");
    setSkillRevisions([]);
    setSelectedSkillRevision(null);
    setSkillRevisionContent("");
    setSkillRevisionPreviousContent("");
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
        if (isSkillResource) {
          const revisions = await listSkillRevisions(resourceId);
          if (ignore) {
            return;
          }
          setSkillRevisions(revisions);
          setTotal(revisions.length);
          setSelectedSkillRevision(revisions[0] || null);
          return;
        }

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
        setSkillRevisions([]);
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
  }, [isSkillResource, open, page, pageSize, reloadKey, resourceId, resourceType, t]);

  useEffect(() => {
    if (!open || !isSkillResource || !selectedSkillRevision) {
      setSkillRevisionContent("");
      setSkillRevisionPreviousContent("");
      setDetailError("");
      return undefined;
    }

    let ignore = false;
    setDetailLoading(true);
    setDetailError("");
    void (async () => {
      try {
        const currentIndex = skillRevisions.findIndex(
          (item) => item.revisionId === selectedSkillRevision.revisionId,
        );
        const previousRevision =
          currentIndex >= 0 ? skillRevisions[currentIndex + 1] : undefined;
        const [currentContent, previousContent] = await Promise.all([
          getSkillRevisionFile(resourceId, selectedSkillRevision.revisionId),
          previousRevision
            ? getSkillRevisionFile(resourceId, previousRevision.revisionId)
            : Promise.resolve(""),
        ]);
        if (ignore) {
          return;
        }
        setSkillRevisionContent(currentContent);
        setSkillRevisionPreviousContent(previousContent);
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load skill revision detail failed:", error);
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
  }, [
    detailReloadKey,
    isSkillResource,
    open,
    resourceId,
    selectedSkillRevision,
    skillRevisions,
    t,
  ]);

  useEffect(() => {
    if (!open || isSkillResource || !selectedId) {
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
  }, [detailReloadKey, isSkillResource, items, open, selectedId, t]);

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
          ) : isSkillResource ? (
            skillRevisions.length ? (
              <div className="memory-version-list">
                {skillRevisions.map((item) => {
                  const active = selectedSkillRevision?.revisionId === item.revisionId;
                  const label = getChangeSourceLabel(item.changeSource, t);

                  return (
                    <button
                      key={item.revisionId}
                      type="button"
                      className={`memory-version-list-item${active ? " is-active" : ""}`}
                      onClick={() => setSelectedSkillRevision(item)}
                    >
                      <span className="memory-version-list-item-main">
                        <strong>r{item.revisionNo}</strong>
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
            )
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

          {total > pageSize && !isSkillResource ? (
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
          {isSkillResource ? (
            <SkillRevisionDetail
              revision={selectedSkillRevision}
              content={skillRevisionContent}
              previousContent={skillRevisionPreviousContent}
              loading={detailLoading}
              error={detailError}
              t={t}
              onRetry={() => setDetailReloadKey((value) => value + 1)}
            />
          ) : (
            <ResourceVersionDetail
              detail={detail}
              loading={detailLoading}
              error={detailError}
              t={t}
              onRetry={() => setDetailReloadKey((value) => value + 1)}
            />
          )}
        </section>
      </div>
    </Drawer>
  );
}
