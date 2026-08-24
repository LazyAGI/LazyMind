import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { ChangeEvent, KeyboardEvent } from "react";
import { Alert, Button, Input, Modal, Skeleton, message } from "antd";
import {
  FolderOutlined,
  InboxOutlined,
  PlusOutlined,
  UnorderedListOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import type { ConversationArchiveFolder } from "@/api/generated/core-client";

import "./ArchiveFolderPickerModal.scss";

type ArchiveFolderPickerMode = "archive" | "move";
type ArchiveFolderPickerItemKind = "dialog" | "task";

interface ArchiveFolderPickerModalProps {
  open: boolean;
  mode: ArchiveFolderPickerMode;
  itemName: string;
  itemKind?: ArchiveFolderPickerItemKind;
  folders: ConversationArchiveFolder[];
  unfiledTotalCount: number;
  selectedFolderId: string;
  foldersLoading: boolean;
  folderLoadError: boolean;
  submitting: boolean;
  submitDisabled?: boolean;
  createFolder: (name: string) => Promise<ConversationArchiveFolder>;
  onFolderCreated: (folder: ConversationArchiveFolder) => void;
  onSelectFolder: (folderId: string) => void;
  onRetry: () => void;
  onSubmit: () => void;
  onCancel: () => void;
}

export default function ArchiveFolderPickerModal({
  open,
  mode,
  itemName,
  itemKind = "dialog",
  folders,
  unfiledTotalCount,
  selectedFolderId,
  foldersLoading,
  folderLoadError,
  submitting,
  submitDisabled = false,
  createFolder,
  onFolderCreated,
  onSelectFolder,
  onRetry,
  onSubmit,
  onCancel,
}: ArchiveFolderPickerModalProps) {
  const { t } = useTranslation();
  const pickerId = useId();
  const submitButtonRef = useRef<HTMLButtonElement>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [folderName, setFolderName] = useState("");
  const [folderError, setFolderError] = useState("");
  const [folderCreating, setFolderCreating] = useState(false);

  const folderOptions = useMemo(() => [
    {
      id: "unfiled",
      name: t("settingsPage.recovery.unfiled"),
      count: unfiledTotalCount,
      isDefault: true,
    },
    ...folders.map((folder) => ({
      id: folder.id,
      name: folder.name,
      count: folder.total_count,
      isDefault: false,
    })),
  ], [folders, t, unfiledTotalCount]);

  const busy = folderCreating || submitting;
  const title = t(mode === "archive"
    ? "settingsPage.recovery.archiveToFolder"
    : "settingsPage.recovery.moveToFolder");
  const actionLabel = t(mode === "archive"
    ? "settingsPage.recovery.archiveAction"
    : "settingsPage.recovery.move");

  const resetCreate = () => {
    setCreateOpen(false);
    setFolderName("");
    setFolderError("");
  };

  useEffect(() => {
    setCreateOpen(false);
    setFolderName("");
    setFolderError("");
  }, [open]);

  const selectFolder = (folderId: string) => {
    onSelectFolder(folderId);
    resetCreate();
  };

  const submitNewFolder = async () => {
    const name = folderName.trim();
    if (!name) {
      setFolderError(t("settingsPage.recovery.folderRequired"));
      return;
    }
    if (Array.from(name).length > 30) {
      setFolderError(t("settingsPage.recovery.folderTooLong"));
      return;
    }
    const normalizedName = name.toLocaleLowerCase();
    if (folders.some((folder) => folder.name.trim().toLocaleLowerCase() === normalizedName)) {
      setFolderError(t("settingsPage.recovery.folderDuplicate"));
      return;
    }

    setFolderCreating(true);
    setFolderError("");
    try {
      const folder = await createFolder(name);
      onFolderCreated(folder);
      resetCreate();
      message.success(t("settingsPage.recovery.folderCreatedNamed", { name: folder.name }));
      window.setTimeout(() => submitButtonRef.current?.focus(), 0);
    } catch (error) {
      const status = (error as { response?: { status?: number } })?.response?.status;
      setFolderError(t(status === 409
        ? "settingsPage.recovery.folderDuplicate"
        : "settingsPage.recovery.folderCreateFailed"));
    } finally {
      setFolderCreating(false);
    }
  };

  return (
    <Modal
      className="archive-folder-picker-modal"
      rootClassName="archive-folder-picker-modal-root"
      title={title}
      open={open}
      width={620}
      centered
      footer={[
        <Button key="cancel" disabled={busy} onClick={onCancel}>{t("common.cancel")}</Button>,
        <Button
          key="submit"
          ref={submitButtonRef}
          type="primary"
          aria-label={actionLabel}
          loading={submitting}
          disabled={submitDisabled || createOpen || folderCreating || foldersLoading || folderLoadError || !selectedFolderId}
          onClick={onSubmit}
        >
          {actionLabel}
        </Button>,
      ]}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      onCancel={onCancel}
      destroyOnHidden
    >
      <div className="archive-folder-picker-summary">
        <span className="archive-folder-picker-summary-icon" aria-hidden="true">
          {itemKind === "task" ? <UnorderedListOutlined /> : <InboxOutlined />}
        </span>
        <span>{itemName}</span>
      </div>
      <div className="archive-folder-picker">
        <span className="archive-folder-picker-label" id={`${pickerId}-label`}>
          {t("settingsPage.recovery.selectFolder")}
        </span>
        {foldersLoading ? (
          <Skeleton active paragraph={{ rows: 2 }} />
        ) : folderLoadError ? (
          <Alert
            type="warning"
            showIcon
            message={t("settingsPage.recovery.folderLoadFailed")}
            action={<Button size="small" onClick={onRetry}>{t("common.retry")}</Button>}
          />
        ) : (
          <div className="archive-folder-picker-options" role="radiogroup" aria-labelledby={`${pickerId}-label`}>
            {folderOptions.map((folder) => (
              <label
                className={`archive-folder-picker-option${selectedFolderId === folder.id ? " is-selected" : ""}${busy ? " is-disabled" : ""}`}
                key={folder.id}
              >
                <input
                  type="radio"
                  name={`${pickerId}-folder`}
                  value={folder.id}
                  checked={selectedFolderId === folder.id}
                  disabled={busy}
                  onChange={(event: ChangeEvent<HTMLInputElement>) => selectFolder(event.currentTarget.value)}
                />
                <span className="archive-folder-picker-option-icon" aria-hidden="true"><FolderOutlined /></span>
                <span className="archive-folder-picker-option-name">{folder.name}</span>
                <span className="archive-folder-picker-option-count">
                  {folder.isDefault ? <>{t("settingsPage.recovery.defaultFolder")} · </> : null}
                  {t("settingsPage.recovery.conversationCount", { count: folder.count })}
                </span>
              </label>
            ))}
          </div>
        )}
        {createOpen ? (
          <div className="archive-folder-picker-new-field">
            <label htmlFor={`${pickerId}-folder-name`}>{t("settingsPage.recovery.folderName")}</label>
            <Input
              id={`${pickerId}-folder-name`}
              autoFocus
              maxLength={30}
              value={folderName}
              placeholder={t("settingsPage.recovery.folderPlaceholder")}
              disabled={folderCreating}
              aria-invalid={Boolean(folderError)}
              aria-describedby={folderError ? `${pickerId}-folder-error` : `${pickerId}-folder-hint`}
              onChange={(event: ChangeEvent<HTMLInputElement>) => {
                setFolderName(event.target.value);
                if (folderError) setFolderError("");
              }}
              onPressEnter={() => void submitNewFolder()}
              onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                if (event.key === "Escape") {
                  event.stopPropagation();
                  resetCreate();
                }
              }}
            />
            <small id={`${pickerId}-folder-hint`}>{t("settingsPage.recovery.createAutoSelect")}</small>
            {folderError ? (
              <span className="archive-folder-picker-new-error" id={`${pickerId}-folder-error`} role="alert">
                {folderError}
              </span>
            ) : null}
            <div className="archive-folder-picker-new-actions">
              <Button size="small" disabled={folderCreating} onClick={resetCreate}>{t("common.cancel")}</Button>
              <Button
                size="small"
                type="primary"
                aria-label={t("settingsPage.recovery.create")}
                loading={folderCreating}
                onClick={() => void submitNewFolder()}
              >
                {t("settingsPage.recovery.create")}
              </Button>
            </div>
          </div>
        ) : (
          <button
            className="archive-folder-picker-create"
            type="button"
            disabled={foldersLoading || folderLoadError || submitting}
            onClick={() => {
              setCreateOpen(true);
              setFolderName("");
              setFolderError("");
            }}
          >
            <PlusOutlined aria-hidden="true" />
            {t("settingsPage.recovery.createInline")}
          </button>
        )}
      </div>
    </Modal>
  );
}
