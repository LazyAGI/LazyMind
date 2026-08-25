import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Empty,
  Modal,
  Skeleton,
  Spin,
  Tabs,
  Tag,
  Tooltip,
  Tree,
  message,
} from "antd";
import {
  FileOutlined,
  FileSearchOutlined,
  FolderOutlined,
  RollbackOutlined,
} from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  compareSkillRevisionFileDiff,
  compareSkillRevisionTreeDiff,
  getSkillRevisionFile,
  getSkillRevisionTree,
  listSkillRevisions,
  rollbackSkill,
  RollbackConflictError as SkillRollbackConflictError,
  type SkillRevisionRecord,
  type SkillDiffFileRecord,
  type SkillTreeNodeRecord,
} from '../skillApi';
import type { DataNode } from "antd/es/tree";
import {
  buildDiffLinesWithInline,
  formatDateTime,
  parseMarkdownFrontMatter,
} from "../shared";
import { DiffLineContent } from "./DiffLineContent";
import { getDiffStatusColor, mapDiffEntryLines } from "./skillPackage/skillDiffUtils";
import {
  buildAntTreeData,
  flattenSkillTree,
  pickDefaultFilePath,
  type SkillTreeFileItem,
} from "./skillPackage/skillTreeUtils";
import { buildCurrentRevisionLineage } from "./versionHistoryUtils";

interface ResourceVersionDrawerProps {
  open: boolean;
  resourceId: string;
  resourceName: string;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onRolledBack?: () => void | Promise<void>;
}

type RevisionListItem = {
  revisionId: string;
  parentRevisionId: string;
  revisionNo: number;
  changeSource: string;
  createdAt: string;
  isHead: boolean;
  displayRevisionNo: number;
};

const buildRevisionDiffTreeData = (
  files: SkillDiffFileRecord[],
  t: ResourceVersionDrawerProps["t"],
): DataNode[] => {
  type MutableNode = DataNode & { children: MutableNode[] };
  const roots: MutableNode[] = [];
  const nodes = new Map<string, MutableNode>();

  files.forEach((file) => {
    const parts = file.path.split("/").filter(Boolean);
    parts.forEach((part, index) => {
      const path = parts.slice(0, index + 1).join("/");
      const isLeaf = index === parts.length - 1;
      const isDirLeaf = isLeaf && file.type === "dir";
      const isFile = isLeaf && file.type !== "dir";
      const status = isFile || isDirLeaf ? file.status : "";
      const existing = nodes.get(path);
      if (existing) {
        // Prefer explicit directory status when a later dir entry arrives.
        if (isDirLeaf && status && status !== "unchanged") {
          existing.title = (
            <span className="memory-skill-tree-node-title">
              <Tooltip title={part} placement="right">
                <span className="memory-version-skill-tree-name">{part}</span>
              </Tooltip>
              <Tag bordered={false} color={getDiffStatusColor(status)}>
                {t(`admin.memorySkillDiffStatus_${status}`, { defaultValue: status })}
              </Tag>
            </span>
          );
        }
        return;
      }
      const node: MutableNode = {
        key: path,
        children: [],
        icon: isFile ? <FileOutlined /> : <FolderOutlined />,
        isLeaf: isFile,
        selectable: isFile,
        title: (
          <span className="memory-skill-tree-node-title">
            <Tooltip title={part} placement="right">
              <span className="memory-version-skill-tree-name">{part}</span>
            </Tooltip>
            {status && status !== "unchanged" ? (
              <Tag bordered={false} color={getDiffStatusColor(status)}>
                {t(`admin.memorySkillDiffStatus_${status}`, { defaultValue: status })}
              </Tag>
            ) : null}
          </span>
        ),
      };
      nodes.set(path, node);
      const parentPath = parts.slice(0, index).join("/");
      const parent = nodes.get(parentPath);
      if (parent) parent.children.push(node);
      else roots.push(node);
    });
  });
  return roots;
};

const isDiffContentRequestable = (
  file?: SkillDiffFileRecord | null,
): file is SkillDiffFileRecord =>
  Boolean(
    file &&
      file.type !== "dir" &&
      file.status?.toLowerCase() !== "unchanged" &&
      !file.binary &&
      !file.tooLarge,
  );

