import { useEffect, useState } from "react";
import type { ChangeEvent } from "react";
import { Alert, Button, Input, Modal, Select, Space, message } from "antd";
import { FolderAddOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  archiveConversation,
  createArchiveFolder,
  listArchiveFolders,
} from "@/modules/settings/recoveryApi";
import type { ConversationArchiveFolder } from "@/api/generated/core-client";

interface ArchiveConversationModalProps {
  conversationId?: string;
  title?: string;
  open: boolean;
  onCancel: () => void;
  onArchived: () => void;
}

export default function ArchiveConversationModal({
  conversationId,
  title,
  open,
  onCancel,
  onArchived,
}: ArchiveConversationModalProps) {
  const { t } = useTranslation();
  const [folders, setFolders] = useState<ConversationArchiveFolder[]>([]);
  const [folderId, setFolderId] = useState("unfiled");
  const [folderName, setFolderName] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [loading, setLoading] = useState(false);
  const [folderLoading, setFolderLoading] = useState(false);
  const [folderError, setFolderError] = useState(false);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setFolderId("unfiled");
    setFolderName("");
    setShowCreate(false);
    setFolderError(false);
    setFolderLoading(true);
    void listArchiveFolders(controller.signal)
      .then((result) => setFolders(result.folders))
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

  const createFolder = async () => {
    const name = folderName.trim();
    if (!name) {
      message.warning(t("settingsPage.recovery.folderRequired"));
      return;
    }
    if (Array.from(name).length > 30) {
      message.warning(t("settingsPage.recovery.folderTooLong"));
      return;
    }
    setFolderLoading(true);
    try {
      const folder = await createArchiveFolder(name);
      setFolders((current) => [...current, folder]);
      setFolderId(folder.id);
      setFolderName("");
      setShowCreate(false);
      message.success(t("settingsPage.recovery.folderCreated"));
    } catch {
      message.error(t("settingsPage.recovery.folderCreateFailed"));
    } finally {
      setFolderLoading(false);
    }
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
    <Modal
      title={t("settingsPage.recovery.archiveNamed", { name: title || "" })}
      open={open}
      okText={t("settingsPage.recovery.archiveAction")}
      cancelText={t("common.cancel")}
      confirmLoading={loading}
      okButtonProps={{ disabled: folderLoading || folderError }}
      onOk={() => void submit()}
      onCancel={onCancel}
      destroyOnHidden
    >
      <Space direction="vertical" size={12} style={{ width: "100%" }}>
        {folderError ? (
          <Alert
            type="error"
            showIcon
            message={t("settingsPage.recovery.folderLoadFailed")}
            action={<Button size="small" onClick={() => setRevision((value) => value + 1)}>{t("common.retry")}</Button>}
          />
        ) : null}
        <Select
          value={folderId}
          loading={folderLoading}
          aria-label={t("settingsPage.recovery.targetFolder")}
          options={[
            { value: "unfiled", label: t("settingsPage.recovery.unfiled") },
            ...folders.map((folder) => ({ value: folder.id, label: folder.name })),
          ]}
          onChange={setFolderId}
          style={{ width: "100%" }}
        />
        {showCreate ? (
          <Space.Compact block>
            <Input
              autoFocus
              maxLength={30}
              showCount
              value={folderName}
              aria-label={t("settingsPage.recovery.folderName")}
              placeholder={t("settingsPage.recovery.folderPlaceholder")}
              onChange={(event: ChangeEvent<HTMLInputElement>) => setFolderName(event.target.value)}
              onPressEnter={() => void createFolder()}
            />
            <Button loading={folderLoading} onClick={() => void createFolder()}>{t("settingsPage.recovery.create")}</Button>
          </Space.Compact>
        ) : (
          <Button icon={<FolderAddOutlined />} onClick={() => setShowCreate(true)}>
            {t("settingsPage.recovery.createInline")}
          </Button>
        )}
      </Space>
    </Modal>
  );
}
