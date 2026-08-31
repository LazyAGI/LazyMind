import {
  Configuration as CoreConfiguration,
  DefaultApiFactory,
  type ConversationArchiveFolder,
  type ConversationRecoveryItem,
  type ConversationRecoveryListResponse,
  type WorkflowTrashItem,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import {
  emptySkillTrash,
  listTrashedSkillAssetsPage,
  purgeSkillAsset,
  restoreSkillAsset,
  type SkillAssetRecord,
} from "@/modules/memory/skillApi";

const coreApi = DefaultApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

export type RecoveryKind = "dialog" | "task";
export type RecoveryFolderFilter = "all" | "unfiled" | string;

export interface ArchiveFolderList {
  folders: ConversationArchiveFolder[];
  unfiledDialogCount: number;
  unfiledTaskCount: number;
  unfiledTotalCount: number;
}

export interface RecoveryPage<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}

export interface RecoverySkillItem extends SkillAssetRecord {
  trashExpiresAt?: string;
}

export interface RecoverySkillPage extends RecoveryPage<RecoverySkillItem> {
  categories: string[];
}

export async function listArchiveFolders(signal?: AbortSignal): Promise<ArchiveFolderList> {
  const response = await coreApi.apiCoreConversationArchiveFoldersGet({ signal });
  return {
    folders: response.data.folders || [],
    unfiledDialogCount: response.data.unfiled_dialog_count || 0,
    unfiledTaskCount: response.data.unfiled_task_count || 0,
    unfiledTotalCount: response.data.unfiled_total_count || 0,
  };
}

export async function createArchiveFolder(name: string): Promise<ConversationArchiveFolder> {
  const response = await coreApi.apiCoreConversationArchiveFoldersPost({
    conversationArchiveFolderRequest: { name },
  }, { silentError: true } as never);
  return response.data.folder;
}

export async function updateArchiveFolder(folderId: string, name: string): Promise<void> {
  await coreApi.apiCoreConversationArchiveFoldersFolderIdPatch({
    folderId,
    conversationArchiveFolderRequest: { name },
  });
}

export async function deleteArchiveFolder(folderId: string, moveToFolderId?: string): Promise<number> {
  const response = await coreApi.apiCoreConversationArchiveFoldersFolderIdDelete({
    folderId,
    moveToFolderId,
  });
  return response.data.moved_count || 0;
}

const toPage = (data: ConversationRecoveryListResponse): RecoveryPage<ConversationRecoveryItem> => ({
  items: data.items || [],
  total: data.total || 0,
  page: data.page || 1,
  pageSize: data.page_size || 20,
});

export async function listArchivedConversations(options: {
  kind: RecoveryKind;
  keyword?: string;
  folderId?: RecoveryFolderFilter;
  page?: number;
  pageSize?: number;
  signal?: AbortSignal;
}): Promise<RecoveryPage<ConversationRecoveryItem>> {
  const response = await coreApi.apiCoreConversationsArchivedGet({
    kind: options.kind,
    keyword: options.keyword || undefined,
    folderId: options.folderId || "all",
    page: options.page || 1,
    pageSize: options.pageSize || 20,
  }, { signal: options.signal });
  return toPage(response.data);
}

export async function listTrashedConversations(options: {
  kind: RecoveryKind;
  keyword?: string;
  page?: number;
  pageSize?: number;
  signal?: AbortSignal;
}): Promise<RecoveryPage<ConversationRecoveryItem>> {
  const response = await coreApi.apiCoreConversationsTrashGet({
    kind: options.kind,
    keyword: options.keyword || undefined,
    page: options.page || 1,
    pageSize: options.pageSize || 20,
  }, { signal: options.signal });
  return toPage(response.data);
}

export async function archiveConversation(conversationId: string, folderId?: string | null): Promise<void> {
  await coreApi.apiCoreConversationsConversationIdArchivePost({
    conversationId,
    conversationArchiveRequest: { folder_id: folderId || null },
  }, { silentError: true } as never);
}

export async function unarchiveConversation(conversationId: string): Promise<void> {
  await coreApi.apiCoreConversationsConversationIdUnarchivePost({ conversationId });
}

export async function restoreConversation(conversationId: string): Promise<void> {
  await coreApi.apiCoreConversationsConversationIdRestorePost({ conversationId });
}

export async function trashConversation(conversationId: string): Promise<void> {
  await coreApi.apiCoreConversationsNameDelete({ name: conversationId });
}

export async function purgeConversation(conversationId: string): Promise<void> {
  await coreApi.apiCoreConversationsConversationIdPurgeDelete({ conversationId });
}

export async function emptyConversationTrash(kind: RecoveryKind): Promise<number> {
  const response = await coreApi.apiCoreConversationsTrashDelete({ kind });
  return response.data.deleted_count || 0;
}

export async function listWorkflowTrash(options: {
  keyword?: string;
  page?: number;
  pageSize?: number;
  signal?: AbortSignal;
}): Promise<RecoveryPage<WorkflowTrashItem>> {
  const response = await coreApi.apiCoreWorkflowDraftsTrashGet({
    keyword: options.keyword || undefined,
    page: options.page || 1,
    pageSize: options.pageSize || 12,
  }, { signal: options.signal });
  const data = response.data.data;
  return { items: data.records || [], total: data.total || 0, page: data.page || 1, pageSize: data.page_size || 12 };
}

export async function restoreWorkflow(draftId: string): Promise<void> {
  await coreApi.apiCoreWorkflowDraftsDraftIdRestorePost({ draftId });
}

export async function purgeWorkflow(draftId: string): Promise<void> {
  await coreApi.apiCoreWorkflowDraftsDraftIdPurgeDelete({ draftId });
}

export async function emptyWorkflowTrash(): Promise<number> {
  const response = await coreApi.apiCoreWorkflowDraftsTrashDelete();
  return response.data.data?.purged || 0;
}

export async function listSkillTrash(options: {
  keyword?: string;
  category?: string;
  page?: number;
  pageSize?: number;
}): Promise<RecoverySkillPage> {
  const response = await listTrashedSkillAssetsPage(options);
  return {
    items: response.records.map((item) => ({
      ...item,
      trashExpiresAt: (item as SkillAssetRecord & { trashExpiresAt?: string }).trashExpiresAt,
    })),
    total: response.total,
    page: response.page,
    pageSize: response.pageSize,
    categories: response.categories,
  };
}

export { emptySkillTrash, purgeSkillAsset, restoreSkillAsset };