const pickDefaultDiffFilePath = (files: SkillDiffFileRecord[]) => {
  const fileEntries = files.filter((file) => file.type !== "dir");
  return (
    fileEntries.find((file) => file.status !== "unchanged")?.path ||
    fileEntries[0]?.path ||
    ""
  );
};

type RevisionChangeKind =
  | "initial"
  | "user_edit"
  | "auto_evolution"
  | "platform_update"
  | "restore"
  | "other";

const userEditChangeSources = new Set([
  "direct_save",
  "draft_commit",
  "draft_confirm",
  "review_accept",
  "metadata_update",
]);

const getRevisionChangeKind = (changeSource: string): RevisionChangeKind => {
  const source = changeSource.trim();
  if (source === "create" || source === "internal_direct") return "initial";
  if (userEditChangeSources.has(source)) return "user_edit";
  if (source === "auto_apply") return "auto_evolution";
  if (source === "distribution_upgrade") return "platform_update";
  if (source === "rollback") return "restore";
  return "other";
};

const changeKindColorMap: Record<RevisionChangeKind, string> = {
  initial: "cyan",
  user_edit: "purple",
  auto_evolution: "blue",
  platform_update: "magenta",
  restore: "orange",
  other: "default",
};

const changeKindLabelKeys: Record<RevisionChangeKind, string> = {
  initial: "admin.memoryVersionKindInitial",
  user_edit: "admin.memoryVersionKindUserEdit",
  auto_evolution: "admin.memoryVersionKindAutoEvolution",
  platform_update: "admin.memoryVersionKindPlatformUpdate",
  restore: "admin.memoryVersionKindRestore",
  other: "admin.memoryVersionKindOther",
};

const changeKindDescriptionKeys: Record<RevisionChangeKind, string> = {
  initial: "admin.memoryVersionChangeDescriptionInitial",
  user_edit: "admin.memoryVersionChangeDescriptionUserEdit",
  auto_evolution: "admin.memoryVersionChangeDescriptionAutoEvolution",
  platform_update: "admin.memoryVersionChangeDescriptionPlatformUpdate",
  restore: "admin.memoryVersionChangeDescriptionRestore",
  other: "admin.memoryVersionChangeDescriptionOther",
};

const getChangeKindLabel = (
  kind: RevisionChangeKind,
  t: ResourceVersionDrawerProps["t"],
) => {
  return t(changeKindLabelKeys[kind]);
};

const getChangeKindDescription = (
  kind: RevisionChangeKind,
  t: ResourceVersionDrawerProps["t"],
) => {
  return t(changeKindDescriptionKeys[kind]);
};

const formatRevisionLabel = (revisionNo: number) => `v${revisionNo}`;

const getContentLines = (content: string) =>
  (content || "-").split("\n").map((text, index) => ({
    id: `${index}-${text}`,
    text: text || " ",
  }));

const isDiffHunkHeader = (text: string) =>
  /^@@\s*-\d+(?:,\d+)?\s+\+\d+(?:,\d+)?\s+@@(?:\s.*)?$/.test(text.trim());

const mapHistoricalSkillDiffLines = (
  lines: Parameters<typeof mapDiffEntryLines>[0],
) => mapDiffEntryLines(lines).filter((line) => !isDiffHunkHeader(line.text));

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

function SkillRevisionContentPanel({
  tree,
  selectedPath,
  content,
  loading,
  t,
  onSelect,
}: {
  tree: SkillTreeNodeRecord | null;
  selectedPath: string;
  content: string;
  loading: boolean;
  t: ResourceVersionDrawerProps["t"];
  onSelect: (file: SkillTreeFileItem) => void;
}) {
  const files = useMemo(() => (tree ? flattenSkillTree(tree) : []), [tree]);
  const selectedFile = files.find((file) => file.path === selectedPath) || null;
  const treeData = useMemo(
    () =>
      tree
        ? buildAntTreeData(tree, new Map(), (file) => (
            <Tooltip title={file.name} placement="right">
              <span className="memory-version-skill-tree-name">{file.name}</span>
            </Tooltip>
          ))
        : [],
    [tree],
  );

  return (
    <div className="memory-version-skill-package">
      <aside className="memory-version-skill-tree">
        <div className="memory-version-content-panel-head">
          {t("admin.memorySkillPackageTreeTitle")}
        </div>
        <Tree
          blockNode
          showIcon
          defaultExpandAll
          treeData={treeData}
          selectedKeys={selectedPath ? [selectedPath] : []}
          onSelect={(keys) => {
            const path = String(keys[0] || "");
            const file = files.find((item) => item.path === path);
            if (file) onSelect(file);
          }}
        />
      </aside>
      <div className="memory-version-skill-content">
        <div className="memory-version-content-panel-head">{selectedPath || "-"}</div>
        {loading ? (
          <div className="memory-version-file-loading"><Spin /></div>
        ) : selectedFile?.binary ? (
          <Alert showIcon type="info" message={t("admin.memoryVersionBinaryFileHint")} />
        ) : (
          <VersionContentPanel label={selectedPath || "-"} content={content} />
        )}
      </div>
    </div>
  );
}

