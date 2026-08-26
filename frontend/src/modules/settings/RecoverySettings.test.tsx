import { createRef } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import RecoverySettings from "./RecoverySettings";

const mocks = vi.hoisted(() => ({
  listArchiveFolders: vi.fn(),
  createArchiveFolder: vi.fn(),
  archiveConversation: vi.fn(),
  updateArchiveFolder: vi.fn(),
  deleteArchiveFolder: vi.fn(),
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
        "settingsPage.recovery.createInline": "新建文件夹",
        "settingsPage.recovery.manageFolders": "管理文件夹",
        "settingsPage.recovery.folderName": "文件夹名称",
        "settingsPage.recovery.folderPlaceholder": "输入文件夹名称",
        "settingsPage.recovery.create": "创建",
        "settingsPage.recovery.move": "移动",
        "settingsPage.recovery.moveToFolder": "移动到文件夹",
        "settingsPage.recovery.selectFolder": "选择文件夹",
        "settingsPage.recovery.defaultFolder": "默认",
        "settingsPage.recovery.createAutoSelect": "创建后将自动选中新文件夹。",
        "settingsPage.recovery.folderDuplicate": "已存在同名文件夹",
        "settingsPage.recovery.folderCreateFailed": "文件夹创建失败，请重试",
        "settingsPage.recovery.folderCreatedNamed": `已创建文件夹“${values?.name}”`,
        "settingsPage.recovery.alreadyInNamed": `“${values?.name}”已在“${values?.folder}”中`,
        "settingsPage.recovery.movedNamed": `已将“${values?.name}”移动到“${values?.folder}”`,
        "settingsPage.recovery.editFolderNamed": `编辑“${values?.name}”`,
        "settingsPage.recovery.deleteFolderNamed": `删除“${values?.name}”`,
        "settingsPage.recovery.deleteFolderTitle": `删除文件夹“${values?.name}”？`,
        "settingsPage.recovery.deleteNonEmptyFolderDescription": `该文件夹包含 ${values?.count} 项，请选择移动目标后再删除。`,
        "settingsPage.recovery.deleteMoveTarget": "内容移动到",
        "settingsPage.recovery.deleteFolder": "删除文件夹",
        "common.save": "保存",
        "common.close": "关闭",
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
      if (key === "settingsPage.recovery.conversationCount") return `${values?.count} 个会话`;
      if (key === "settingsPage.recovery.daysRemaining") return `${values?.count} 天后自动清理`;
      return labels[key] || key;
    },
  }),
}));

