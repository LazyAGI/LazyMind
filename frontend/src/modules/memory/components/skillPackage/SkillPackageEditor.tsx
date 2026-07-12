import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Spin,
  Tag,
  Tree,
  Typography,
  Upload,
  message,
} from "antd";
import {
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
  resolveParentPathFromSelection,
} from "./skillTreeUtils";

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
  const [fileBinary, setFileBinary] = useState<boolean | null>(null);
  const [fileLoading, setFileLoading] = useState(false);
  const [fileDiff, setFileDiff] = useState<SkillDiffFileRecord | null>(null);
  const [isEditing, setIsEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [committing, setCommitting] = useState(false);
  const [reviewedPaths, setReviewedPaths] = useState<Set<string>>(new Set());
  const [reviewMeta, setReviewMeta] = useState<SkillDraftReviewMeta | null>(null);
  const [fileHunkSummaries, setFileHunkSummaries] = useState<
    Record<string, { hunkIds: string[]; allDecided: boolean }>
  >({});
  const [hunkSubmitting, setHunkSubmitting] = useState<Record<string, SkillDraftReviewDecision>>({});
  const [undoing, setUndoing] = useState(false);
  const [createFileOpen, setCreateFileOpen] = useState(false);
  const [createDirOpen, setCreateDirOpen] = useState(false);
  const [newParentPath, setNewParentPath] = useState("");
  const [newItemName, setNewItemName] = useState("");
  const contentCacheRef = useRef<Map<string, CachedFileContent>>(new Map());

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
  const usesHunkReview = reviewMode && Boolean(reviewMeta?.reviewId);
  const allHunksDecided =
    !usesHunkReview ||
    changedPaths.every((path) => {
      const summary = fileHunkSummaries[path];
      return Boolean(summary?.allDecided);
    });
  const allReviewed = allFilesViewed && allHunksDecided;
  const canUndoReview = Boolean(reviewMode && reviewMeta?.canUndo && reviewMeta.reviewId);

  const directoryOptions = useMemo(() => {
    const directories = treeRoot ? collectSkillTreeDirectories(treeRoot) : [];
    const optionMap = new Map<string, { value: string; label: string }>();
    optionMap.set("", { value: "", label: t("admin.memorySkillPackageRootPath") });
    directories.forEach((path) => {
      optionMap.set(path, { value: path, label: path });
    });
    const selectedParent = resolveParentPathFromSelection(selectedPath);
    if (selectedParent && !optionMap.has(selectedParent)) {
      optionMap.set(selectedParent, { value: selectedParent, label: selectedParent });
    }
    return Array.from(optionMap.values());
  }, [selectedPath, t, treeRoot]);

  const renderPathLabel = (value: string) =>
    value === "" ? t("admin.memorySkillPackageRootPath") : value;

  const openCreateModal = (mode: "file" | "dir") => {
    setNewParentPath(resolveParentPathFromSelection(selectedPath));
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
      const defaultPath = pickDefaultFilePath(files);
      if (defaultPath) {
        setSelectedPath((previous) => previous || defaultPath);
      }

      if (agentReview && nextDiffFiles.length) {
        const firstChanged = collectChangedFilePaths(nextDiffFiles)[0];
        if (firstChanged) {
          setSelectedPath(firstChanged);
        }
      }
    } catch (error) {
      console.error("Load skill package failed:", error);
      setErrorMessage(
        getLocalizedErrorMessage(error, t("admin.memorySkillPackageLoadFailed")) ||
          t("admin.memorySkillPackageLoadFailed"),
      );
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

        const cachedFile = contentCacheRef.current.get(path);
        if (cachedFile && !reviewMode) {
          setFileContent(cachedFile.content);
          setOriginalContent(cachedFile.content);
          setFileBinary(cachedFile.binary);
          return;
        }

        const file = await readSkillFsFile(skillId, path);
        contentCacheRef.current.set(path, {
          content: file.content,
          binary: file.binary,
        });
        setFileContent(file.content);
        setOriginalContent(file.content);
        setFileBinary(file.binary);
      } catch (error) {
        console.error("Load skill file failed:", error);
        message.error(
          getLocalizedErrorMessage(error, t("admin.memorySkillPackageFileLoadFailed")) ||
            t("admin.memorySkillPackageFileLoadFailed"),
        );
      } finally {
        setFileLoading(false);
      }
    },
    [diffStatusMap, reviewMode, skillId, t, updateFileHunkSummary],
  );

  useEffect(() => {
    if (!selectedPath || loading) {
      return;
    }
    void loadFileView(selectedPath);
  }, [loadFileView, loading, selectedPath]);

  const treeData = useMemo<DataNode[]>(() => {
    if (!treeRoot) {
      return [];
    }
    return buildAntTreeData(treeRoot, diffStatusMap, (item, status) => (
      <span className="memory-skill-tree-node-title">
        <EllipsisText text={item.name} className="memory-skill-tree-node-name" />
        {status && status !== "unchanged" ? (
          <Tag bordered={false} color={getDiffStatusColor(status)} className="memory-skill-tree-status">
            {t(`admin.memorySkillDiffStatus_${status}`, { defaultValue: status })}
          </Tag>
        ) : null}
      </span>
    ));
  }, [diffStatusMap, t, treeRoot]);

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
      const nextVersion = await writeSkillDraftText(skillId, {
        path: selectedPath,
        content: fileContent,
        expectedDraftVersion: status.draftVersion,
      });
      setDraftStatus((previous) =>
        previous ? { ...previous, draftVersion: nextVersion, hasUncommittedDraft: true } : previous,
      );
      setOriginalContent(fileContent);
      setFileBinary(false);
      contentCacheRef.current.set(selectedPath, {
        content: fileContent,
        binary: false,
      });
      setIsEditing(false);

      const treeDiff = await compareSkillTreeDiff(skillId);
      setDiffFiles(treeDiff.files);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Save skill file failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("common.saveFailed")) || t("common.saveFailed"),
      );
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
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillDraftCommitFailed")) ||
          t("admin.memorySkillDraftCommitFailed"),
      );
    } finally {
      setCommitting(false);
    }
  };

  const refreshCurrentFileDiff = useCallback(async () => {
    if (!selectedPath) {
      return;
    }
    const treeDiff = await compareSkillTreeDiff(skillId);
    setDiffFiles(treeDiff.files);
    await loadFileView(selectedPath);
  }, [loadFileView, selectedPath, skillId]);

  const handleHunkDecision = async (
    hunkId: string,
    decision: SkillDraftReviewDecision,
  ) => {
    if (!reviewMode || !selectedPath || !reviewMeta?.reviewId) {
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
              pendingCount: result.pendingCount ?? previous.pendingCount,
              acceptedCount: result.acceptedCount ?? previous.acceptedCount,
              rejectedCount: result.rejectedCount ?? previous.rejectedCount,
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
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillHunkActionFailed")) ||
          t("admin.memorySkillHunkActionFailed"),
      );
    } finally {
      setHunkSubmitting((previous) => {
        const next = { ...previous };
        delete next[hunkId];
        return next;
      });
    }
  };

  const handleUndoReview = async () => {
    if (!reviewMode || !reviewMeta?.reviewId || undoing) {
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
              pendingCount: result.pendingCount ?? previous.pendingCount,
              acceptedCount: result.acceptedCount ?? previous.acceptedCount,
              rejectedCount: result.rejectedCount ?? previous.rejectedCount,
            }
          : previous,
      );
      message.success(t("admin.memorySkillDraftReviewUndoSuccess"));
      setFileHunkSummaries({});
      setReviewedPaths(new Set());
      await refreshPackage();
      if (selectedPath) {
        await loadFileView(selectedPath);
      }
    } catch (error) {
      console.error("Undo skill draft review failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillDraftReviewUndoFailed")) ||
          t("admin.memorySkillDraftReviewUndoFailed"),
      );
    } finally {
      setUndoing(false);
    }
  };

  const handleConfirmReview = async () => {
    if (!canEdit || !reviewMode || committing) {
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
      setReviewedPaths(new Set());
      setReviewMeta(null);
      setFileHunkSummaries({});
      await refreshPackage();
      await onSkillUpdated?.();
    } catch (error) {
      console.error("Confirm skill draft failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillDraftConfirmFailed")) ||
          t("admin.memorySkillDraftConfirmFailed"),
      );
    } finally {
      setCommitting(false);
    }
  };

  const handleDiscardDraft = async () => {
    Modal.confirm({
      title: t("admin.memorySkillDraftDiscardConfirmTitle"),
      content: t("admin.memorySkillDraftDiscardConfirmContent"),
      okText: t("admin.memorySkillDraftDiscardConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await discardSkillDraft(skillId);
          message.success(t("admin.memorySkillDraftDiscardSuccess"));
          setReviewMode(false);
          setReviewedPaths(new Set());
          setReviewMeta(null);
          setFileHunkSummaries({});
          await refreshPackage();
          await onSkillUpdated?.();
        } catch (error) {
          console.error("Discard skill draft failed:", error);
          message.error(
            getLocalizedErrorMessage(error, t("admin.memorySkillDraftDiscardFailed")) ||
              t("admin.memorySkillDraftDiscardFailed"),
          );
        }
      },
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
      setSelectedPath(trimmedPath);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Create skill path failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("common.saveFailed")) || t("common.saveFailed"),
      );
    }
  };

  const renderCreatePathForm = (isDirectory: boolean) => (
    <Space.Compact block className="memory-skill-package-create-form">
      <Select
        value={newParentPath}
        options={directoryOptions}
        popupMatchSelectWidth
        popupClassName="memory-skill-package-path-dropdown"
        labelRender={({ value }) => (
          <EllipsisText
            text={renderPathLabel(String(value ?? ""))}
            className="memory-skill-package-path-option"
          />
        )}
        optionRender={(option) => (
          <EllipsisText
            text={renderPathLabel(String(option.value ?? ""))}
            className="memory-skill-package-path-option"
          />
        )}
        onChange={setNewParentPath}
      />
      <Input
        value={newItemName}
        placeholder={
          isDirectory
            ? t("admin.memorySkillPackageNewFolderNamePlaceholder")
            : t("admin.memorySkillPackageNewFileNamePlaceholder")
        }
        onChange={(event) => setNewItemName(event.target.value)}
        onPressEnter={() => void handleCreatePath(isDirectory)}
      />
    </Space.Compact>
  );

  const handleDeleteFile = () => {
    if (!selectedPath || !draftStatus || reviewMode) {
      return;
    }
    Modal.confirm({
      title: t("admin.memorySkillPackageDeleteConfirmTitle"),
      content: t("admin.memorySkillPackageDeleteConfirmContent", { path: selectedPath }),
      okText: t("common.delete"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          const nextVersion = await deleteSkillDraftPath(skillId, {
            path: selectedPath,
            expectedDraftVersion: draftStatus.draftVersion,
            recursive: false,
          });
          setDraftStatus((previous) =>
            previous
              ? { ...previous, draftVersion: nextVersion, hasUncommittedDraft: true }
              : previous,
          );
          await refreshPackage();
          message.success(t("admin.memorySkillPackageDeleteSuccess"));
        } catch (error) {
          console.error("Delete skill file failed:", error);
          message.error(
            getLocalizedErrorMessage(error, t("admin.memorySkillPackageDeleteFailed")) ||
              t("admin.memorySkillPackageDeleteFailed"),
          );
        }
      },
    });
  };

  const handleUploadFile = async (file: File) => {
    if (!selectedPath || !draftStatus || reviewMode) {
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
      message.error(
        getLocalizedErrorMessage(error, t("common.saveFailed")) || t("common.saveFailed"),
      );
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
        reviewMode={reviewMode && Boolean(reviewMeta?.reviewId)}
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
          {canEdit && reviewMode && usesHunkReview && reviewMeta ? (
            <span className="memory-skill-review-stats">
              {t("admin.memorySkillReviewDecisionStats", {
                accepted: reviewMeta.acceptedCount ?? 0,
                rejected: reviewMeta.rejectedCount ?? 0,
                pending: reviewMeta.pendingCount ?? 0,
              })}
            </span>
          ) : null}
          {canEdit && reviewMode ? (
            <>
              {canUndoReview ? (
                <Button
                  icon={<RollbackOutlined />}
                  loading={undoing}
                  onClick={() => void handleUndoReview()}
                >
                  {t("admin.memorySkillDraftReviewUndo")}
                </Button>
              ) : null}
              <Button
                type="primary"
                loading={committing}
                disabled={!allReviewed}
                onClick={() => void handleConfirmReview()}
              >
                {t("admin.memorySkillDraftConfirm")}
              </Button>
              <Button danger onClick={() => void handleDiscardDraft()}>
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
              <Button danger onClick={() => void handleDiscardDraft()}>
                {t("admin.memorySkillDraftDiscard")}
              </Button>
            </>
          ) : null}
        </Space>
        {canManageSelectedFile ? (
          <Space wrap className="memory-skill-package-file-actions">
            <Upload
              showUploadList={false}
              beforeUpload={(file) => void handleUploadFile(file as File)}
            >
              <Button icon={<UploadOutlined />}>{t("admin.memorySkillPackageUploadFile")}</Button>
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
        <aside className="memory-skill-package-tree">
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
