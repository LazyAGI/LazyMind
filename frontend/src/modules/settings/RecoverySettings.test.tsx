import { createRef } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import RecoverySettings from "./RecoverySettings";

const mocks = vi.hoisted(() => ({
  listArchiveFolders: vi.fn(),
  createArchiveFolder: vi.fn(),
  listArchivedConversations: vi.fn(),
  listTrashedConversations: vi.fn(),
  listSkillTrash: vi.fn(),
  listWorkflowTrash: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN" },
    t: (key: string, values?: Record<string, unknown>) => {
      const labels: Record<string, string> = {
        "settingsPage.recovery.title": "回收站与归档",
        "settingsPage.recovery.description": "归档可取消；回收站保留 30 天。",
        "settingsPage.recovery.viewTabs": "回收站与归档视图",
        "settingsPage.recovery.trash": "回收站",
        "settingsPage.recovery.trashSubtitle": "恢复或彻底删除内容",
        "settingsPage.recovery.archive": "已归档",
        "settingsPage.recovery.archiveSubtitle": "管理已归档的会话",
        "settingsPage.recovery.dialog": "对话",
        "settingsPage.recovery.task": "任务",
        "settingsPage.recovery.skills": "技能",
        "settingsPage.recovery.conversations": "会话",
        "settingsPage.recovery.workflows": "工作流",
        "settingsPage.recovery.sessionType": "会话类型",
        "settingsPage.recovery.trashCategory": "回收站分类",
        "settingsPage.recovery.searchArchived": "搜索已归档内容",
        "settingsPage.recovery.folderFilter": "归档文件夹筛选",
        "settingsPage.recovery.allFolders": "全部文件夹",
        "settingsPage.recovery.unfiled": "未分类",
        "settingsPage.recovery.newFolder": "新建文件夹",
        "settingsPage.recovery.folderName": "文件夹名称",
        "settingsPage.recovery.folderPlaceholder": "输入文件夹名称",
        "settingsPage.recovery.create": "创建",
        "settingsPage.recovery.name": "名称",
        "settingsPage.recovery.archivedAt": "归档时间",
        "settingsPage.recovery.actions": "操作",
        "settingsPage.recovery.moveTo": "移动到",
        "settingsPage.recovery.unarchive": "取消归档",
        "settingsPage.recovery.moveToTrash": "移入回收站",
        "settingsPage.recovery.moreActions": "更多操作",
        "settingsPage.recovery.emptyTrash": "清空回收站",
        "settingsPage.recovery.allCategories": "全部分类",
        "settingsPage.recovery.trashEmpty": "当前分类的回收站为空",
        "settingsPage.recovery.search.skills": "搜索技能名称或描述",
        "settingsPage.recovery.search.conversations": "搜索已删除会话",
        "settingsPage.recovery.search.workflows": "搜索工作流名称或标识",
        "common.cancel": "取消",
      };
      if (key === "settingsPage.recovery.itemCount") return `${values?.count} 项`;
      return labels[key] || key;
    },
  }),
}));

vi.mock("./recoveryApi", () => ({
  listArchiveFolders: mocks.listArchiveFolders,
  createArchiveFolder: mocks.createArchiveFolder,
  listArchivedConversations: mocks.listArchivedConversations,
  listTrashedConversations: mocks.listTrashedConversations,
  listSkillTrash: mocks.listSkillTrash,
  listWorkflowTrash: mocks.listWorkflowTrash,
  archiveConversation: vi.fn(),
  emptyConversationTrash: vi.fn(),
  emptySkillTrash: vi.fn(),
  emptyWorkflowTrash: vi.fn(),
  purgeConversation: vi.fn(),
  purgeSkillAsset: vi.fn(),
  purgeWorkflow: vi.fn(),
  restoreConversation: vi.fn(),
  restoreSkillAsset: vi.fn(),
  restoreWorkflow: vi.fn(),
  trashConversation: vi.fn(),
  unarchiveConversation: vi.fn(),
}));

describe("RecoverySettings", () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.listArchiveFolders.mockResolvedValue({
      folders: [{
        id: "folder-1",
        name: "产品设计",
        dialog_count: 1,
        task_count: 0,
        total_count: 1,
        created_at: "2026-08-18T08:00:00Z",
        updated_at: "2026-08-18T08:00:00Z",
      }],
      unfiledDialogCount: 0,
      unfiledTaskCount: 0,
      unfiledTotalCount: 0,
    });
    mocks.listArchivedConversations.mockResolvedValue({
      items: [{
        conversation_id: "conversation-1",
        display_name: "设置页信息架构整理",
        kind: "dialog",
        folder_id: "folder-1",
        archived_at: "2026-08-18T08:00:00Z",
        create_time: "2026-08-18T07:00:00Z",
        update_time: "2026-08-18T08:00:00Z",
      }],
      total: 1,
      page: 1,
      pageSize: 20,
    });
    const emptyPage = { items: [], total: 0, page: 1, pageSize: 12 };
    mocks.listSkillTrash.mockResolvedValue(emptyPage);
    mocks.listTrashedConversations.mockResolvedValue({ ...emptyPage, pageSize: 20 });
    mocks.listWorkflowTrash.mockResolvedValue(emptyPage);
    mocks.createArchiveFolder.mockResolvedValue({
      id: "folder-2",
      name: "知识库",
      dialog_count: 0,
      task_count: 0,
      total_count: 0,
      created_at: "2026-08-18T09:00:00Z",
      updated_at: "2026-08-18T09:00:00Z",
    });
  });

  it("opens on Archived and renders grouped conversation data", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);

    expect(screen.getByRole("tab", { name: /已归档/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: /回收站/ })).toHaveAttribute("aria-selected", "false");
    expect(await screen.findByText("设置页信息架构整理")).toBeInTheDocument();
    expect(screen.getByText("产品设计")).toBeInTheDocument();
    expect(mocks.listArchivedConversations).toHaveBeenCalledWith(expect.objectContaining({
      kind: "dialog",
      folderId: "all",
      page: 1,
    }));
  });

  it("switches trash categories without losing the page", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    fireEvent.click(screen.getByRole("tab", { name: /回收站/ }));
    expect(screen.getByRole("tab", { name: "技能" })).toHaveAttribute("aria-selected", "true");

    fireEvent.click(screen.getByRole("tab", { name: "会话" }));
    await waitFor(() => expect(mocks.listTrashedConversations).toHaveBeenCalledWith(expect.objectContaining({
      kind: "dialog",
      page: 1,
      pageSize: 20,
    })));
  });

  it("creates a folder from the archive toolbar", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    fireEvent.click(screen.getByRole("button", { name: /新建文件夹/ }));
    const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "知识库" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));

    await waitFor(() => expect(mocks.createArchiveFolder).toHaveBeenCalledWith("知识库"));
  });
});
