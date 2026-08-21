import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Empty,
  Input,
  Modal,
  Space,
  Spin,
  Tag,
  Tooltip,
  Tree,
  Typography,
  Upload,
  message,
} from "antd";
import {
  CheckOutlined,
  CloseOutlined,
  DeleteOutlined,
  FileAddOutlined,
  FolderAddOutlined,
  RollbackOutlined,
  SaveOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import type { DataNode } from "antd/es/tree";
import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";
import { getLocalizedErrorMessage } from "@/components/request";
import { splitMarkdownFrontMatter } from "../../shared";
import {
  commitSkillDraft,
  commitSkillDraftReview,
  compareSkillFileDiff,
  compareSkillTreeDiff,
  confirmSkillDraft,
  deleteSkillDraftPath,
  discardSkillDraft,
  getSkillDraftStatus,
  getSkillTree,
  hasSkillDraftChanges,
  mkdirSkillDraftPath,
  probeSkillAgentReviewMode,
  readSkillFsFile,
  submitSkillDraftReviewActions,
  undoSkillDraftReview,
  uploadSkillDraftFile,
  writeSkillDraftText,
  SKILL_MD_PATH,
  type SkillDiffFileRecord,
  type SkillDraftReviewMeta,
  type SkillDraftReviewDecision,
  type SkillDraftStatusRecord,
  type SkillTreeNodeRecord,
} from "../../skillApi";
import { uploadSkillTempFile } from "../../skillUpload";
import SkillDiffHunkPanel from "./SkillDiffHunkPanel";
import {
  buildDiffHunkBlocks,
  getDiffStatusColor,
  isPendingHunkDecision,
  mapSkillDiffEntryLines,
  summarizeSkillReviewFiles,
  type SkillReviewPendingHunk,
  type SkillReviewStats,
} from "./skillDiffUtils";
import {
  buildAntTreeData,
  buildDiffStatusMap,
  buildSkillItemPath,
  collectChangedFilePaths,
  collectSkillTreeDirectories,
  flattenSkillTree,
  isMarkdownSkillFile,
  pickDefaultFilePath,
  resolveCreateParentPath,
} from "./skillTreeUtils";

const SKILL_UPLOAD_ACCEPT_EXTS = new Set([
  ".md", ".markdown",
  ".txt", ".json", ".yaml", ".yml", ".toml",
  ".py", ".js", ".ts", ".css", ".html", ".sh",
]);

const SKILL_UPLOAD_ACCEPT_ATTR =
  ".md,.markdown,.txt,.json,.yaml,.yml,.toml,.py,.js,.ts,.css,.html,.sh";

interface SkillPackageEditorProps {
  skillId: string;
  canEdit: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onSkillUpdated?: () => void | Promise<void>;
}

const { Text } = Typography;

interface CachedFileContent {
  content: string;
  binary: boolean;
}

interface SkillReviewSnapshot extends SkillReviewStats {
  reviewId: string;
  reviewVersion: number;
  canUndo: boolean;
  pendingHunks: SkillReviewPendingHunk[];
}

const EllipsisText = ({
  text,
  className = "",
}: {
  text: string;
  className?: string;
}) => (
  <Text ellipsis={{ tooltip: text }} className={`memory-skill-ellipsis-text ${className}`.trim()}>
    {text}
  </Text>
);

export default function SkillPackageEditor({
  skillId,
  canEdit,
  t,
  onSkillUpdated,
}: SkillPackageEditorProps) {
  const [loading, setLoading] = useState(true);
  const [errorMessage, setErrorMessage] = useState("");
  const [treeRoot, setTreeRoot] = useState<SkillTreeNodeRecord | null>(null);
  const [diffFiles, setDiffFiles] = useState<SkillDiffFileRecord[]>([]);
  const [draftStatus, setDraftStatus] = useState<SkillDraftStatusRecord | null>(null);
  const [reviewMode, setReviewMode] = useState(false);
  const [selectedPath, setSelectedPath] = useState("");
  const [fileContent, setFileContent] = useState("");
  const [originalContent, setOriginalContent] = useState("");
  const [skillFrontMatter, setSkillFrontMatter] = useState("");
  const [fileBinary, setFileBinary] = useState<boolean | null>(null);
  const [fileLoading, setFileLoading] = useState(false);
  const [fileDiff, setFileDiff] = useState<SkillDiffFileRecord | null>(null);
  const [isEditing, setIsEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [committing, setCommitting] = useState(false);
  const [reviewedPaths, setReviewedPaths] = useState<Set<string>>(new Set());
  const [reviewMeta, setReviewMeta] = useState<SkillDraftReviewMeta | null>(null);
  const [reviewSnapshot, setReviewSnapshot] = useState<SkillReviewSnapshot | null>(null);
  const [reviewSnapshotLoading, setReviewSnapshotLoading] = useState(false);
  const [reviewSnapshotError, setReviewSnapshotError] = useState("");
  const [fileHunkSummaries, setFileHunkSummaries] = useState<
    Record<string, { hunkIds: string[]; allDecided: boolean }>
  >({});
  const [hunkSubmitting, setHunkSubmitting] = useState<Record<string, SkillDraftReviewDecision>>({});
  const [batchSubmitting, setBatchSubmitting] = useState<"accept" | "reject" | "">("");
  const [undoing, setUndoing] = useState(false);
  const [createFileOpen, setCreateFileOpen] = useState(false);
  const [createDirOpen, setCreateDirOpen] = useState(false);
  const [newParentPath, setNewParentPath] = useState("");
  const [newItemName, setNewItemName] = useState("");
  const contentCacheRef = useRef<Map<string, CachedFileContent>>(new Map());
  const reviewSnapshotRequestRef = useRef(0);
  const reviewMutationLockRef = useRef(false);

  const flatFiles = useMemo(
    () => (treeRoot ? flattenSkillTree(treeRoot) : []),
    [treeRoot],
  );
  const diffStatusMap = useMemo(() => buildDiffStatusMap(diffFiles), [diffFiles]);
  const changedPaths = useMemo(() => collectChangedFilePaths(diffFiles), [diffFiles]);
  const selectedFile = useMemo(
    () => flatFiles.find((item) => item.path === selectedPath) || null,
    [flatFiles, selectedPath],
  );
  const selectedFileBinary = fileBinary ?? selectedFile?.binary ?? false;
  const hasLoadedTextContent = fileBinary !== null && fileContent.length > 0;
  const canPreviewSelectedFileAsText = Boolean(
    selectedFile && (!selectedFileBinary || hasLoadedTextContent),
  );
  const canEditSelectedFile = Boolean(selectedFile && !selectedFileBinary);
  const hasLocalDraft = Boolean(
    draftStatus?.hasUncommittedDraft || (draftStatus?.overlayCount ?? 0) > 0,
  );
  const allFilesViewed =
    !reviewMode || changedPaths.every((path) => reviewedPaths.has(path));
  const usesHunkReview = Boolean(reviewSnapshot?.reviewId || reviewMeta?.reviewId);
  const allHunksDecided =
    !usesHunkReview ||
    changedPaths.every((path) => {
      const summary = fileHunkSummaries[path];
      return Boolean(summary?.allDecided);
    });
  const allReviewed = usesHunkReview
    ? Boolean(reviewSnapshot && !reviewSnapshotError && reviewSnapshot.pending === 0)
    : allFilesViewed && allHunksDecided;
  const canUndoReview = Boolean(reviewMode && reviewMeta?.canUndo && reviewMeta.reviewId);
  const reviewMutationBusy = Boolean(
    committing ||
      undoing ||
      reviewSnapshotLoading ||
      batchSubmitting ||
      Object.keys(hunkSubmitting).length > 0,
  );
  const canBulkReview = Boolean(
    canEdit &&
      reviewMode &&
      usesHunkReview &&
      reviewSnapshot &&
      !reviewSnapshotLoading &&
      !reviewSnapshotError &&
      reviewSnapshot.pending > 0 &&
      reviewSnapshot.pendingHunks.length === reviewSnapshot.pending &&
      !reviewMutationBusy,
  );
  const canBulkReject = Boolean(
    canEdit &&
      reviewMode &&
      usesHunkReview &&
      reviewSnapshot &&
      !reviewSnapshotLoading &&
      !reviewSnapshotError &&
      reviewSnapshot.pending > 0 &&
      !reviewMutationBusy,
  );

  const directoryPaths = useMemo(
    () => new Set(treeRoot ? collectSkillTreeDirectories(treeRoot) : []),
    [treeRoot],
  );

  const renderPathLabel = (value: string) =>
    value === "" ? t("admin.memorySkillPackageRootPath") : value;

  const openCreateModal = (mode: "file" | "dir") => {
    setNewParentPath(resolveCreateParentPath(selectedPath, directoryPaths));
    setNewItemName("");
    if (mode === "file") {
      setCreateFileOpen(true);
      return;
    }
    setCreateDirOpen(true);
  };

  const closeCreateModal = () => {
    setCreateFileOpen(false);
    setCreateDirOpen(false);
    setNewParentPath("");
    setNewItemName("");
  };

  const refreshPackage = useCallback(async () => {
    setLoading(true);
    setErrorMessage("");
    contentCacheRef.current.clear();
    try {
      const [tree, status] = await Promise.all([
        getSkillTree(skillId),
        getSkillDraftStatus(skillId),
      ]);

      setTreeRoot(tree);
      setDraftStatus(status);

      const hasDraftChanges = hasSkillDraftChanges(status);

      let nextDiffFiles: SkillDiffFileRecord[] = [];
      if (hasDraftChanges) {
        const treeDiff = await compareSkillTreeDiff(skillId);
        nextDiffFiles = treeDiff.files;
        setDiffFiles(nextDiffFiles);
      } else {
        setDiffFiles([]);
      }

      const agentReview = await probeSkillAgentReviewMode(
        skillId,
        status,
        collectChangedFilePaths(nextDiffFiles),
      );

      setReviewMode(agentReview);
      if (!agentReview) {
        setReviewMeta(null);
        setFileHunkSummaries({});
      }

      const files = flattenSkillTree(tree);
      const directories = new Set(collectSkillTreeDirectories(tree));
      const defaultPath = pickDefaultFilePath(files);
      setSelectedPath((previous) =>
        files.some((file) => file.path === previous) || directories.has(previous)
          ? previous
          : defaultPath,
      );

      if (agentReview && nextDiffFiles.length) {
        const firstChanged = collectChangedFilePaths(nextDiffFiles).find((path) =>
          files.some((file) => file.path === path),
        );
        if (firstChanged) {
          setSelectedPath(firstChanged);
        }
      }
    } catch (error) {
      console.error("Load skill package failed:", error);
      setErrorMessage(getLocalizedErrorMessage(error));
    } finally {
      setLoading(false);
    }
  }, [skillId, t]);

  useEffect(() => {
    void refreshPackage();
  }, [refreshPackage]);

  const updateFileHunkSummary = useCallback((path: string, diff: SkillDiffFileRecord) => {
    const hunks = buildDiffHunkBlocks(mapSkillDiffEntryLines(diff.diffEntryLines));
    const hunkIds = hunks.map((hunk) => hunk.hunkId);
    const allDecided =
      hunks.length === 0 ||
      hunks.every((hunk) => !isPendingHunkDecision(hunk.decision));
    setFileHunkSummaries((previous) => ({
      ...previous,
      [path]: { hunkIds, allDecided },
    }));
  }, []);

  const loadReviewSnapshot = useCallback(async (): Promise<SkillReviewSnapshot | null> => {
    const requestId = ++reviewSnapshotRequestRef.current;
    if (!reviewMode || !changedPaths.length) {
      if (requestId === reviewSnapshotRequestRef.current) {
        setReviewSnapshot(null);
        setReviewSnapshotError("");
        setReviewSnapshotLoading(false);
      }
      return null;
    }

    setReviewSnapshotLoading(true);
    setReviewSnapshotError("");
    try {
      const files = await Promise.all(
        changedPaths.map((path) => compareSkillFileDiff(skillId, path)),
      );
      if (requestId !== reviewSnapshotRequestRef.current) {
        return null;
      }

      const reviewFile = files.find((file) => file.review?.reviewId);
      if (!reviewFile?.review) {
        setReviewSnapshot(null);
        setReviewSnapshotError(t("admin.memorySkillReviewSnapshotUnavailable"));
        return null;
      }

      const summary = summarizeSkillReviewFiles(files);
      const snapshot: SkillReviewSnapshot = {
        reviewId: reviewFile.review.reviewId,
        reviewVersion: reviewFile.review.reviewVersion,
        canUndo: reviewFile.review.canUndo,
        ...summary.stats,
        pendingHunks: summary.pendingHunks,
      };
      const nextSummaries = files.reduce<
        Record<string, { hunkIds: string[]; allDecided: boolean }>
      >((result, file) => {
        const hunks = buildDiffHunkBlocks(mapSkillDiffEntryLines(file.diffEntryLines));
        result[file.path] = {
          hunkIds: hunks.map((hunk) => hunk.hunkId),
          allDecided: hunks.every((hunk) => !isPendingHunkDecision(hunk.decision)),
        };
        return result;
      }, {});

      setReviewSnapshot(snapshot);
      setReviewMeta((previous) => ({
        ...(previous || {
          reviewId: snapshot.reviewId,
          reviewVersion: snapshot.reviewVersion,
          canUndo: snapshot.canUndo,
        }),
        reviewId: snapshot.reviewId,
        reviewVersion: snapshot.reviewVersion,
        canUndo: snapshot.canUndo,
        pendingCount: snapshot.pending,
        acceptedCount: snapshot.accepted,
        rejectedCount: snapshot.rejected,
      }));
      setFileHunkSummaries(nextSummaries);
      setReviewedPaths(new Set(changedPaths));
      return snapshot;
    } catch (error) {
      if (requestId === reviewSnapshotRequestRef.current) {
        setReviewSnapshotError(
          getLocalizedErrorMessage(error) ||
            t("admin.memorySkillReviewSnapshotUnavailable"),
        );
      }
      return null;
    } finally {
      if (requestId === reviewSnapshotRequestRef.current) {
        setReviewSnapshotLoading(false);
      }
    }
  }, [changedPaths, reviewMode, skillId, t]);

  useEffect(() => {
    if (!reviewMode) {
      reviewSnapshotRequestRef.current += 1;
      setReviewSnapshot(null);
      setReviewSnapshotError("");
      setReviewSnapshotLoading(false);
      return;
    }

    void loadReviewSnapshot();
    return () => {
      reviewSnapshotRequestRef.current += 1;
    };
  }, [loadReviewSnapshot, reviewMode]);

  const loadFileView = useCallback(
    async (path: string) => {
      if (!path) {
        return;
      }
      setFileLoading(true);
      setFileDiff(null);
      setFileBinary(null);
      setIsEditing(false);
      try {
        const status = diffStatusMap.get(path);
        const shouldShowDiff = Boolean(
          status && status !== "unchanged" && status !== "deleted",
        );

        if (shouldShowDiff) {
          const diff = await compareSkillFileDiff(skillId, path);
          setFileDiff(diff);
          if (diff.review) {
            setReviewMeta(diff.review);
          }
          updateFileHunkSummary(path, diff);
          if (reviewMode) {
            setReviewedPaths((previous) => new Set(previous).add(path));
          }
        }

        if (status === "deleted") {
          setFileContent("");
          setOriginalContent("");
          setFileBinary(null);
          return;
        }

        const toEditorContent = (content: string) => {
          if (path !== SKILL_MD_PATH) {
            setSkillFrontMatter("");
            return content;
          }
          const split = splitMarkdownFrontMatter(content);
          setSkillFrontMatter(split?.frontMatter || "");
          return split ? split.content : content;
        };

        const cachedFile = contentCacheRef.current.get(path);
        if (cachedFile && !reviewMode) {
          const editorContent = toEditorContent(cachedFile.content);
          setFileContent(editorContent);
          setOriginalContent(editorContent);
          setFileBinary(cachedFile.binary);
          return;
        }

        const file = await readSkillFsFile(skillId, path);
        contentCacheRef.current.set(path, {
          content: file.content,
          binary: file.binary,
        });
        const editorContent = toEditorContent(file.content);
        setFileContent(editorContent);
        setOriginalContent(editorContent);
        setFileBinary(file.binary);
      } catch (error) {
        console.error("Load skill file failed:", error);
      } finally {
        setFileLoading(false);
      }
    },
    [diffStatusMap, reviewMode, skillId, t, updateFileHunkSummary],
  );

  useEffect(() => {
    if (!selectedPath || !selectedFile || loading) {
      return;
    }
    void loadFileView(selectedPath);
  }, [loadFileView, loading, selectedFile, selectedPath]);

  const handleDeletePath = useCallback(
    (path: string, isDirectory: boolean) => {
      if (!path || !draftStatus || reviewMode) {
        return;
      }
      Modal.confirm({
        title: isDirectory
          ? t("admin.memorySkillPackageDeleteFolderConfirmTitle")
          : t("admin.memorySkillPackageDeleteConfirmTitle"),
        content: isDirectory
          ? t("admin.memorySkillPackageDeleteFolderConfirmContent", { path })
          : t("admin.memorySkillPackageDeleteConfirmContent", { path }),
        okText: t("common.delete"),
        cancelText: t("common.cancel"),
        okButtonProps: { danger: true },
        onOk: async () => {
          try {
            const nextVersion = await deleteSkillDraftPath(skillId, {
              path,
              expectedDraftVersion: draftStatus.draftVersion,
              recursive: isDirectory,
            });
            setDraftStatus((previous) =>
              previous
                ? { ...previous, draftVersion: nextVersion, hasUncommittedDraft: true }
                : previous,
            );
            await refreshPackage();
            message.success(
              isDirectory
                ? t("admin.memorySkillPackageDeleteFolderSuccess")
                : t("admin.memorySkillPackageDeleteSuccess"),
            );
          } catch (error) {
            console.error(
              isDirectory ? "Delete skill folder failed:" : "Delete skill file failed:",
              error,
            );
          }
        },
      });
    },
    [draftStatus, refreshPackage, reviewMode, skillId, t],
  );

  const handleDeleteFile = () => {
    if (!selectedPath) {
      return;
    }
    handleDeletePath(selectedPath, false);
  };

  const treeData = useMemo<DataNode[]>(() => {
    if (!treeRoot) {
      return [];
    }
    return buildAntTreeData(treeRoot, diffStatusMap, (item, status) => {
      const isDirectory = item.type === "dir";
      const canDelete =
        canEdit &&
        !reviewMode &&
        status !== "deleted" &&
        (isDirectory || item.path !== SKILL_MD_PATH);
      const deleteLabel = isDirectory
        ? t("admin.memorySkillPackageDeleteFolder")
        : t("admin.memorySkillPackageDelete");
      return (
        <span className="memory-skill-tree-node-title">
          <EllipsisText text={item.name} className="memory-skill-tree-node-name" />
          {status && status !== "unchanged" ? (
            <Tag bordered={false} color={getDiffStatusColor(status)} className="memory-skill-tree-status">
              {t(`admin.memorySkillDiffStatus_${status}`, { defaultValue: status })}
            </Tag>
          ) : null}
          {canDelete ? (
            <Tooltip title={deleteLabel}>
              <button
                type="button"
                className="memory-skill-tree-node-delete"
                aria-label={deleteLabel}
                onClick={(event) => {
                  event.stopPropagation();
                  handleDeletePath(item.path, isDirectory);
                }}
                onMouseDown={(event) => event.stopPropagation()}
              >
                <DeleteOutlined />
              </button>
            </Tooltip>
          ) : null}
        </span>
      );
    });
  }, [canEdit, diffStatusMap, handleDeletePath, reviewMode, t, treeRoot]);

  const handleSaveFile = async () => {
    if (!selectedPath || !canEdit || reviewMode || saving) {
      return;
    }
    if (fileContent === originalContent) {
      setIsEditing(false);
      return;
    }

    setSaving(true);
    try {
      const status = draftStatus || (await getSkillDraftStatus(skillId));
      const persistedContent =
        selectedPath === SKILL_MD_PATH && skillFrontMatter
          ? `${skillFrontMatter}${fileContent}`
          : fileContent;
      const nextVersion = await writeSkillDraftText(skillId, {
        path: selectedPath,
        content: persistedContent,
        expectedDraftVersion: status.draftVersion,
      });
      setDraftStatus((previous) =>
        previous ? { ...previous, draftVersion: nextVersion, hasUncommittedDraft: true } : previous,
      );
      setOriginalContent(fileContent);
      setFileBinary(false);
      contentCacheRef.current.set(selectedPath, {
        content: persistedContent,
        binary: false,
      });
      setIsEditing(false);

      const tree = await getSkillTree(skillId);
      setTreeRoot(tree);
      const treeDiff = await compareSkillTreeDiff(skillId);
      setDiffFiles(treeDiff.files);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Save skill file failed:", error);
    } finally {
      setSaving(false);
    }
  };

  const handleCommitDraft = async () => {
    if (!canEdit || reviewMode || committing || !draftStatus) {
      return;
    }
    setCommitting(true);
    try {
      await commitSkillDraft(skillId, draftStatus.draftVersion);
      message.success(t("admin.memorySkillDraftCommitSuccess"));
      await refreshPackage();
      await onSkillUpdated?.();
    } catch (error) {
      console.error("Commit skill draft failed:", error);
    } finally {
      setCommitting(false);
    }
  };

  const refreshCurrentFileDiff = useCallback(async () => {
    if (!selectedPath) {
      return;
    }
    const tree = await getSkillTree(skillId);
    const files = flattenSkillTree(tree);
    const nextSelectedPath = files.some((file) => file.path === selectedPath)
      ? selectedPath
      : pickDefaultFilePath(files);
    setTreeRoot(tree);
    setSelectedPath(nextSelectedPath);
    const treeDiff = await compareSkillTreeDiff(skillId);
    setDiffFiles(treeDiff.files);
    if (nextSelectedPath) {
      const nextFileDiff = await compareSkillFileDiff(skillId, nextSelectedPath);
      setFileDiff(nextFileDiff);
      if (nextFileDiff.review) {
        setReviewMeta(nextFileDiff.review);
      }
      updateFileHunkSummary(nextSelectedPath, nextFileDiff);
    }
  }, [selectedPath, skillId, updateFileHunkSummary]);

  const handleHunkDecision = async (
    hunkId: string,
    decision: SkillDraftReviewDecision,
  ) => {
    if (
      !selectedPath ||
      !reviewMeta?.reviewId ||
      reviewMutationBusy ||
      reviewMutationLockRef.current
    ) {
      return;
    }
    setHunkSubmitting((previous) => ({ ...previous, [hunkId]: decision }));
    try {
      const result = await submitSkillDraftReviewActions(skillId, reviewMeta.reviewId, {
        expectedReviewVersion: reviewMeta.reviewVersion,
        items: [{ hunkId, decision, path: selectedPath }],
      });
      setReviewMeta((previous) =>
        previous
          ? {
              ...previous,
              reviewVersion: result.reviewVersion,
              canUndo: result.canUndo,
            }
          : previous,
      );
      message.success(
        decision === "accept"
          ? t("admin.memorySkillHunkAcceptSuccess")
          : t("admin.memorySkillHunkRejectSuccess"),
      );
      await refreshCurrentFileDiff();
    } catch (error) {
      console.error("Submit skill draft review action failed:", error);
    } finally {
      setHunkSubmitting((previous) => {
        const next = { ...previous };
        delete next[hunkId];
        return next;
      });
    }
  };

  const resetReviewState = () => {
    reviewSnapshotRequestRef.current += 1;
    setReviewSnapshot(null);
    setReviewSnapshotError("");
    setReviewSnapshotLoading(false);
    setReviewedPaths(new Set());
    setReviewMeta(null);
    setFileHunkSummaries({});
  };

  const handleBatchAccept = () => {
    if (!canBulkReview || reviewMutationLockRef.current) {
      return;
    }

    Modal.confirm({
      title: t("admin.memorySkillBatchAcceptConfirmTitle"),
      content: t("admin.memorySkillBatchAcceptConfirmContent", {
        count: reviewSnapshot?.pending ?? 0,
      }),
      okText: t("admin.memorySkillBatchAcceptConfirmOk"),
      cancelText: t("common.cancel"),
      onOk: async () => {
        if (reviewMutationLockRef.current) {
          return;
        }
        reviewMutationLockRef.current = true;
        setBatchSubmitting("accept");
        try {
          const snapshot = await loadReviewSnapshot();
          if (!snapshot || !snapshot.pendingHunks.length) {
            return;
          }
          if (snapshot.pendingHunks.length !== snapshot.pending) {
            message.error(t("admin.memorySkillHunkActionsUnavailable"));
            return;
          }

          const result = await submitSkillDraftReviewActions(skillId, snapshot.reviewId, {
            expectedReviewVersion: snapshot.reviewVersion,
            items: snapshot.pendingHunks.map((hunk) => ({
              path: hunk.path,
              hunkId: hunk.hunkId,
              decision: "accept" as const,
            })),
          });
          const acceptedCount = snapshot.pendingHunks.length;
          setReviewSnapshot((previous) =>
            previous
              ? {
                  ...previous,
                  reviewVersion: result.reviewVersion,
                  canUndo: result.canUndo,
                  accepted: previous.accepted + acceptedCount,
                  pending: previous.pending - acceptedCount,
                  pendingHunks: [],
                }
              : previous,
          );
          setReviewMeta((previous) =>
            previous
              ? {
                  ...previous,
                  reviewVersion: result.reviewVersion,
                  canUndo: result.canUndo,
                  pendingCount: 0,
                  acceptedCount: (previous.acceptedCount ?? 0) + acceptedCount,
                }
              : previous,
          );
          setFileHunkSummaries((previous) =>
            Object.fromEntries(
              Object.entries(previous).map(([path, summary]) => [
                path,
                { ...summary, allDecided: true },
              ]),
            ),
          );
          await refreshCurrentFileDiff().catch((error) => {
            console.error("Refresh skill diff after batch accept failed:", error);
          });
          await loadReviewSnapshot();
          message.success(t("admin.memorySkillBatchAcceptSuccess"));
        } catch (error) {
          console.error("Batch accept skill draft review failed:", error);
          message.error(
            getLocalizedErrorMessage(error) || t("admin.memorySkillBatchActionFailed"),
          );
          await loadReviewSnapshot();
        } finally {
          setBatchSubmitting("");
          reviewMutationLockRef.current = false;
        }
      },
    });
  };

  const discardDraft = async () => {
    if (reviewMutationLockRef.current) {
      return;
    }
    reviewMutationLockRef.current = true;
    setBatchSubmitting("reject");
    try {
      await discardSkillDraft(skillId);
      message.success(t("admin.memorySkillDraftDiscardSuccess"));
      setReviewMode(false);
      resetReviewState();
      await refreshPackage();
      await onSkillUpdated?.();
    } catch (error) {
      console.error("Discard skill draft failed:", error);
      message.error(
        getLocalizedErrorMessage(error) || t("admin.memorySkillBatchActionFailed"),
      );
    } finally {
      setBatchSubmitting("");
      reviewMutationLockRef.current = false;
    }
  };

  const handleBatchReject = () => {
    if (!canBulkReject || reviewMutationLockRef.current) {
      return;
    }

    Modal.confirm({
      title: t("admin.memorySkillBatchRejectConfirmTitle"),
      content: t("admin.memorySkillBatchRejectConfirmContent", {
        count: reviewSnapshot?.pending ?? 0,
      }),
      okText: t("admin.memorySkillBatchRejectConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => discardDraft(),
    });
  };

  const handleUndoReview = async () => {
    if (
      !reviewMode ||
      !reviewMeta?.reviewId ||
      undoing ||
      batchSubmitting ||
      Object.keys(hunkSubmitting).length > 0
    ) {
      return;
    }
    setUndoing(true);
    try {
      const result = await undoSkillDraftReview(
        skillId,
        reviewMeta.reviewId,
        reviewMeta.reviewVersion,
      );
      setReviewMeta((previous) =>
        previous
          ? {
              ...previous,
              reviewVersion: result.reviewVersion,
              canUndo: result.canUndo,
            }
          : previous,
      );
      message.success(t("admin.memorySkillDraftReviewUndoSuccess"));
      setFileHunkSummaries({});
      setReviewedPaths(new Set());
      setReviewSnapshot(null);
      await refreshPackage();
    } catch (error) {
      console.error("Undo skill draft review failed:", error);
    } finally {
      setUndoing(false);
    }
  };

  const handleConfirmReview = async () => {
    if (!canEdit || !reviewMode || committing || !allReviewed || reviewMutationBusy) {
      return;
    }
    setCommitting(true);
    try {
      if (reviewMeta?.reviewId) {
        await commitSkillDraftReview(
          skillId,
          reviewMeta.reviewId,
          reviewMeta.reviewVersion,
        );
      } else {
        await confirmSkillDraft(skillId);
      }
      message.success(t("admin.memorySkillDraftConfirmSuccess"));
      setReviewMode(false);
      resetReviewState();
      await refreshPackage();
      await onSkillUpdated?.();
    } catch (error) {
      console.error("Confirm skill draft failed:", error);
    } finally {
      setCommitting(false);
    }
  };

  const handleDiscardDraft = () => {
    if (reviewMutationBusy || reviewMutationLockRef.current) {
      return;
    }
    Modal.confirm({
      title: t("admin.memorySkillDraftDiscardConfirmTitle"),
      content: t("admin.memorySkillDraftDiscardConfirmContent"),
      okText: t("admin.memorySkillDraftDiscardConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => discardDraft(),
    });
  };

  const handleCreatePath = async (isDirectory: boolean) => {
    const trimmedName = newItemName.trim();
    if (!trimmedName || !draftStatus) {
      message.warning(
        isDirectory
          ? t("admin.memorySkillPackageNewFolderNameRequired")
          : t("admin.memorySkillPackageNewFileNameRequired"),
      );
      return;
    }
    if (isDirectory && trimmedName.includes("/")) {
      message.warning(t("admin.memorySkillPackageNewFolderNameInvalid"));
      return;
    }

    const trimmedPath = buildSkillItemPath(newParentPath, trimmedName);
    if (!trimmedPath) {
      return;
    }

    try {
      let nextVersion = draftStatus.draftVersion;
      if (isDirectory) {
        nextVersion = await mkdirSkillDraftPath(skillId, {
          path: trimmedPath,
          expectedDraftVersion: draftStatus.draftVersion,
        });
      } else {
        nextVersion = await writeSkillDraftText(skillId, {
          path: trimmedPath,
          content: "",
          expectedDraftVersion: draftStatus.draftVersion,
        });
      }
      setDraftStatus((previous) =>
        previous
          ? { ...previous, draftVersion: nextVersion, hasUncommittedDraft: true }
          : previous,
      );
      closeCreateModal();
      await refreshPackage();
      if (!isDirectory) {
        contentCacheRef.current.set(trimmedPath, {
          content: "",
          binary: false,
        });
      }
      setSelectedPath(trimmedPath);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Create skill path failed:", error);
    }
  };

  const renderCreatePathForm = (isDirectory: boolean) => (
    <div className="memory-skill-package-create-form">
      <Text
        type="secondary"
        className="memory-skill-package-create-target"
        title={renderPathLabel(newParentPath)}
      >
        {t("admin.memorySkillPackageCreateTarget", {
          path: renderPathLabel(newParentPath),
        })}
      </Text>
      <Input
        autoFocus
        value={newItemName}
        aria-label={
          isDirectory
            ? t("admin.memorySkillPackageNewFolder")
            : t("admin.memorySkillPackageNewFile")
        }
        placeholder={
          isDirectory
            ? t("admin.memorySkillPackageNewFolderNamePlaceholder")
            : t("admin.memorySkillPackageNewFileNamePlaceholder")
        }
        onChange={(event) => setNewItemName(event.target.value)}
        onPressEnter={() => void handleCreatePath(isDirectory)}
      />
    </div>
  );

  const handleUploadFile = async (file: File) => {
    if (!selectedPath || !draftStatus || reviewMode) {
      return false;
    }
    const ext = file.name.toLowerCase().replace(/^.*(\.[^.]+)$/, "$1");
    if (!SKILL_UPLOAD_ACCEPT_EXTS.has(ext)) {
      message.warning(t("admin.memorySkillPackageUploadFileTypeError"));
      return false;
    }
    if (file.size > 512 * 1024) {
      message.warning(t("admin.memorySkillPackageUploadFileSizeError"));
      return false;
    }
    try {
      const upload = await uploadSkillTempFile(file);
      const nextVersion = await uploadSkillDraftFile(skillId, {
        path: selectedPath,
        uploadId: upload.uploadId,
        expectedDraftVersion: draftStatus.draftVersion,
      });
      setDraftStatus((previous) =>
        previous
          ? { ...previous, draftVersion: nextVersion, hasUncommittedDraft: true }
          : previous,
      );
      await refreshPackage();
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Upload skill file failed:", error);
    }
    return false;
  };

  const renderDiffPanel = () => {
    const diffEntryLines = fileDiff?.diffEntryLines || [];

    if (fileDiff?.binary) {
      return (
        <Alert type="info" showIcon message={t("admin.memorySkillPackageBinaryDiffHint")} />
      );
    }

    if (fileDiff?.tooLarge) {
      return (
        <Alert type="warning" showIcon message={t("admin.memorySkillPackageDiffTooLarge")} />
      );
    }

    if (!diffEntryLines.length) {
      return (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memorySkillPackageDiffEmpty")}
        />
      );
    }

    return (
      <SkillDiffHunkPanel
        diffEntryLines={diffEntryLines}
        stripFrontMatter={selectedPath === SKILL_MD_PATH}
        hunkReviewActive={Boolean(reviewMeta?.reviewId || fileDiff?.review?.reviewId)}
        hunkSubmitting={hunkSubmitting}
        onHunkDecision={(hunk, decision) => void handleHunkDecision(hunk.hunkId, decision)}
        t={t}
      />
    );
  };

  const renderContentPanel = () => {
    if (fileLoading) {
      return (
        <div className="memory-skill-package-panel-loading">
          <Spin />
        </div>
      );
    }

    if (!selectedFile) {
      return (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memorySkillPackageSelectFile")}
        />
      );
    }

    if (selectedFile.type === "dir") {
      return null;
    }

    if (diffStatusMap.get(selectedPath) === "deleted") {
      return (
        <Alert type="warning" showIcon message={t("admin.memorySkillPackageFileDeleted")} />
      );
    }

    const showDiff =
      reviewMode || (diffStatusMap.get(selectedPath) && diffStatusMap.get(selectedPath) !== "unchanged");

    if (showDiff && !isEditing) {
      return renderDiffPanel();
    }

    if (!canPreviewSelectedFileAsText) {
      return (
        <Alert type="info" showIcon message={t("admin.memorySkillPackageBinaryFileHint")} />
      );
    }

    if (isEditing) {
      return (
        <Input.TextArea
          value={fileContent}
          onChange={(event) => setFileContent(event.target.value)}
          autoSize={{ minRows: 18, maxRows: 32 }}
          className="memory-skill-detail-textarea"
        />
      );
    }

    if (isMarkdownSkillFile(selectedFile)) {
      return <MarkdownViewer>{fileContent || "-"}</MarkdownViewer>;
    }

    return <pre className="memory-skill-package-plain">{fileContent || "-"}</pre>;
  };

  const canManageSelectedFile = Boolean(
    canEdit &&
      !reviewMode &&
      selectedFile &&
      selectedFile.type !== "dir" &&
      diffStatusMap.get(selectedPath) !== "deleted",
  );

  if (loading) {
    return (
      <div className="memory-skill-package-loading">
        <Spin />
      </div>
    );
  }

  if (errorMessage) {
    return (
      <Alert
        type="error"
        showIcon
        message={errorMessage}
        action={
          <Button size="small" onClick={() => void refreshPackage()}>
            {t("common.retry")}
          </Button>
        }
      />
    );
  }

  return (
    <div className="memory-skill-package-editor">
      {reviewMode ? (
        <Alert
          type="warning"
          showIcon
          className="memory-skill-package-review-alert"
          message={t("admin.memorySkillPackageReviewTitle")}
          description={t("admin.memorySkillPackageReviewHint")}
        />
      ) : hasLocalDraft ? (
        <Alert
          type="info"
          showIcon
          className="memory-skill-package-review-alert"
          message={t("admin.memorySkillPackageUncommittedTitle")}
          description={t("admin.memorySkillPackageUncommittedHint")}
        />
      ) : null}
      {reviewMode && reviewSnapshotError ? (
        <Alert
          type="error"
          showIcon
          message={reviewSnapshotError}
          action={
            <Button
              size="small"
              loading={reviewSnapshotLoading}
              onClick={() => void loadReviewSnapshot()}
            >
              {t("common.retry")}
            </Button>
          }
        />
      ) : null}

      <div className="memory-skill-package-toolbar">
        <Space wrap>
          {canEdit && !reviewMode ? (
            <>
              <Button icon={<FileAddOutlined />} onClick={() => openCreateModal("file")}>
                {t("admin.memorySkillPackageNewFile")}
              </Button>
              <Button icon={<FolderAddOutlined />} onClick={() => openCreateModal("dir")}>
                {t("admin.memorySkillPackageNewFolder")}
              </Button>
            </>
          ) : null}
          {canEdit && reviewMode && usesHunkReview ? (
            <span className="memory-skill-review-stats" aria-live="polite">
              {reviewSnapshotLoading && !reviewSnapshot
                ? t("admin.memorySkillReviewStatsLoading")
                : t("admin.memorySkillReviewDecisionStats", {
                    accepted: reviewSnapshot?.accepted ?? 0,
                    rejected: reviewSnapshot?.rejected ?? 0,
                    pending: reviewSnapshot?.pending ?? 0,
                  })}
            </span>
          ) : null}
          {canEdit && reviewMode ? (
            <>
              {usesHunkReview ? (
                <>
                  <Button
                    icon={<CheckOutlined />}
                    loading={batchSubmitting === "accept"}
                    disabled={!canBulkReview}
                    onClick={handleBatchAccept}
                  >
                    {t("admin.memorySkillBatchAccept")}
                  </Button>
                  <Button
                    danger
                    icon={<CloseOutlined />}
                    loading={batchSubmitting === "reject"}
                    disabled={!canBulkReject}
                    onClick={handleBatchReject}
                  >
                    {t("admin.memorySkillBatchReject")}
                  </Button>
                </>
              ) : null}
              {canUndoReview ? (
                <Button
                  icon={<RollbackOutlined />}
                  loading={undoing}
                  disabled={
                    batchSubmitting !== "" ||
                    committing ||
                    Object.keys(hunkSubmitting).length > 0
                  }
                  onClick={() => void handleUndoReview()}
                >
                  {t("admin.memorySkillDraftReviewUndo")}
                </Button>
              ) : null}
              <Button
                type="primary"
                loading={committing}
                disabled={!allReviewed || reviewMutationBusy}
                onClick={() => void handleConfirmReview()}
              >
                {t("admin.memorySkillDraftConfirm")}
              </Button>
              <Button
                danger
                loading={batchSubmitting === "reject"}
                disabled={reviewMutationBusy}
                onClick={() => void handleDiscardDraft()}
              >
                {t("admin.memorySkillDraftDiscard")}
              </Button>
            </>
          ) : null}
          {canEdit && !reviewMode && hasLocalDraft ? (
            <>
              <Button
                type="primary"
                icon={<SaveOutlined />}
                loading={committing}
                onClick={() => void handleCommitDraft()}
              >
                {t("admin.memorySkillDraftCommit")}
              </Button>
              <Button
                danger
                loading={batchSubmitting === "reject"}
                disabled={reviewMutationBusy}
                onClick={() => void handleDiscardDraft()}
              >
                {t("admin.memorySkillDraftDiscard")}
              </Button>
            </>
          ) : null}
        </Space>
        {canManageSelectedFile ? (
          <Space wrap className="memory-skill-package-file-actions">
            <Upload
              accept={SKILL_UPLOAD_ACCEPT_ATTR}
              showUploadList={false}
              beforeUpload={(file) => void handleUploadFile(file as File)}
            >
              <Tooltip
                placement="bottomRight"
                title={
                  <>
                    <div>{t("admin.memorySkillPackageUploadFileTooltip").split("\n")[0]}</div>
                    <div style={{ marginTop: 4 }}>{t("admin.memorySkillPackageUploadFileTooltip").split("\n")[1]}</div>
                  </>
                }
              >
                <Button icon={<UploadOutlined />}>{t("admin.memorySkillPackageUploadFile")}</Button>
              </Tooltip>
            </Upload>
            {canEditSelectedFile ? (
              isEditing ? (
                <>
                  <Button onClick={() => setIsEditing(false)} disabled={saving}>
                    {t("common.cancel")}
                  </Button>
                  <Button type="primary" loading={saving} onClick={() => void handleSaveFile()}>
                    {t("common.save")}
                  </Button>
                </>
              ) : (
                <Button onClick={() => setIsEditing(true)}>{t("common.edit")}</Button>
              )
            ) : null}
            {selectedPath !== SKILL_MD_PATH ? (
              <Button danger icon={<DeleteOutlined />} onClick={handleDeleteFile}>
                {t("common.delete")}
              </Button>
            ) : null}
          </Space>
        ) : null}
      </div>

      <div className="memory-skill-package-body">
        <aside
          className="memory-skill-package-tree"
          onClick={(event) => {
            const target = event.target as HTMLElement;
            if (
              target.closest(
                ".ant-tree-treenode, .memory-skill-package-tree-head",
              )
            ) {
              return;
            }
            setSelectedPath("");
          }}
        >
          <div className="memory-skill-package-tree-head">{t("admin.memorySkillPackageTreeTitle")}</div>
          {treeData.length ? (
            <Tree
              showIcon
              blockNode
              selectedKeys={selectedPath ? [selectedPath] : []}
              treeData={treeData}
              onSelect={(keys) => {
                const nextPath = String(keys[0] || "");
                if (nextPath) {
                  setSelectedPath(nextPath);
                }
              }}
            />
          ) : (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t("admin.memorySkillPackageTreeEmpty")}
            />
          )}
        </aside>

        <section className="memory-skill-package-main">
          <div className="memory-skill-package-main-head">
            {selectedPath ? (
              <EllipsisText
                text={selectedPath}
                className="memory-skill-package-main-path memory-skill-package-main-path-strong"
              />
            ) : (
              <strong className="memory-skill-package-main-path">
                {t("admin.memorySkillPackageSelectFile")}
              </strong>
            )}
            {selectedPath && diffStatusMap.get(selectedPath) ? (
              <Tag color={getDiffStatusColor(diffStatusMap.get(selectedPath) || "")}>
                {diffStatusMap.get(selectedPath)}
              </Tag>
            ) : null}
          </div>
          <div className="memory-skill-package-main-content">{renderContentPanel()}</div>
        </section>
      </div>

      <Modal
        open={createFileOpen}
        title={t("admin.memorySkillPackageNewFile")}
        okText={t("common.create")}
        cancelText={t("common.cancel")}
        onCancel={closeCreateModal}
        onOk={() => void handleCreatePath(false)}
      >
        {renderCreatePathForm(false)}
      </Modal>

      <Modal
        open={createDirOpen}
        title={t("admin.memorySkillPackageNewFolder")}
        okText={t("common.create")}
        cancelText={t("common.cancel")}
        onCancel={closeCreateModal}
        onOk={() => void handleCreatePath(true)}
      >
        {renderCreatePathForm(true)}
      </Modal>
    </div>
  );
}
