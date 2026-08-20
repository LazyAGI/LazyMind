import { useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent, RefObject, ReactNode } from "react";
import {
  Alert,
  Button,
  Dropdown,
  Empty,
  Input,
  Modal,
  Pagination,
  Select,
  Skeleton,
  Space,
  Tooltip,
  message,
} from "antd";
import type { MenuProps } from "antd";
import {
  DeleteOutlined,
  EditOutlined,
  FolderAddOutlined,
  FolderOutlined,
  InboxOutlined,
  MoreOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import type {
  ConversationArchiveFolder,
  ConversationRecoveryItem,
  WorkflowTrashItem,
} from "@/api/generated/core-client";
import { useTranslation } from "react-i18next";
import {
  archiveConversation,
  createArchiveFolder,
  deleteArchiveFolder,
  emptyConversationTrash,
  emptySkillTrash,
  emptyWorkflowTrash,
  listArchiveFolders,
  listArchivedConversations,
  listSkillTrash,
  listTrashedConversations,
  listWorkflowTrash,
  purgeConversation,
  purgeSkillAsset,
  purgeWorkflow,
  restoreConversation,
  restoreSkillAsset,
  restoreWorkflow,
  trashConversation,
  unarchiveConversation,
  updateArchiveFolder,
  type RecoveryFolderFilter,
  type RecoveryKind,
  type RecoverySkillItem,
} from "./recoveryApi";

type RecoveryView = "archive" | "trash";
type TrashAsset = "skills" | "conversations" | "workflows";

interface RecoverySettingsProps {
  headingRef: RefObject<HTMLHeadingElement>;
}

function useDebouncedValue<T>(value: T, delay = 300) {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timer);
  }, [delay, value]);
  return debounced;
}

function rowMenu(items: MenuProps["items"], busy: boolean, ariaLabel: string) {
  return <Dropdown menu={{ items }} disabled={busy} trigger={["click"]}>
    <Button icon={<MoreOutlined />} aria-label={ariaLabel} />
  </Dropdown>;
}

function ActionSet({ children, menuItems, busy, moreLabel }: { children: ReactNode; menuItems: MenuProps["items"]; busy: boolean; moreLabel: string }) {
  return <div className="recovery-actions">
    <Space className="recovery-actions-desktop" size={8}>{children}</Space>
    <span className="recovery-actions-mobile">{rowMenu(menuItems, busy, moreLabel)}</span>
  </div>;
}

