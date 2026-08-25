import { useEffect, useState } from "react";
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
  title?: string;
  itemKind?: "dialog" | "task";
  open: boolean;
  onCancel: () => void;
  onArchived: () => void;
}

export default function ArchiveConversationModal({
  conversationId,
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
    if (!conversationId) return;
    setLoading(true);
    try {
      await archiveConversation(conversationId, folderId === "unfiled" ? null : folderId);
      onArchived();
    } catch {
      message.error(t("settingsPage.recovery.operationFailed"));
    } finally {
      setLoading(false);
    }
  };

  return (
    <ArchiveFolderPickerModal
      open={open}
      mode="archive"
      itemName={title || ""}
      itemKind={itemKind}
      folders={folders}
      unfiledTotalCount={unfiledTotalCount}
      selectedFolderId={folderId}
      foldersLoading={folderLoading}
      folderLoadError={folderError}
      submitting={loading}
      submitDisabled={!conversationId}
      createFolder={createArchiveFolder}
      onFolderCreated={handleFolderCreated}
      onSelectFolder={setFolderId}
      onRetry={() => setRevision((value) => value + 1)}
      onSubmit={() => void submit()}
      onCancel={onCancel}
    />
  );
}
