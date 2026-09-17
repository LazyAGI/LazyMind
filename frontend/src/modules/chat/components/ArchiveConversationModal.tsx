import { getLocalizedErrorMessage } from "@/components/request";
import { useEffect, useRef, useState } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";

import type { ConversationArchiveFolder } from "@/api/generated/core-client";
import ArchiveFolderPickerModal from "@/components/ui/ArchiveFolderPickerModal";
import {
  archiveConversation,
  createArchiveFolder,
  listArchiveFolders,
} from "@/modules/settings/recoveryApi";

interface ArchiveConversationModalProps {
  conversationId?: string;
  conversationIds?: string[];
  title?: string;
  itemKind?: "dialog" | "task";
  open: boolean;
  onCancel: () => void;
  onArchived: (archivedIds: string[], failedIds: string[]) => void;
}

export default function ArchiveConversationModal({
  conversationId,
  conversationIds,
  title,
  itemKind = "dialog",
  open,
  onCancel,
  onArchived,
}: ArchiveConversationModalProps) {
  const { t } = useTranslation();
  const [folders, setFolders] = useState<ConversationArchiveFolder[]>([]);
  const [unfiledTotalCount, setUnfiledTotalCount] = useState(0);
  const [folderId, setFolderId] = useState("unfiled");
  const [loading, setLoading] = useState(false);
  const [folderLoading, setFolderLoading] = useState(false);
  const [folderError, setFolderError] = useState(false);
  const [revision, setRevision] = useState(0);
  const submittingRef = useRef(false);
  const ids = [...new Set(conversationIds ?? (conversationId ? [conversationId] : []))];

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setFolderId("unfiled");
    setFolderError(false);
    setFolderLoading(true);
    void listArchiveFolders(controller.signal)
      .then((result) => {
        setFolders(result.folders);
        setUnfiledTotalCount(result.unfiledTotalCount);
      })
      .catch((error) => {
        if (error?.name !== "CanceledError" && error?.name !== "AbortError") {
          setFolderError(true);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setFolderLoading(false);
      });
    return () => controller.abort();
  }, [open, revision]);

  const handleFolderCreated = (folder: ConversationArchiveFolder) => {
    setFolders((current) => current.some((item) => item.id === folder.id)
      ? current.map((item) => item.id === folder.id ? folder : item)
      : [...current, folder]);
    setFolderId(folder.id);
  };

  const submit = async () => {
    if (!ids.length || submittingRef.current) return;
    submittingRef.current = true;
    setLoading(true);
    const archivedIds: string[] = [];
    const failedIds: string[] = [];
    try {
      // Keep requests bounded and retain failed items for an explicit retry.
      for (const id of ids) {
        try {
          await archiveConversation(id, folderId === "unfiled" ? null : folderId);
          archivedIds.push(id);
        } catch (error) {
          if (ids.length === 1) message.error(getLocalizedErrorMessage(error));
          failedIds.push(id);
        }
      }
      if (failedIds.length && ids.length > 1) {
        message.error(t(archivedIds.length ? "chat.batchArchivePartialFailure" : "settingsPage.recovery.operationFailed", { count: failedIds.length }));
      }
      if (archivedIds.length) onArchived(archivedIds, failedIds);
    } finally {
      submittingRef.current = false;
      setLoading(false);
    }
  };

  return (
    <ArchiveFolderPickerModal
      open={open}
      mode="archive"
      itemName={conversationIds ? t("settingsPage.recovery.conversationCount", { count: ids.length }) : title || ""}
      itemKind={itemKind}
      folders={folders}
      unfiledTotalCount={unfiledTotalCount}
      selectedFolderId={folderId}
      foldersLoading={folderLoading}
      folderLoadError={folderError}
      submitting={loading}
      submitDisabled={!ids.length}
      createFolder={createArchiveFolder}
      onFolderCreated={handleFolderCreated}
      onSelectFolder={setFolderId}
      onRetry={() => setRevision((value) => value + 1)}
      onSubmit={() => void submit()}
      onCancel={onCancel}
    />
  );
}