function SkillRevisionDiffPanel({
  files,
  selectedPath,
  lines,
  loading,
  error,
  t,
  onSelect,
}: {
  files: SkillDiffFileRecord[];
  selectedPath: string;
  lines: ReturnType<typeof buildDiffLinesWithInline>;
  loading: boolean;
  error: string;
  t: ResourceVersionDrawerProps["t"];
  onSelect: (path: string) => void;
}) {
  const selectedFile = files.find((file) => file.path === selectedPath);
  const treeData = useMemo(() => buildRevisionDiffTreeData(files, t), [files, t]);

  return (
    <div className="memory-version-skill-package">
      <aside className="memory-version-skill-tree">
        <div className="memory-version-content-panel-head">
          {t("admin.memorySkillPackageTreeTitle")}
        </div>
        <Tree
          blockNode
          showIcon
          defaultExpandAll
          treeData={treeData}
          selectedKeys={selectedPath ? [selectedPath] : []}
          onSelect={(keys) => {
            const path = String(keys[0] || "");
            const file = files.find(
              (item) => item.path === path && item.type !== "dir",
            );
            if (file) onSelect(file.path);
          }}
        />
      </aside>
      <div className="memory-version-skill-content">
        <div className="memory-version-content-panel-head">{selectedPath || "-"}</div>
        {loading ? (
          <div className="memory-version-file-loading"><Spin /></div>
        ) : error ? (
          <Alert showIcon type="error" message={error} />
        ) : selectedFile?.type === "dir" ? (
          <Alert
            showIcon
            type="info"
            message={t("admin.memoryVersionDiffDirectoryHint", {
              status: t(`admin.memorySkillDiffStatus_${selectedFile.status}`, {
                defaultValue: selectedFile.status,
              }),
            })}
          />
        ) : selectedFile?.binary ? (
          <Alert showIcon type="info" message={t("admin.memoryVersionBinaryFileHint")} />
        ) : selectedFile?.tooLarge ? (
          <Alert showIcon type="warning" message={t("admin.memorySkillPackageDiffTooLarge")} />
        ) : lines.length ? (
          <div className="memory-version-diff" aria-label={t("admin.memoryVersionTabDiff")}>
            {lines.map((line, index) => (
              <div key={`${index}-${line.type}-${line.text}`} className={`memory-diff-line is-${line.type}`}>
                <span className="memory-diff-prefix">
                  {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                </span>
                <DiffLineContent line={line} />
              </div>
            ))}
          </div>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("admin.memoryVersionDiffEmpty")} />
        )}
      </div>
    </div>
  );
}