export default function RecoverySettings({ headingRef }: RecoverySettingsProps) {
  const { t, i18n } = useTranslation();
  const [view, setView] = useState<RecoveryView>("archive");
  const [archiveKind, setArchiveKind] = useState<RecoveryKind>("dialog");
  const [archiveKeyword, setArchiveKeyword] = useState("");
  const debouncedArchiveKeyword = useDebouncedValue(archiveKeyword);
  const [folderFilter, setFolderFilter] = useState<RecoveryFolderFilter>("all");
  const [archivePage, setArchivePage] = useState(1);
  const [archiveItems, setArchiveItems] = useState<ConversationRecoveryItem[]>([]);
  const [archiveTotal, setArchiveTotal] = useState(0);
  const [archiveLoading, setArchiveLoading] = useState(true);
  const [archiveError, setArchiveError] = useState(false);
  const [archiveRevision, setArchiveRevision] = useState(0);
  const [folders, setFolders] = useState<ConversationArchiveFolder[]>([]);
  const [foldersLoading, setFoldersLoading] = useState(true);
  const [folderError, setFolderError] = useState(false);
  const [folderRevision, setFolderRevision] = useState(0);
  const [busyKey, setBusyKey] = useState("");

  const [folderModalOpen, setFolderModalOpen] = useState(false);
  const [folderName, setFolderName] = useState("");
  const [folderSaving, setFolderSaving] = useState(false);
  const [folderManagerOpen, setFolderManagerOpen] = useState(false);
  const [editingFolderId, setEditingFolderId] = useState("");
  const [editingFolderName, setEditingFolderName] = useState("");
  const [folderUpdatingId, setFolderUpdatingId] = useState("");
  const [deleteFolder, setDeleteFolder] = useState<ConversationArchiveFolder | null>(null);
  const [deleteMoveTarget, setDeleteMoveTarget] = useState("unfiled");
  const [folderDeleting, setFolderDeleting] = useState(false);
  const [moveItem, setMoveItem] = useState<ConversationRecoveryItem | null>(null);
  const [moveFolderId, setMoveFolderId] = useState<string>("unfiled");

  const [trashAsset, setTrashAsset] = useState<TrashAsset>("skills");
  const [trashKind, setTrashKind] = useState<RecoveryKind>("dialog");
  const [trashKeywords, setTrashKeywords] = useState<Record<TrashAsset, string>>({ skills: "", conversations: "", workflows: "" });
  const debouncedTrashKeywords = useDebouncedValue(trashKeywords);
  const debouncedTrashKeyword = debouncedTrashKeywords[trashAsset];
  const [skillCategory, setSkillCategory] = useState("");
  const [trashPages, setTrashPages] = useState<Record<TrashAsset, number>>({ skills: 1, conversations: 1, workflows: 1 });
  const [trashPageSizes, setTrashPageSizes] = useState<Record<TrashAsset, number>>({ skills: 12, conversations: 20, workflows: 12 });
  const [skillItems, setSkillItems] = useState<RecoverySkillItem[]>([]);
  const [skillCategories, setSkillCategories] = useState<string[]>([]);
  const [conversationItems, setConversationItems] = useState<ConversationRecoveryItem[]>([]);
  const [workflowItems, setWorkflowItems] = useState<WorkflowTrashItem[]>([]);
  const [trashTotals, setTrashTotals] = useState<Record<TrashAsset, number>>({ skills: 0, conversations: 0, workflows: 0 });
  const trashTotal = trashTotals[trashAsset];
  const [trashClearCount, setTrashClearCount] = useState(0);
  const [trashClearCountLoading, setTrashClearCountLoading] = useState(true);
  const [trashLoading, setTrashLoading] = useState(true);
  const [trashError, setTrashError] = useState(false);
  const [trashRevision, setTrashRevision] = useState(0);
  const latestTrashRequest = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    setFoldersLoading(true);
    setFolderError(false);
    void listArchiveFolders(controller.signal).then((result) => {
      setFolders(result.folders);
    }).catch((error) => {
      if (error?.name !== "CanceledError" && error?.name !== "AbortError") setFolderError(true);
    }).finally(() => {
      if (!controller.signal.aborted) setFoldersLoading(false);
    });
    return () => controller.abort();
  }, [folderRevision]);

  useEffect(() => {
    const controller = new AbortController();
    setArchiveLoading(true);
    setArchiveError(false);
    void listArchivedConversations({
      kind: archiveKind,
      keyword: debouncedArchiveKeyword.trim(),
      folderId: folderFilter,
      page: archivePage,
      pageSize: 20,
      signal: controller.signal,
    }).then((result) => {
      if (archivePage > 1 && result.items.length === 0 && result.total > 0) {
        setArchivePage(Math.max(1, Math.ceil(result.total / 20)));
        return;
      }
      setArchiveItems(result.items);
      setArchiveTotal(result.total);
    }).catch((error) => {
      if (error?.name !== "CanceledError" && error?.name !== "AbortError") setArchiveError(true);
    }).finally(() => {
      if (!controller.signal.aborted) setArchiveLoading(false);
    });
    return () => controller.abort();
  }, [archiveKind, archivePage, archiveRevision, debouncedArchiveKeyword, folderFilter]);

  useEffect(() => {
    const requestID = ++latestTrashRequest.current;
    const controller = new AbortController();
    setTrashLoading(true);
    setTrashError(false);
    const page = trashPages[trashAsset];
    const pageSize = trashPageSizes[trashAsset];
    const request = trashAsset === "skills"
      ? listSkillTrash({ keyword: debouncedTrashKeyword.trim(), category: skillCategory, page, pageSize })
      : trashAsset === "conversations"
        ? listTrashedConversations({ kind: trashKind, keyword: debouncedTrashKeyword.trim(), page, pageSize, signal: controller.signal })
        : listWorkflowTrash({ keyword: debouncedTrashKeyword.trim(), page, pageSize, signal: controller.signal });
    void request.then((result) => {
      if (requestID !== latestTrashRequest.current) return;
      if (page > 1 && result.items.length === 0 && result.total > 0) {
        setTrashPages((current) => ({ ...current, [trashAsset]: Math.max(1, Math.ceil(result.total / pageSize)) }));
        return;
      }
      if (trashAsset === "skills") {
        setSkillItems(result.items as RecoverySkillItem[]);
        setSkillCategories("categories" in result ? result.categories : []);
      } else if (trashAsset === "conversations") setConversationItems(result.items as ConversationRecoveryItem[]);
      else setWorkflowItems(result.items as WorkflowTrashItem[]);
      setTrashTotals((current) => ({ ...current, [trashAsset]: result.total }));
    }).catch((error) => {
      if (requestID === latestTrashRequest.current && error?.name !== "CanceledError" && error?.name !== "AbortError") setTrashError(true);
    }).finally(() => {
      if (requestID === latestTrashRequest.current) setTrashLoading(false);
    });
    return () => controller.abort();
  }, [debouncedTrashKeyword, skillCategory, trashAsset, trashKind, trashPageSizes, trashPages, trashRevision]);

  useEffect(() => {
    const controller = new AbortController();
    let current = true;
    setTrashClearCount(0);
    setTrashClearCountLoading(true);
    const request = trashAsset === "skills"
      ? listSkillTrash({ page: 1, pageSize: 1 })
      : trashAsset === "conversations"
        ? listTrashedConversations({ kind: trashKind, page: 1, pageSize: 1, signal: controller.signal })
        : listWorkflowTrash({ page: 1, pageSize: 1, signal: controller.signal });
    void request.then((result) => {
      if (current) setTrashClearCount(result.total);
    }).catch((error) => {
      if (current && error?.name !== "CanceledError" && error?.name !== "AbortError") setTrashClearCount(0);
    }).finally(() => {
      if (current) setTrashClearCountLoading(false);
    });
    return () => {
      current = false;
      controller.abort();
    };
  }, [trashAsset, trashKind, trashRevision]);

  const locale = i18n.language === "zh-CN" ? "zh-CN" : "en-US";
  const formatTime = (value?: string) => value ? new Date(value).toLocaleString(locale, { hour12: false }) : "—";
  const retentionText = (expiresAt?: string) => {
    if (!expiresAt) return t("settingsPage.recovery.retentionUnknown");
    const days = Math.max(0, Math.ceil((new Date(expiresAt).getTime() - Date.now()) / 86_400_000));
    return days === 0
      ? t("settingsPage.recovery.expiresToday")
      : t("settingsPage.recovery.daysRemaining", { count: days });
  };

  const folderOptions = useMemo(() => [
    { value: "unfiled", label: t("settingsPage.recovery.unfiled") },
    ...folders.map((folder) => ({ value: folder.id, label: folder.name })),
  ], [folders, t]);
  const folderFilterOptions = useMemo(() => [
    { value: "all", label: t("settingsPage.recovery.allFolders") },
    ...folderOptions,
  ], [folderOptions, t]);
  const deleteTargetOptions = useMemo(
    () => folderOptions.filter((option) => option.value !== deleteFolder?.id),
    [deleteFolder?.id, folderOptions],
  );

  const reloadArchive = () => {
    setArchiveRevision((value) => value + 1);
    setFolderRevision((value) => value + 1);
  };
  const reloadTrash = () => setTrashRevision((value) => value + 1);

  const saveFolder = async () => {
    const name = folderName.trim();
    if (!name) {
      message.warning(t("settingsPage.recovery.folderRequired"));
      return;
    }
    if (Array.from(name).length > 30) {
      message.warning(t("settingsPage.recovery.folderTooLong"));
      return;
    }
    setFolderSaving(true);
    try {
      const folder = await createArchiveFolder(name);
      setFolderModalOpen(false);
      setFolderName("");
      setFolderRevision((value) => value + 1);
      if (moveItem) setMoveFolderId(folder.id);
      message.success(t("settingsPage.recovery.folderCreated"));
    } catch {
      message.error(t("settingsPage.recovery.folderCreateFailed"));
    } finally {
      setFolderSaving(false);
    }
  };

  const startEditingFolder = (folder: ConversationArchiveFolder) => {
    setEditingFolderId(folder.id);
    setEditingFolderName(folder.name);
  };

  const saveFolderRename = async (folder: ConversationArchiveFolder) => {
    const name = editingFolderName.trim();
    if (!name) {
      message.warning(t("settingsPage.recovery.folderRequired"));
      return;
    }
    if (Array.from(name).length > 30) {
      message.warning(t("settingsPage.recovery.folderTooLong"));
      return;
    }
    if (name === folder.name) {
      setEditingFolderId("");
      return;
    }
    setFolderUpdatingId(folder.id);
    try {
      await updateArchiveFolder(folder.id, name);
      setFolders((current) => current.map((item) => item.id === folder.id ? { ...item, name } : item));
      setEditingFolderId("");
      setFolderRevision((value) => value + 1);
      message.success(t("settingsPage.recovery.folderRenamed"));
    } catch {
      message.error(t("settingsPage.recovery.folderRenameFailed"));
    } finally {
      setFolderUpdatingId("");
    }
  };

  const startDeletingFolder = (folder: ConversationArchiveFolder) => {
    setEditingFolderId("");
    setDeleteFolder(folder);
    setDeleteMoveTarget("unfiled");
  };

  const deleteSelectedFolder = async () => {
    if (!deleteFolder) return;
    const folderId = deleteFolder.id;
    setFolderDeleting(true);
    try {
      await deleteArchiveFolder(folderId, deleteFolder.total_count > 0 ? deleteMoveTarget : undefined);
      setFolders((current) => current.filter((folder) => folder.id !== folderId));
      setDeleteFolder(null);
      if (folderFilter === folderId) {
        setFolderFilter("all");
        setArchivePage(1);
      }
      reloadArchive();
      message.success(t("settingsPage.recovery.folderDeleted"));
    } catch {
      message.error(t("settingsPage.recovery.folderDeleteFailed"));
    } finally {
      setFolderDeleting(false);
    }
  };

  const closeFolderManager = () => {
    if (folderDeleting || folderUpdatingId) return;
    if (deleteFolder) {
      setDeleteFolder(null);
      return;
    }
    setEditingFolderId("");
    setFolderManagerOpen(false);
  };

  const moveArchivedItem = async () => {
    if (!moveItem) return;
    const currentFolder = moveItem.folder_id || "unfiled";
    if (currentFolder === moveFolderId) {
      message.info(t("settingsPage.recovery.alreadyInFolder"));
      return;
    }
    const key = `move:${moveItem.conversation_id}`;
    setBusyKey(key);
    try {
      await archiveConversation(moveItem.conversation_id, moveFolderId === "unfiled" ? null : moveFolderId);
      setMoveItem(null);
      reloadArchive();
      message.success(t("settingsPage.recovery.moved"));
    } catch {
      message.error(t("settingsPage.recovery.operationFailed"));
    } finally {
      setBusyKey("");
    }
  };

  const runArchiveAction = async (item: ConversationRecoveryItem, action: "unarchive" | "trash") => {
    const key = `${action}:${item.conversation_id}`;
    setBusyKey(key);
    try {
      if (action === "unarchive") await unarchiveConversation(item.conversation_id);
      else await trashConversation(item.conversation_id);
      reloadArchive();
      if (action === "trash") reloadTrash();
      message.success(t(action === "unarchive" ? "settingsPage.recovery.unarchived" : "settingsPage.recovery.movedToTrash"));
    } catch {
      message.error(t("settingsPage.recovery.operationFailed"));
    } finally {
      setBusyKey("");
    }
  };

  const confirmArchiveTrash = (item: ConversationRecoveryItem) => Modal.confirm({
    title: t("settingsPage.recovery.moveToTrashTitle", { name: item.display_name }),
    content: t("settingsPage.recovery.moveToTrashDescription"),
    okText: t("settingsPage.recovery.moveToTrash"),
    okButtonProps: { danger: true },
    cancelText: t("common.cancel"),
    onOk: () => runArchiveAction(item, "trash"),
  });

  const groupedArchiveItems = useMemo(() => {
    if (folderFilter !== "all") return [{ id: folderFilter, name: "", items: archiveItems }];
    const groups = new Map<string, ConversationRecoveryItem[]>();
    archiveItems.forEach((item) => {
      const key = item.folder_id || "unfiled";
      groups.set(key, [...(groups.get(key) || []), item]);
    });
    return [...groups.entries()].map(([id, items]) => ({
      id,
      name: id === "unfiled" ? t("settingsPage.recovery.unfiled") : folders.find((folder) => folder.id === id)?.name || t("settingsPage.recovery.unknownFolder"),
      items,
    }));
  }, [archiveItems, folderFilter, folders, t]);

  const archivedRow = (item: ConversationRecoveryItem) => {
    const busy = busyKey.endsWith(`:${item.conversation_id}`);
    const actions: MenuProps["items"] = [
      { key: "move", label: t("settingsPage.recovery.moveTo"), onClick: () => { setMoveItem(item); setMoveFolderId(item.folder_id || "unfiled"); } },
      { key: "unarchive", label: t("settingsPage.recovery.unarchive"), onClick: () => void runArchiveAction(item, "unarchive") },
      { key: "trash", danger: true, label: t("settingsPage.recovery.moveToTrash"), onClick: () => confirmArchiveTrash(item) },
    ];
    return <div className="recovery-row" key={item.conversation_id}>
      <div className="recovery-name-cell"><span className="recovery-item-icon"><InboxOutlined /></span><div><strong>{item.display_name}</strong><small>{formatTime(item.archived_at)}</small></div></div>
      <time>{formatTime(item.archived_at)}</time>
      <ActionSet busy={busy} menuItems={actions} moreLabel={t("settingsPage.recovery.moreActions")}>
        <Button disabled={busy} onClick={() => { setMoveItem(item); setMoveFolderId(item.folder_id || "unfiled"); }}>{t("settingsPage.recovery.moveTo")}</Button>
        <Button loading={busyKey === `unarchive:${item.conversation_id}`} disabled={busy && busyKey !== `unarchive:${item.conversation_id}`} onClick={() => void runArchiveAction(item, "unarchive")}>{t("settingsPage.recovery.unarchive")}</Button>
        <Tooltip title={t("settingsPage.recovery.moveToTrash")}><Button aria-label={t("settingsPage.recovery.moveToTrashNamed", { name: item.display_name })} danger icon={<DeleteOutlined />} disabled={busy} onClick={() => confirmArchiveTrash(item)} /></Tooltip>
      </ActionSet>
    </div>;
  };

  const runTrashItemAction = async (asset: TrashAsset, id: string, action: "restore" | "purge") => {
    const key = `${asset}:${action}:${id}`;
    setBusyKey(key);
    try {
      if (asset === "skills") {
        if (action === "restore") await restoreSkillAsset(id); else await purgeSkillAsset(id);
      } else if (asset === "conversations") {
        if (action === "restore") await restoreConversation(id); else await purgeConversation(id);
      } else if (action === "restore") await restoreWorkflow(id); else await purgeWorkflow(id);
      reloadTrash();
      if (asset === "conversations") reloadArchive();
      message.success(t(action === "restore" ? "settingsPage.recovery.restored" : "settingsPage.recovery.purged"));
    } catch (error: any) {
      const conflict = error?.response?.status === 409;
      message.error(t(conflict ? "settingsPage.recovery.workflowConflict" : "settingsPage.recovery.operationFailed"));
    } finally {
      setBusyKey("");
    }
  };

  const confirmTrashAction = (asset: TrashAsset, id: string, name: string, action: "restore" | "purge", restoreDescription?: string) => {
    const run = () => runTrashItemAction(asset, id, action);
    if (action === "restore" && asset !== "conversations") return void run();
    Modal.confirm({
      title: t(action === "restore" ? "settingsPage.recovery.restoreNamed" : "settingsPage.recovery.purgeNamed", { name }),
      content: action === "purge" ? t("settingsPage.recovery.irreversible") : restoreDescription,
      okText: t(action === "restore" ? "settingsPage.recovery.restore" : "settingsPage.recovery.purge"),
      okButtonProps: { danger: action === "purge" },
      cancelText: t("common.cancel"),
      onOk: run,
    });
  };

  const trashRow = (asset: TrashAsset, id: string, name: string, deletedAt?: string, expiresAt?: string, description?: string, restoreDescription?: string) => {
    const busy = busyKey.startsWith(`${asset}:`) && busyKey.endsWith(`:${id}`);
    const actions: MenuProps["items"] = [
      { key: "restore", label: t("settingsPage.recovery.restore"), onClick: () => confirmTrashAction(asset, id, name, "restore", restoreDescription) },
      { key: "purge", danger: true, label: t("settingsPage.recovery.purge"), onClick: () => confirmTrashAction(asset, id, name, "purge") },
    ];
    return <div className="recovery-row" key={id}>
      <div className="recovery-name-cell"><span className="recovery-item-icon"><InboxOutlined /></span><div><strong>{name}</strong>{description ? <p>{description}</p> : null}<small>{formatTime(deletedAt)} · {retentionText(expiresAt)}</small></div></div>
      <time>{formatTime(deletedAt)}<small>{retentionText(expiresAt)}</small></time>
      <ActionSet busy={busy} menuItems={actions} moreLabel={t("settingsPage.recovery.moreActions")}>
        <Button loading={busyKey === `${asset}:restore:${id}`} disabled={busy && busyKey !== `${asset}:restore:${id}`} onClick={() => confirmTrashAction(asset, id, name, "restore", restoreDescription)}>{t("settingsPage.recovery.restore")}</Button>
        <Button danger loading={busyKey === `${asset}:purge:${id}`} disabled={busy && busyKey !== `${asset}:purge:${id}`} onClick={() => confirmTrashAction(asset, id, name, "purge")}>{t("settingsPage.recovery.purge")}</Button>
      </ActionSet>
    </div>;
  };

  const emptyCurrentTrash = () => {
    const assetName = t(`settingsPage.recovery.${trashAsset}`);
    Modal.confirm({
      title: t("settingsPage.recovery.emptyNamed", { name: assetName, count: trashClearCount }),
      content: t("settingsPage.recovery.irreversible"),
      okText: t("settingsPage.recovery.emptyTrash"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        setBusyKey(`empty:${trashAsset}`);
        try {
          const count = trashAsset === "skills"
            ? await emptySkillTrash()
            : trashAsset === "conversations"
              ? await emptyConversationTrash(trashKind)
              : await emptyWorkflowTrash();
          reloadTrash();
          if (trashAsset === "conversations") reloadArchive();
          message.success(t("settingsPage.recovery.emptiedCount", { count }));
        } catch {
          message.error(t("settingsPage.recovery.operationFailed"));
        } finally {
          setBusyKey("");
        }
      },
    });
  };

  const archivePanel = <>
    <div className="recovery-toolbar is-archive">
      <div className="recovery-segment" role="tablist" aria-label={t("settingsPage.recovery.sessionType")}>
        {(["dialog", "task"] as RecoveryKind[]).map((kind) => <button key={kind} type="button" role="tab" aria-selected={archiveKind === kind} className={archiveKind === kind ? "is-active" : ""} onClick={() => { setArchiveKind(kind); setArchivePage(1); }}>{t(`settingsPage.recovery.${kind}`)}</button>)}
      </div>
      <Input allowClear value={archiveKeyword} onChange={(event: ChangeEvent<HTMLInputElement>) => { setArchiveKeyword(event.target.value); setArchivePage(1); }} prefix={<SearchOutlined />} placeholder={t("settingsPage.recovery.searchArchived")} aria-label={t("settingsPage.recovery.searchArchived")} />
      <Select value={folderFilter} options={folderFilterOptions} loading={foldersLoading} onChange={(value: RecoveryFolderFilter) => { setFolderFilter(value); setArchivePage(1); }} aria-label={t("settingsPage.recovery.folderFilter")} />
      <div className="recovery-folder-toolbar-actions">
        <Button icon={<FolderOutlined />} aria-label={t("settingsPage.recovery.manageFolders")} onClick={() => { setDeleteFolder(null); setEditingFolderId(""); setFolderManagerOpen(true); }}>{t("settingsPage.recovery.manageFolders")}</Button>
        <Button icon={<FolderAddOutlined />} aria-label={t("settingsPage.recovery.newFolder")} onClick={() => { setMoveItem(null); setFolderName(""); setFolderModalOpen(true); }}>{t("settingsPage.recovery.newFolder")}</Button>
      </div>
    </div>
    {folderError ? <Alert type="warning" showIcon message={t("settingsPage.recovery.folderLoadFailed")} action={<Button size="small" onClick={() => setFolderRevision((value) => value + 1)}>{t("common.retry")}</Button>} /> : null}
    {archiveLoading ? <div className="recovery-loading"><Skeleton active paragraph={{ rows: 5 }} /></div> : archiveError ? <Alert className="recovery-load-error" type="error" showIcon message={t("settingsPage.recovery.loadFailed")} action={<Button onClick={() => setArchiveRevision((value) => value + 1)}>{t("common.retry")}</Button>} /> : archiveItems.length === 0 ? <Empty description={archiveKeyword.trim() ? t("settingsPage.recovery.noResults") : t("settingsPage.recovery.archiveEmpty")} /> : <div className="recovery-groups">
      {groupedArchiveItems.map((group) => <section className="recovery-group" key={group.id} aria-label={group.name || undefined}>
        {folderFilter === "all" ? <header><span><FolderOutlined /> {group.name}</span><em>{t("settingsPage.recovery.itemCount", { count: group.items.length })}</em></header> : null}
        <div className="recovery-table"><div className="recovery-table-head"><span>{t("settingsPage.recovery.name")}</span><span>{t("settingsPage.recovery.archivedAt")}</span><span>{t("settingsPage.recovery.actions")}</span></div>{group.items.map(archivedRow)}</div>
      </section>)}
    </div>}
    {archiveTotal > 20 ? <Pagination current={archivePage} pageSize={20} total={archiveTotal} showSizeChanger={false} onChange={setArchivePage} /> : null}
  </>;

  const trashPanel = <>
    <div className="recovery-trash-tabs" role="tablist" aria-label={t("settingsPage.recovery.trashCategory")}>
      {(["skills", "conversations", "workflows"] as TrashAsset[]).map((asset) => <button key={asset} type="button" role="tab" aria-selected={trashAsset === asset} className={trashAsset === asset ? "is-active" : ""} onClick={() => setTrashAsset(asset)}>{t(`settingsPage.recovery.${asset}`)}</button>)}
    </div>
    <div className={`recovery-toolbar is-trash-${trashAsset}`}>
      {trashAsset === "conversations" ? <div className="recovery-segment" role="tablist" aria-label={t("settingsPage.recovery.sessionType")}>{(["dialog", "task"] as RecoveryKind[]).map((kind) => <button key={kind} type="button" role="tab" aria-selected={trashKind === kind} className={trashKind === kind ? "is-active" : ""} onClick={() => { setTrashKind(kind); setTrashPages((current) => ({ ...current, conversations: 1 })); }}>{t(`settingsPage.recovery.${kind}`)}</button>)}</div> : null}
      <Input allowClear value={trashKeywords[trashAsset]} onChange={(event: ChangeEvent<HTMLInputElement>) => { setTrashKeywords((current) => ({ ...current, [trashAsset]: event.target.value })); setTrashPages((current) => ({ ...current, [trashAsset]: 1 })); }} prefix={<SearchOutlined />} placeholder={t(`settingsPage.recovery.search.${trashAsset}`)} aria-label={t(`settingsPage.recovery.search.${trashAsset}`)} />
      {trashAsset === "skills" ? <Select allowClear value={skillCategory || undefined} placeholder={t("settingsPage.recovery.allCategories")} options={skillCategories.map((category) => ({ value: category, label: category }))} onChange={(value: string | undefined) => { setSkillCategory(value || ""); setTrashPages((current) => ({ ...current, skills: 1 })); }} /> : null}
      <Button danger disabled={trashClearCountLoading || trashClearCount === 0} loading={trashClearCountLoading || busyKey === `empty:${trashAsset}`} onClick={emptyCurrentTrash}>{t("settingsPage.recovery.emptyTrash")}</Button>
    </div>
    {trashLoading ? <div className="recovery-loading"><Skeleton active paragraph={{ rows: 5 }} /></div> : trashError ? <Alert className="recovery-load-error" type="error" showIcon message={t("settingsPage.recovery.loadFailed")} action={<Button onClick={reloadTrash}>{t("common.retry")}</Button>} /> : trashTotal === 0 ? <Empty description={trashKeywords[trashAsset].trim() || (trashAsset === "skills" && skillCategory) ? t("settingsPage.recovery.noResults") : t("settingsPage.recovery.trashEmpty")} /> : <div className="recovery-table">
      <div className="recovery-table-head"><span>{t("settingsPage.recovery.name")}</span><span>{t("settingsPage.recovery.deletedAt")}</span><span>{t("settingsPage.recovery.actions")}</span></div>
      {trashAsset === "skills" ? skillItems.map((item) => trashRow("skills", item.skillId, item.name, item.deletedAt, item.trashExpiresAt, item.description)) : null}
      {trashAsset === "conversations" ? conversationItems.map((item) => trashRow("conversations", item.conversation_id, item.display_name, item.deleted_at, item.trash_expires_at, undefined, t(item.kind === "task" ? "settingsPage.recovery.restoreTaskDescription" : "settingsPage.recovery.restoreConversationDescription"))) : null}
      {trashAsset === "workflows" ? workflowItems.map((item) => trashRow("workflows", item.id, item.name, item.deleted_at, item.trash_expires_at, item.workflow_id)) : null}
    </div>}
    {trashTotal > trashPageSizes[trashAsset] ? <Pagination current={trashPages[trashAsset]} pageSize={trashPageSizes[trashAsset]} total={trashTotal} showSizeChanger={trashAsset !== "conversations"} pageSizeOptions={[12, 24]} onChange={(page: number, pageSize: number) => { setTrashPages((current) => ({ ...current, [trashAsset]: page })); setTrashPageSizes((current) => ({ ...current, [trashAsset]: pageSize })); }} /> : null}
  </>;

  return <>
    <header className="settings-detail-header recovery-heading"><div><h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.recovery.title")}</h1><p>{t("settingsPage.recovery.description")}</p></div></header>
    <div className="recovery-view-tabs" role="tablist" aria-label={t("settingsPage.recovery.viewTabs")}>
      <button type="button" role="tab" aria-selected={view === "trash"} className={view === "trash" ? "is-active" : ""} onClick={() => setView("trash")}><DeleteOutlined /><span><strong>{t("settingsPage.recovery.trash")}</strong><small>{t("settingsPage.recovery.trashSubtitle")}</small></span></button>
      <button type="button" role="tab" aria-selected={view === "archive"} className={view === "archive" ? "is-active" : ""} onClick={() => setView("archive")}><InboxOutlined /><span><strong>{t("settingsPage.recovery.archive")}</strong><small>{t("settingsPage.recovery.archiveSubtitle")}</small></span></button>
    </div>
    <section className="recovery-surface" aria-label={t(view === "archive" ? "settingsPage.recovery.archive" : "settingsPage.recovery.trash")}>{view === "archive" ? archivePanel : trashPanel}</section>
    <Modal title={t("settingsPage.recovery.newFolder")} open={folderModalOpen} confirmLoading={folderSaving} okText={t("settingsPage.recovery.create")} cancelText={t("common.cancel")} onOk={() => void saveFolder()} onCancel={() => setFolderModalOpen(false)} destroyOnHidden><Input autoFocus maxLength={30} showCount value={folderName} onChange={(event: ChangeEvent<HTMLInputElement>) => setFolderName(event.target.value)} onPressEnter={() => void saveFolder()} placeholder={t("settingsPage.recovery.folderPlaceholder")} aria-label={t("settingsPage.recovery.folderName")} /></Modal>
    <Modal
      title={deleteFolder ? t("settingsPage.recovery.deleteFolderTitle", { name: deleteFolder.name }) : t("settingsPage.recovery.manageFolders")}
      open={folderManagerOpen}
      footer={deleteFolder ? undefined : <Button onClick={closeFolderManager}>{t("common.close")}</Button>}
      okText={t("settingsPage.recovery.deleteFolder")}
      okButtonProps={{ danger: true }}
      cancelText={t("common.cancel")}
      confirmLoading={folderDeleting}
      closable={!folderDeleting && !Boolean(folderUpdatingId)}
      maskClosable={!folderDeleting && !Boolean(folderUpdatingId)}
      onOk={() => void deleteSelectedFolder()}
      onCancel={closeFolderManager}
      destroyOnHidden
    >
      {deleteFolder ? <div className="recovery-folder-delete">
        <p>{t(deleteFolder.total_count > 0 ? "settingsPage.recovery.deleteNonEmptyFolderDescription" : "settingsPage.recovery.deleteEmptyFolderDescription", { count: deleteFolder.total_count })}</p>
        {deleteFolder.total_count > 0 ? <div className="recovery-folder-delete-target">
          <label htmlFor="recovery-folder-delete-target">{t("settingsPage.recovery.deleteMoveTarget")}</label>
          <Select id="recovery-folder-delete-target" value={deleteMoveTarget} options={deleteTargetOptions} onChange={setDeleteMoveTarget} aria-label={t("settingsPage.recovery.deleteMoveTarget")} />
        </div> : null}
      </div> : foldersLoading ? <Skeleton active paragraph={{ rows: 3 }} /> : folderError ? <Alert type="warning" showIcon message={t("settingsPage.recovery.folderLoadFailed")} action={<Button size="small" onClick={() => setFolderRevision((value) => value + 1)}>{t("common.retry")}</Button>} /> : folders.length === 0 ? <Empty description={t("settingsPage.recovery.folderManagerEmpty")} /> : <div className="recovery-folder-manager" role="list">
        {folders.map((folder) => <div className="recovery-folder-manager-row" role="listitem" key={folder.id}>
          {editingFolderId === folder.id ? <>
            <Input autoFocus maxLength={30} showCount value={editingFolderName} onChange={(event: ChangeEvent<HTMLInputElement>) => setEditingFolderName(event.target.value)} onPressEnter={() => void saveFolderRename(folder)} aria-label={t("settingsPage.recovery.folderName")} />
            <Space size={8}>
              <Button type="primary" loading={folderUpdatingId === folder.id} onClick={() => void saveFolderRename(folder)}>{t("common.save")}</Button>
              <Button disabled={Boolean(folderUpdatingId)} onClick={() => setEditingFolderId("")}>{t("common.cancel")}</Button>
            </Space>
          </> : <>
            <div><strong>{folder.name}</strong><small>{t("settingsPage.recovery.itemCount", { count: folder.total_count })}</small></div>
            <Space size={8}>
              <Tooltip title={t("settingsPage.recovery.editFolderNamed", { name: folder.name })}><Button icon={<EditOutlined />} aria-label={t("settingsPage.recovery.editFolderNamed", { name: folder.name })} disabled={Boolean(folderUpdatingId)} onClick={() => startEditingFolder(folder)} /></Tooltip>
              <Tooltip title={t("settingsPage.recovery.deleteFolderNamed", { name: folder.name })}><Button danger icon={<DeleteOutlined />} aria-label={t("settingsPage.recovery.deleteFolderNamed", { name: folder.name })} disabled={Boolean(folderUpdatingId)} onClick={() => startDeletingFolder(folder)} /></Tooltip>
            </Space>
          </>}
        </div>)}
      </div>}
    </Modal>
    <Modal title={t("settingsPage.recovery.moveNamed", { name: moveItem?.display_name || "" })} open={Boolean(moveItem)} okText={t("settingsPage.recovery.move")} cancelText={t("common.cancel")} confirmLoading={busyKey.startsWith("move:")} onOk={() => void moveArchivedItem()} onCancel={() => setMoveItem(null)} destroyOnHidden>
      <div className="recovery-move-form"><Select value={moveFolderId} options={folderOptions} onChange={setMoveFolderId} aria-label={t("settingsPage.recovery.targetFolder")} /><Button icon={<FolderAddOutlined />} onClick={() => { setFolderName(""); setFolderModalOpen(true); }}>{t("settingsPage.recovery.createInline")}</Button></div>
    </Modal>
    <span className="settings-screenreader-status" role="status" aria-live="polite">{busyKey ? t("settingsPage.recovery.processing") : ""}</span>
  </>;
}