vi.mock("./recoveryApi", () => ({
  listArchiveFolders: mocks.listArchiveFolders,
  createArchiveFolder: mocks.createArchiveFolder,
  updateArchiveFolder: mocks.updateArchiveFolder,
  deleteArchiveFolder: mocks.deleteArchiveFolder,
  listArchivedConversations: mocks.listArchivedConversations,
  listTrashedConversations: mocks.listTrashedConversations,
  listSkillTrash: mocks.listSkillTrash,
  listWorkflowTrash: mocks.listWorkflowTrash,
  archiveConversation: mocks.archiveConversation,
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
    mocks.archiveConversation.mockResolvedValue(undefined);
    mocks.updateArchiveFolder.mockResolvedValue(undefined);
    mocks.deleteArchiveFolder.mockResolvedValue(1);
  });

  function openArchiveView() {
    fireEvent.click(screen.getByRole("tab", { name: /已归档/ }));
  }

  it("opens on Trash with the skill category selected", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);

    expect(screen.getByRole("tab", { name: /回收站/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: /已归档/ })).toHaveAttribute("aria-selected", "false");
    expect(screen.getByRole("tab", { name: "技能" })).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(mocks.listSkillTrash).toHaveBeenCalled());
  });

  it("updates the remaining retention days without reloading the page", async () => {
    mocks.listSkillTrash.mockResolvedValue({
      items: [{
        skillId: "skill-1",
        name: "实时清理技能",
        description: "",
        deletedAt: "2026-08-25T00:00:00Z",
        trashExpiresAt: "2026-08-27T00:00:00Z",
      }],
      total: 1,
      page: 1,
      pageSize: 12,
      categories: [],
    });
    const nowSpy = vi.spyOn(Date, "now").mockReturnValue(new Date("2026-08-25T00:00:00Z").getTime());
    let refreshRetention: (() => void) | undefined;
    const intervalSpy = vi.spyOn(window, "setInterval").mockImplementation((handler: TimerHandler, timeout?: number) => {
      if (timeout === 60_000) refreshRetention = handler as () => void;
      return 1;
    });
    const view = render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);

    try {
      expect(await screen.findAllByText("2 天后自动清理")).toHaveLength(1);
      const requestCount = mocks.listSkillTrash.mock.calls.length;

      nowSpy.mockReturnValue(new Date("2026-08-26T00:00:01Z").getTime());
      act(() => refreshRetention?.());

      expect(screen.getAllByText("1 天后自动清理")).toHaveLength(1);
      expect(mocks.listSkillTrash).toHaveBeenCalledTimes(requestCount);
    } finally {
      view.unmount();
      intervalSpy.mockRestore();
      nowSpy.mockRestore();
    }
  });

  it("switches trash categories without losing the page", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
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
    openArchiveView();
    fireEvent.click(screen.getByRole("button", { name: /新建文件夹/ }));
    const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "知识库" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));

    await waitFor(() => expect(mocks.createArchiveFolder).toHaveBeenCalledWith("知识库"));
  });

  it("renames a custom archive folder from the folder manager", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    fireEvent.click(screen.getByRole("button", { name: "管理文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "管理文件夹" });

    expect(within(dialog).queryByText("未分类")).not.toBeInTheDocument();
    fireEvent.click(await within(dialog).findByRole("button", { name: "编辑“产品设计”" }));
    const input = within(dialog).getByRole("textbox", { name: "文件夹名称" });
    fireEvent.change(input, { target: { value: "产品研发" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));

    await waitFor(() => expect(mocks.updateArchiveFolder).toHaveBeenCalledWith("folder-1", "产品研发"));
  });

  it("requires a move target when deleting a non-empty archive folder", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    fireEvent.click(screen.getByRole("button", { name: "管理文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "管理文件夹" });

    fireEvent.click(await within(dialog).findByRole("button", { name: "删除“产品设计”" }));
    expect(await within(dialog).findByText("该文件夹包含 1 项，请选择移动目标后再删除。")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "删除文件夹" }));

    await waitFor(() => expect(mocks.deleteArchiveFolder).toHaveBeenCalledWith("folder-1", "unfiled"));
  });

  it("renders the reference move dialog and moves to the selected folder", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    const itemName = await screen.findByText("设置页信息架构整理");
    const row = itemName.closest(".recovery-row");
    expect(row).not.toBeNull();

    fireEvent.click(within(row as HTMLElement).getByRole("button", { name: "移动到" }));
    const dialog = await screen.findByRole("dialog", { name: "移动到文件夹" });

    expect(within(dialog).getByText("设置页信息架构整理")).toBeInTheDocument();
    expect(within(dialog).getByText("选择文件夹")).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: /产品设计.*1 个会话/ })).toBeChecked();

    fireEvent.click(within(dialog).getByRole("radio", { name: /未分类.*默认.*0 个会话/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: /^移动$/ }));

    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledWith("conversation-1", null));
  });

  it("creates a folder inside the move dialog, auto-selects it, and moves there", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    const itemName = await screen.findByText("设置页信息架构整理");
    const row = itemName.closest(".recovery-row");
    expect(row).not.toBeNull();

    fireEvent.click(within(row as HTMLElement).getByRole("button", { name: "移动到" }));
    const dialog = await screen.findByRole("dialog", { name: "移动到文件夹" });
    fireEvent.click(within(dialog).getByRole("button", { name: "新建文件夹" }));

    const input = within(dialog).getByRole("textbox", { name: "文件夹名称" });
    expect(within(dialog).getByRole("button", { name: /^移动$/ })).toBeDisabled();
    fireEvent.change(input, { target: { value: "知识库" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^创建$/ }));

    await waitFor(() => expect(mocks.createArchiveFolder).toHaveBeenCalledWith("知识库"));
    expect(await within(dialog).findByRole("radio", { name: /知识库.*0 个会话/ })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: /^移动$/ }));

    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledWith("conversation-1", "folder-2"));
  });

  it("rejects duplicate folder names inside the move dialog", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    const itemName = await screen.findByText("设置页信息架构整理");
    const row = itemName.closest(".recovery-row");
    expect(row).not.toBeNull();

    fireEvent.click(within(row as HTMLElement).getByRole("button", { name: "移动到" }));
    const dialog = await screen.findByRole("dialog", { name: "移动到文件夹" });
    fireEvent.click(within(dialog).getByRole("button", { name: "新建文件夹" }));
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: " 产品设计 " } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^创建$/ }));

    expect(await within(dialog).findByText("已存在同名文件夹")).toBeInTheDocument();
    expect(mocks.createArchiveFolder).not.toHaveBeenCalled();
  });

  it("closes without a request when the item is already in the selected folder", async () => {
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    const itemName = await screen.findByText("设置页信息架构整理");
    const row = itemName.closest(".recovery-row");
    expect(row).not.toBeNull();

    fireEvent.click(within(row as HTMLElement).getByRole("button", { name: "移动到" }));
    const dialog = await screen.findByRole("dialog", { name: "移动到文件夹" });
    expect(within(dialog).getByRole("radio", { name: /产品设计.*1 个会话/ })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: /^移动$/ }));

    await waitFor(() => expect(dialog).toHaveClass("ant-zoom-leave"));
    expect(mocks.archiveConversation).not.toHaveBeenCalled();
  });

  it("keeps the move dialog and selected destination available after a failed request", async () => {
    mocks.archiveConversation.mockRejectedValueOnce(new Error("move failed"));
    render(<RecoverySettings headingRef={createRef<HTMLHeadingElement>()} />);
    openArchiveView();
    const itemName = await screen.findByText("设置页信息架构整理");
    const row = itemName.closest(".recovery-row");
    expect(row).not.toBeNull();

    fireEvent.click(within(row as HTMLElement).getByRole("button", { name: "移动到" }));
    const dialog = await screen.findByRole("dialog", { name: "移动到文件夹" });
    const unfiled = within(dialog).getByRole("radio", { name: /未分类.*默认.*0 个会话/ });
    fireEvent.click(unfiled);
    fireEvent.click(within(dialog).getByRole("button", { name: /^移动$/ }));

    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("dialog", { name: "移动到文件夹" })).toBeInTheDocument();
    expect(unfiled).toBeChecked();
    await waitFor(() => expect(within(dialog).getByRole("button", { name: /^移动$/ })).not.toBeDisabled());

    mocks.archiveConversation.mockResolvedValueOnce(undefined);
    fireEvent.click(within(dialog).getByRole("button", { name: /^移动$/ }));
    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledTimes(2));
  });
});