function RevisionDetail({
  revision,
  previousRevision,
  content,
  previousContent,
  revisionTree,
  selectedFilePath,
  selectedFileContent,
  fileLoading,
  diffFiles,
  selectedDiffPath,
  selectedDiffLines,
  diffLoading,
  diffError,
  loading,
  error,
  canRollback,
  rollingBack,
  t,
  onRetry,
  onRollback,
  onSelectFile,
  onSelectDiffFile,
}: {
  revision: RevisionListItem | null;
  previousRevision: RevisionListItem | null;
  content: string;
  previousContent: string;
  revisionTree: SkillTreeNodeRecord | null;
  selectedFilePath: string;
  selectedFileContent: string;
  fileLoading: boolean;
  diffFiles: SkillDiffFileRecord[];
  selectedDiffPath: string;
  selectedDiffLines: ReturnType<typeof buildDiffLinesWithInline>;
  diffLoading: boolean;
  diffError: string;
  loading: boolean;
  error: string;
  canRollback: boolean;
  rollingBack: boolean;
  t: ResourceVersionDrawerProps["t"];
  onRetry: () => void;
  onRollback: () => void;
  onSelectFile: (file: SkillTreeFileItem) => void;
  onSelectDiffFile: (path: string) => void;
}) {
  const currentSkill = useMemo(
    () => parseMarkdownFrontMatter(content),
    [content],
  );
  const previousSkill = useMemo(
    () => parseMarkdownFrontMatter(previousContent),
    [previousContent],
  );
  const changedFileCount = diffFiles.filter(
    (file) => file.status?.toLowerCase() !== "unchanged",
  ).length;
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

  const changeKind = getRevisionChangeKind(revision.changeSource);

  return (
    <div className="memory-version-detail-card">
      <div className="memory-version-detail-summary">
        <div>
          <span>{t("admin.memoryVersionChangeSummary")}</span>
          <strong>{getChangeKindLabel(changeKind, t)}</strong>
          <small>{getChangeKindDescription(changeKind, t)}</small>
        </div>
        <div>
          <span>{t("admin.memoryVersionCompareRange")}</span>
          <strong>
            {previousRevision
              ? `${formatRevisionLabel(previousRevision.displayRevisionNo)} → ${formatRevisionLabel(revision.displayRevisionNo)}`
              : t("admin.memoryVersionInitialVersion")}
          </strong>
          <small>
            {previousRevision
              ? t("admin.memoryVersionCompareRangeHint")
              : t("admin.memoryVersionInitialVersionHint")}
          </small>
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
        defaultActiveKey={
          revision.changeSource === "metadata_update"
            ? "metadata"
            : previousRevision
              ? "diff"
              : "content"
        }
        className="memory-version-detail-tabs"
        items={[
          ...(previousRevision
            ? [
                {
                  key: "diff",
                  label: t("admin.memoryVersionTabComparePrevious"),
                  children: (
                    <div className="memory-version-diff-view">
                      <div className="memory-version-compare-banner">
                        <strong>
                          {formatRevisionLabel(previousRevision.displayRevisionNo)} → {formatRevisionLabel(revision.displayRevisionNo)}
                        </strong>
                        <span>
                          {t("admin.memoryVersionCompareFilesHint", {
                            count: changedFileCount,
                          })}
                        </span>
                      </div>
                      <SkillRevisionDiffPanel
                        files={diffFiles}
                        selectedPath={selectedDiffPath}
                        lines={selectedDiffLines}
                        loading={diffLoading}
                        error={diffError}
                        t={t}
                        onSelect={onSelectDiffFile}
                      />
                    </div>
                  ),
                },
              ]
            : []),
          {
            key: "content",
            label: t("admin.memoryVersionTabAfter"),
            children: (
              <SkillRevisionContentPanel
                tree={revisionTree}
                selectedPath={selectedFilePath}
                content={selectedFileContent}
                loading={fileLoading}
                t={t}
                onSelect={onSelectFile}
              />
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
  t,
  onClose,
  onRolledBack,
}: ResourceVersionDrawerProps) {
  const [revisions, setRevisions] = useState<RevisionListItem[]>([]);
  const [selectedRevisionId, setSelectedRevisionId] = useState("");
  const [content, setContent] = useState("");
  const [previousContent, setPreviousContent] = useState("");
  const [revisionTree, setRevisionTree] = useState<SkillTreeNodeRecord | null>(null);
  const [selectedFilePath, setSelectedFilePath] = useState("");
  const [selectedFileContent, setSelectedFileContent] = useState("");
  const [fileLoading, setFileLoading] = useState(false);
  const [diffFiles, setDiffFiles] = useState<SkillDiffFileRecord[]>([]);
  const [diffRevisionId, setDiffRevisionId] = useState("");
  const [diffBaseRevisionId, setDiffBaseRevisionId] = useState("");
  const [selectedDiffPath, setSelectedDiffPath] = useState("");
  const [selectedDiffLines, setSelectedDiffLines] = useState<ReturnType<typeof buildDiffLinesWithInline>>([]);
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffError, setDiffError] = useState("");
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState("");
  const [detailError, setDetailError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [detailReloadKey, setDetailReloadKey] = useState(0);
  const [rollingBack, setRollingBack] = useState(false);
  const fileRequestIdRef = useRef(0);
  const diffRequestIdRef = useRef(0);
  const revisionListRequestIdRef = useRef(0);
  const loadedRevisionListRequestIdRef = useRef(0);
  const [skillRevisionCache, setSkillRevisionCache] = useState<SkillRevisionRecord[]>(
    [],
  );

  const selectedRevision =
    revisions.find((item) => item.revisionId === selectedRevisionId) || null;
  const selectedPreviousRevision = selectedRevision?.parentRevisionId
    ? revisions.find(
        (item) => item.revisionId === selectedRevision.parentRevisionId,
      ) || null
    : null;
  const canRollback = Boolean(selectedRevision && !selectedRevision.isHead);

  useEffect(() => {
    if (!open) {
      return;
    }
    setSelectedRevisionId("");
    setContent("");
    setPreviousContent("");
    setRevisionTree(null);
    setSelectedFilePath("");
    setSelectedFileContent("");
    setDiffFiles([]);
    setDiffRevisionId("");
    setDiffBaseRevisionId("");
    setSelectedDiffPath("");
    setSelectedDiffLines([]);
    setDiffLoading(false);
    setDiffError("");
    setDetailError("");
    setRevisions([]);
    setSkillRevisionCache([]);
  }, [open, resourceId]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    if (!resourceId) {
      return undefined;
    }

    const requestId = revisionListRequestIdRef.current + 1;
    revisionListRequestIdRef.current = requestId;
    loadedRevisionListRequestIdRef.current = 0;
    let ignore = false;
    setLoading(true);
    setErrorMessage("");
    void (async () => {
      try {
        const items = await listSkillRevisions(resourceId);
        if (ignore) {
          return;
        }
        const nextRevisions = buildCurrentRevisionLineage(
          items.map((item) => ({
            revisionId: item.revisionId,
            parentRevisionId: item.parentRevisionId,
            revisionNo: item.revisionNo,
            changeSource: item.changeSource,
            createdAt: item.createdAt,
            isHead: item.isHead,
          })),
        );
        setSkillRevisionCache(items);
        setRevisions(nextRevisions);
        loadedRevisionListRequestIdRef.current = requestId;
        const headRevision = nextRevisions.find((r) => r.isHead);
        setSelectedRevisionId(headRevision?.revisionId || nextRevisions[0]?.revisionId || '');
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load resource versions failed:", error);
        setErrorMessage(getLocalizedErrorMessage(error));
        setRevisions([]);
        setSkillRevisionCache([]);
      } finally {
        if (!ignore) {
          setLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [open, reloadKey, resourceId, t]);

  useEffect(() => {
    const revisionListReady =
      loadedRevisionListRequestIdRef.current !== 0 &&
      loadedRevisionListRequestIdRef.current === revisionListRequestIdRef.current;
    if (!open || !selectedRevisionId || !revisionListReady) {
      setContent("");
      setPreviousContent("");
      setRevisionTree(null);
      setSelectedFilePath("");
      setSelectedFileContent("");
      setDiffFiles([]);
      setDiffRevisionId("");
      setDiffBaseRevisionId("");
      setSelectedDiffPath("");
      setSelectedDiffLines([]);
      setDiffLoading(false);
      setDiffError("");
      setDetailError("");
      return undefined;
    }

    let ignore = false;
    fileRequestIdRef.current += 1;
    diffRequestIdRef.current += 1;
    setDetailLoading(true);
    setFileLoading(false);
    setDetailError("");
    setDiffFiles([]);
    setDiffRevisionId("");
    setDiffBaseRevisionId("");
    setSelectedDiffPath("");
    setSelectedDiffLines([]);
    setDiffLoading(false);
    setDiffError("");
    void (async () => {
      try {
        const selectedRecord = skillRevisionCache.find(
          (item) => item.revisionId === selectedRevisionId,
        );
        if (!selectedRecord) {
          return;
        }
        // Rollbacks create branches, and the 50-revision retention limit may
        // prune an item's parent. Only request an extant diff base.
        const previousRevision =
          selectedRecord.parentRevisionId
            ? skillRevisionCache.find(
                (item) => item.revisionId === selectedRecord.parentRevisionId,
              )
            : undefined;
        const [currentContent, prevContent, tree, revisionDiff] =
          await Promise.all([
            getSkillRevisionFile(resourceId, selectedRevisionId),
            previousRevision
              ? getSkillRevisionFile(resourceId, previousRevision.revisionId)
              : Promise.resolve(""),
            getSkillRevisionTree(resourceId, selectedRevisionId),
            previousRevision
              ? compareSkillRevisionTreeDiff(
                  resourceId,
                  previousRevision.revisionId,
                  selectedRevisionId,
                )
              : Promise.resolve(null),
          ]);
        if (ignore) {
          return;
        }
        setContent(currentContent);
        setPreviousContent(prevContent);
        setRevisionTree(tree);
        const defaultPath = pickDefaultFilePath(flattenSkillTree(tree));
        setSelectedFilePath(defaultPath);
        setSelectedFileContent(
          defaultPath === "SKILL.md" ? currentContent : "",
        );
        const nextDiffFiles =
          revisionDiff?.files ||
          flattenSkillTree(tree).map((file) => ({
            path: file.path,
            status: "added",
            binary: file.binary,
            type: file.type === "dir" ? "dir" : "file",
            tooLarge: false,
            diffEntryLines: [],
          }));
        setDiffFiles(nextDiffFiles);
        setDiffRevisionId(selectedRevisionId);
        setDiffBaseRevisionId(previousRevision?.revisionId || "");
        setSelectedDiffPath(pickDefaultDiffFilePath(nextDiffFiles));
        setSelectedDiffLines([]);
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
    open,
    resourceId,
    selectedRevisionId,
    skillRevisionCache,
    t,
  ]);

  useEffect(() => {
    if (
      !open ||
      !selectedRevisionId ||
      diffRevisionId !== selectedRevisionId ||
      !selectedDiffPath ||
      !diffFiles.some((file) => file.path === selectedDiffPath)
    ) {
      setSelectedDiffLines([]);
      return undefined;
    }

    const selectedDiffFile = diffFiles.find(
      (file) => file.path === selectedDiffPath,
    );

    // Diff panel never calls revision /file. Directories, unchanged,
    // binary and too-large entries only need status presentation.
    if (!isDiffContentRequestable(selectedDiffFile)) {
      setSelectedDiffLines([]);
      setDiffLoading(false);
      setDiffError("");
      return undefined;
    }

    if (selectedDiffFile.diffEntryLines.length) {
      setSelectedDiffLines(
        mapHistoricalSkillDiffLines(selectedDiffFile.diffEntryLines),
      );
      setDiffLoading(false);
      setDiffError("");
      return undefined;
    }

    if (!diffBaseRevisionId) {
      setSelectedDiffLines([]);
      setDiffLoading(false);
      setDiffError("");
      return undefined;
    }

    const requestId = diffRequestIdRef.current + 1;
    diffRequestIdRef.current = requestId;
    let ignore = false;
    setDiffLoading(true);
    setDiffError("");
    void (async () => {
      try {
        // Re-check before requesting in case state changed mid-flight.
        const latestSelected = diffFiles.find(
          (file) => file.path === selectedDiffPath,
        );
        if (!isDiffContentRequestable(latestSelected)) {
          if (!ignore && diffRequestIdRef.current === requestId) {
            setSelectedDiffLines([]);
          }
          return;
        }

        const file = await compareSkillRevisionFileDiff(
          resourceId,
          diffBaseRevisionId,
          selectedRevisionId,
          selectedDiffPath,
        );
        if (ignore || diffRequestIdRef.current !== requestId) {
          return;
        }

        if (file.binary || file.tooLarge || file.type === "dir") {
          setDiffFiles((prev) =>
            prev.map((item) =>
              item.path === selectedDiffPath
                ? {
                    ...item,
                    binary: file.binary,
                    tooLarge: file.tooLarge,
                    type: file.type || item.type,
                    status: file.status || item.status,
                    diffEntryLines: file.diffEntryLines,
                  }
                : item,
            ),
          );
          setSelectedDiffLines([]);
          return;
        }

        setSelectedDiffLines(mapHistoricalSkillDiffLines(file.diffEntryLines));
      } catch (error) {
        if (!ignore && diffRequestIdRef.current === requestId) {
          console.error("Load revision diff file failed:", error);
          setDiffError(getLocalizedErrorMessage(error));
          setSelectedDiffLines([]);
        }
      } finally {
        if (!ignore && diffRequestIdRef.current === requestId) {
          setDiffLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [
    diffBaseRevisionId,
    diffFiles,
    diffRevisionId,
    open,
    resourceId,
    selectedDiffPath,
    selectedRevisionId,
  ]);

  const handleSelectRevisionFile = async (file: SkillTreeFileItem) => {
    const requestId = fileRequestIdRef.current + 1;
    fileRequestIdRef.current = requestId;
    setSelectedFilePath(file.path);
    if (file.binary) {
      setSelectedFileContent("");
      return;
    }
    setFileLoading(true);
    try {
      const nextContent = await getSkillRevisionFile(resourceId, selectedRevisionId, file.path);
      if (fileRequestIdRef.current === requestId) {
        setSelectedFileContent(nextContent);
      }
    } catch (error) {
      console.error("Load revision file failed:", error);
      message.error(getLocalizedErrorMessage(error));
    } finally {
      if (fileRequestIdRef.current === requestId) {
        setFileLoading(false);
      }
    }
  };

  const handleRollback = () => {
    if (!selectedRevision || selectedRevision.isHead) {
      return;
    }

    Modal.confirm({
      title: t('admin.memoryVersionRollbackConfirmTitle'),
      content: t('admin.memoryVersionRollbackConfirmContent', {
        version: formatRevisionLabel(selectedRevision.displayRevisionNo),
        name: resourceName || resourceId,
      }),
      okText: t('admin.memoryVersionRollbackButton'),
      cancelText: t('common.cancel'),
      onOk: async () => {
        setRollingBack(true);
        try {
          await rollbackSkill(resourceId, selectedRevision.revisionId);
          message.success(t('admin.memoryVersionRollbackSuccess'));
          setReloadKey((value) => value + 1);
          await onRolledBack?.();
        } catch (error) {
          const isConflict = error instanceof SkillRollbackConflictError;
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
      width="min(1320px, calc(100vw - 24px))"
      open={open}
      title={title}
      className="memory-version-drawer"
      onClose={onClose}
      extra={
        <Tag bordered={false} className="memory-version-resource-tag">
          {t("admin.memoryVersionResourceSkill")}
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
                const changeKind = getRevisionChangeKind(item.changeSource);
                const label = getChangeKindLabel(changeKind, t);
                const description = getChangeKindDescription(changeKind, t);

                return (
                  <button
                    key={item.revisionId}
                    type="button"
                    className={`memory-version-list-item${active ? " is-active" : ""}`}
                    onClick={() => setSelectedRevisionId(item.revisionId)}
                  >
                    <span className="memory-version-list-item-main">
                      <strong>
                        {formatRevisionLabel(item.displayRevisionNo)}
                        {item.isHead ? (
                          <em className="memory-version-current-badge">
                            {t("admin.memoryVersionCurrentBadge")}
                          </em>
                        ) : null}
                      </strong>
                      <span>{formatDateTime(item.createdAt)}</span>
                      <small>{description}</small>
                    </span>
                    <Tag color={changeKindColorMap[changeKind]}>
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
            previousRevision={selectedPreviousRevision}
            content={content}
            previousContent={previousContent}
            revisionTree={revisionTree}
            selectedFilePath={selectedFilePath}
            selectedFileContent={selectedFileContent}
            fileLoading={fileLoading}
            diffFiles={diffFiles}
            selectedDiffPath={selectedDiffPath}
            selectedDiffLines={selectedDiffLines}
            diffLoading={diffLoading}
            diffError={diffError}
            loading={detailLoading}
            error={detailError}
            canRollback={canRollback}
            rollingBack={rollingBack}
            t={t}
            onRetry={() => setDetailReloadKey((value) => value + 1)}
            onRollback={handleRollback}
            onSelectFile={handleSelectRevisionFile}
            onSelectDiffFile={setSelectedDiffPath}
          />
        </section>
      </div>
    </Drawer>
  );
}
