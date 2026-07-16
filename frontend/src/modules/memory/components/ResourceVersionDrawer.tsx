import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Empty,
  Modal,
  Skeleton,
  Tabs,
  Tag,
  message,
} from "antd";
import { FileSearchOutlined, RollbackOutlined } from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import type { ResourceVersionType } from "../resourceVersionApi";
import {
  getPersonalResourceRevision,
  listPersonalResourceRevisions,
  rollbackPersonalResource,
  RollbackConflictError as PersonalRollbackConflictError,
  type PersonalResourceApiType,
  type PersonalResourceRevisionRecord,
} from '../preferenceApi';
import {
  getSkillRevisionFile,
  listSkillRevisions,
  rollbackSkill,
  RollbackConflictError as SkillRollbackConflictError,
  type SkillRevisionRecord,
} from '../skillApi';
import {
  buildDiffLinesWithInline,
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
  onRolledBack?: () => void | Promise<void>;
}

type RevisionListItem = {
  revisionId: string;
  revisionNo: number;
  changeSource: string;
  createdAt: string;
  isHead: boolean;
};

const changeSourceColorMap: Record<string, string> = {
  auto_apply: "blue",
  direct_save: "green",
  draft_commit: "purple",
  draft_confirm: "purple",
  create: "cyan",
  internal_direct: "default",
  review_accept: "gold",
  metadata_update: "geekblue",
  rollback: "orange",
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
    rollback: t("admin.memoryVersionChangeSourceRollback"),
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

const toPersonalResourceApiType = (
  resourceType: ResourceVersionType,
): PersonalResourceApiType =>
  resourceType === "memory" ? "memory" : "user_preference";

const formatRevisionLabel = (revisionNo: number) => `v${revisionNo}`;

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

function RevisionDetail({
  revision,
  content,
  previousContent,
  loading,
  error,
  canRollback,
  rollingBack,
  t,
  onRetry,
  onRollback,
}: {
  revision: RevisionListItem | null;
  content: string;
  previousContent: string;
  loading: boolean;
  error: string;
  canRollback: boolean;
  rollingBack: boolean;
  t: ResourceVersionDrawerProps["t"];
  onRetry: () => void;
  onRollback: () => void;
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
          <strong>{formatRevisionLabel(revision.revisionNo)}</strong>
        </div>
        <div>
          <span>{t("admin.memoryVersionChangedAt")}</span>
          <strong>{formatDateTime(revision.createdAt)}</strong>
        </div>
      </div>

      <div className="memory-version-detail-actions">
        <Button
          icon={<RollbackOutlined />}
          disabled={!canRollback}
          loading={rollingBack}
          onClick={onRollback}
        >
          {t("admin.memoryVersionRollbackButton")}
        </Button>
        {revision.isHead ? (
          <span className="memory-version-head-hint">
            {t("admin.memoryVersionRollbackCurrentHint")}
          </span>
        ) : null}
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
              <div
                className="memory-version-diff"
                aria-label={t("admin.memoryVersionTabDiff")}
              >
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
  onRolledBack,
}: ResourceVersionDrawerProps) {
  const isSkillResource = resourceType === "skill";
  const personalResourceType = toPersonalResourceApiType(resourceType);

  const [revisions, setRevisions] = useState<RevisionListItem[]>([]);
  const [selectedRevisionId, setSelectedRevisionId] = useState("");
  const [content, setContent] = useState("");
  const [previousContent, setPreviousContent] = useState("");
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState("");
  const [detailError, setDetailError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [detailReloadKey, setDetailReloadKey] = useState(0);
  const [rollingBack, setRollingBack] = useState(false);
  const [skillRevisionCache, setSkillRevisionCache] = useState<SkillRevisionRecord[]>(
    [],
  );
  const [personalRevisionCache, setPersonalRevisionCache] = useState<
    PersonalResourceRevisionRecord[]
  >([]);

  const selectedRevision =
    revisions.find((item) => item.revisionId === selectedRevisionId) || null;
  const canRollback = Boolean(selectedRevision && !selectedRevision.isHead);

  useEffect(() => {
    if (!open) {
      return;
    }
    setSelectedRevisionId("");
    setContent("");
    setPreviousContent("");
    setDetailError("");
    setRevisions([]);
    setSkillRevisionCache([]);
    setPersonalRevisionCache([]);
  }, [open, resourceId, resourceType]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    if (isSkillResource && !resourceId) {
      return undefined;
    }

    let ignore = false;
    setLoading(true);
    setErrorMessage("");
    void (async () => {
      try {
        if (isSkillResource) {
          const items = await listSkillRevisions(resourceId);
          if (ignore) {
            return;
          }
          const nextRevisions = items.map((item) => ({
            revisionId: item.revisionId,
            revisionNo: item.revisionNo,
            changeSource: item.changeSource,
            createdAt: item.createdAt,
            isHead: item.isHead,
          }));
          setSkillRevisionCache(items);
          setPersonalRevisionCache([]);
          setRevisions(nextRevisions);
          setSelectedRevisionId((current) => {
            if (current) {
              const stillExists = nextRevisions.some((r) => r.revisionId === current);
              if (stillExists) return current;
            }
            const headRevision = nextRevisions.find((r) => r.isHead);
            return headRevision?.revisionId || nextRevisions[0]?.revisionId || '';
          });
          return;
        }

        const items = await listPersonalResourceRevisions(personalResourceType);
        if (ignore) {
          return;
        }
        const nextRevisions = items.map((item) => ({
          revisionId: item.revisionId,
          revisionNo: item.revisionNo,
          changeSource: item.changeSource,
          createdAt: item.createdAt,
          isHead: item.isHead,
        }));
        setPersonalRevisionCache(items);
        setSkillRevisionCache([]);
        setRevisions(nextRevisions);
        setSelectedRevisionId((current) => {
          if (current) {
            const stillExists = nextRevisions.some((r) => r.revisionId === current);
            if (stillExists) return current;
          }
          const headRevision = nextRevisions.find((r) => r.isHead);
          return headRevision?.revisionId || nextRevisions[0]?.revisionId || '';
        });
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load resource versions failed:", error);
        setErrorMessage(getLocalizedErrorMessage(error));
        setRevisions([]);
        setSkillRevisionCache([]);
        setPersonalRevisionCache([]);
      } finally {
        if (!ignore) {
          setLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [isSkillResource, open, personalResourceType, reloadKey, resourceId, t]);

  useEffect(() => {
    if (!open || !selectedRevisionId) {
      setContent("");
      setPreviousContent("");
      setDetailError("");
      return undefined;
    }

    let ignore = false;
    setDetailLoading(true);
    setDetailError("");
    void (async () => {
      try {
        if (isSkillResource) {
          const currentIndex = skillRevisionCache.findIndex(
            (item) => item.revisionId === selectedRevisionId,
          );
          const previousRevision =
            currentIndex >= 0 ? skillRevisionCache[currentIndex + 1] : undefined;
          const [currentContent, prevContent] = await Promise.all([
            getSkillRevisionFile(resourceId, selectedRevisionId),
            previousRevision
              ? getSkillRevisionFile(resourceId, previousRevision.revisionId)
              : Promise.resolve(""),
          ]);
          if (ignore) {
            return;
          }
          setContent(currentContent);
          setPreviousContent(prevContent);
          return;
        }

        const currentIndex = personalRevisionCache.findIndex(
          (item) => item.revisionId === selectedRevisionId,
        );
        const previousRevision =
          currentIndex >= 0 ? personalRevisionCache[currentIndex + 1] : undefined;
        const [currentDetail, previousDetail] = await Promise.all([
          getPersonalResourceRevision(personalResourceType, selectedRevisionId),
          previousRevision
            ? getPersonalResourceRevision(
                personalResourceType,
                previousRevision.revisionId,
              )
            : Promise.resolve(null),
        ]);
        if (ignore) {
          return;
        }
        setContent(currentDetail.content);
        setPreviousContent(previousDetail?.content || "");
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load revision detail failed:", error);
        setDetailError(getLocalizedErrorMessage(error));
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
    personalResourceType,
    personalRevisionCache,
    resourceId,
    selectedRevisionId,
    skillRevisionCache,
    t,
  ]);

  const handleRollback = () => {
    if (!selectedRevision || selectedRevision.isHead) {
      return;
    }

    Modal.confirm({
      title: t('admin.memoryVersionRollbackConfirmTitle'),
      content: t('admin.memoryVersionRollbackConfirmContent', {
        version: formatRevisionLabel(selectedRevision.revisionNo),
        name: resourceName || resourceId,
      }),
      okText: t('admin.memoryVersionRollbackButton'),
      cancelText: t('common.cancel'),
      onOk: async () => {
        setRollingBack(true);
        try {
          if (isSkillResource) {
            await rollbackSkill(resourceId, selectedRevision.revisionId);
          } else {
            const headRevision = revisions.find((item) => item.isHead);
            await rollbackPersonalResource(personalResourceType, {
              revisionId: selectedRevision.revisionId,
              expectedHeadRevisionId: headRevision?.revisionId || undefined,
              message: `rollback to ${formatRevisionLabel(selectedRevision.revisionNo)}`,
            });
          }
          message.success(t('admin.memoryVersionRollbackSuccess'));
          setReloadKey((value) => value + 1);
          await onRolledBack?.();
        } catch (error) {
          const isConflict =
            error instanceof SkillRollbackConflictError ||
            error instanceof PersonalRollbackConflictError;
          if (isConflict) {
            return;
          }
          console.error('Rollback resource version failed:', error);
          throw error;
        } finally {
          setRollingBack(false);
        }
      },
    });
  };

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
            <strong>{t("common.totalItems", { total: revisions.length })}</strong>
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
          ) : revisions.length ? (
            <div className="memory-version-list">
              {revisions.map((item) => {
                const active = selectedRevisionId === item.revisionId;
                const label = getChangeSourceLabel(item.changeSource, t);

                return (
                  <button
                    key={item.revisionId}
                    type="button"
                    className={`memory-version-list-item${active ? " is-active" : ""}`}
                    onClick={() => setSelectedRevisionId(item.revisionId)}
                  >
                    <span className="memory-version-list-item-main">
                      <strong>
                        {formatRevisionLabel(item.revisionNo)}
                        {item.isHead ? (
                          <em className="memory-version-current-badge">
                            {t("admin.memoryVersionCurrentBadge")}
                          </em>
                        ) : null}
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
        </aside>

        <section className="memory-version-detail-panel">
          <RevisionDetail
            revision={selectedRevision}
            content={content}
            previousContent={previousContent}
            loading={detailLoading}
            error={detailError}
            canRollback={canRollback}
            rollingBack={rollingBack}
            t={t}
            onRetry={() => setDetailReloadKey((value) => value + 1)}
            onRollback={handleRollback}
          />
        </section>
      </div>
    </Drawer>
  );
}
