import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ComponentProps } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ArchiveConversationModal from "./ArchiveConversationModal";

const mocks = vi.hoisted(() => ({
  archiveConversation: vi.fn(),
  createArchiveFolder: vi.fn(),
  listArchiveFolders: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      const labels: Record<string, string> = {
        "common.cancel": "取消",
        "common.retry": "重试",
        "settingsPage.recovery.archiveAction": "归档",
        "settingsPage.recovery.archiveToFolder": "归档到文件夹",
        "settingsPage.recovery.unfiled": "未分类",
        "settingsPage.recovery.selectFolder": "选择文件夹",
        "settingsPage.recovery.defaultFolder": "默认",
        "settingsPage.recovery.conversationCount": "{{count}} 个会话",
        "settingsPage.recovery.createInline": "新建文件夹",
        "settingsPage.recovery.folderName": "文件夹名称",
        "settingsPage.recovery.createAutoSelect": "创建后将自动选中新文件夹。",
        "settingsPage.recovery.folderRequired": "请输入文件夹名称",
        "settingsPage.recovery.folderTooLong": "文件夹名称不能超过 30 个字符",
        "settingsPage.recovery.folderDuplicate": "已存在同名文件夹",
        "settingsPage.recovery.folderCreatedNamed": "已创建文件夹“{{name}}”",
        "settingsPage.recovery.folderCreateFailed": "文件夹创建失败，请重试",
        "settingsPage.recovery.folderLoadFailed": "文件夹加载失败，已保留当前列表",
        "settingsPage.recovery.create": "创建",
        "settingsPage.recovery.operationFailed": "操作失败，请重试",
      };
      return (labels[key] || key).replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(values?.[name] ?? ""));
    },
  }),
}));

vi.mock("@/modules/settings/recoveryApi", () => ({
  archiveConversation: mocks.archiveConversation,
  createArchiveFolder: mocks.createArchiveFolder,
  listArchiveFolders: mocks.listArchiveFolders,
}));

const productFolder = {
  id: "folder-1",
  name: "产品设计",
  dialog_count: 2,
  task_count: 1,
  total_count: 3,
  created_at: "2026-08-24T08:00:00Z",
  updated_at: "2026-08-24T08:00:00Z",
};

function renderModal(overrides: Partial<ComponentProps<typeof ArchiveConversationModal>> = {}) {
  const onArchived = vi.fn();
  const onCancel = vi.fn();
  render(
    <ArchiveConversationModal
      open
      conversationId="conversation-1"
      title="北京今天天气如何"
      onArchived={onArchived}
      onCancel={onCancel}
      {...overrides}
    />,
  );
  return { onArchived, onCancel };
}

describe("ArchiveConversationModal", () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.listArchiveFolders.mockResolvedValue({
      folders: [productFolder],
      unfiledDialogCount: 1,
      unfiledTaskCount: 1,
      unfiledTotalCount: 2,
    });
    mocks.createArchiveFolder.mockResolvedValue({
      id: "folder-2",
      name: "知识库",
      dialog_count: 0,
      task_count: 0,
      total_count: 0,
      created_at: "2026-08-24T09:00:00Z",
      updated_at: "2026-08-24T09:00:00Z",
    });
    mocks.archiveConversation.mockResolvedValue(undefined);
  });

  it("renders the shared folder picker and archives to a custom folder", async () => {
    const { onArchived } = renderModal();
    const dialog = await screen.findByRole("dialog", { name: "归档到文件夹" });

    expect(within(dialog).getByText("北京今天天气如何")).toBeInTheDocument();
    expect(await within(dialog).findByRole("radio", { name: /未分类.*默认.*2 个会话/ })).toBeChecked();
    const product = within(dialog).getByRole("radio", { name: /产品设计.*3 个会话/ });
    fireEvent.click(product);
    fireEvent.click(within(dialog).getByRole("button", { name: "归档" }));

    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledWith("conversation-1", "folder-1"));
    expect(onArchived).toHaveBeenCalledOnce();
  });

  it("creates a folder inline, auto-selects it, and waits for explicit archive", async () => {
    renderModal();
    const dialog = await screen.findByRole("dialog", { name: "归档到文件夹" });
    await within(dialog).findByRole("radio", { name: /产品设计.*3 个会话/ });
    fireEvent.click(within(dialog).getByRole("button", { name: "新建文件夹" }));

    expect(within(dialog).getByRole("button", { name: "归档" })).toBeDisabled();
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), {
      target: { value: "知识库" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "创建" }));

    await waitFor(() => expect(mocks.createArchiveFolder).toHaveBeenCalledWith("知识库"));
    expect(mocks.archiveConversation).not.toHaveBeenCalled();
    expect(await within(dialog).findByRole("radio", { name: /知识库.*0 个会话/ })).toBeChecked();

    fireEvent.click(within(dialog).getByRole("button", { name: "归档" }));
    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledWith("conversation-1", "folder-2"));
  });

  it("keeps the selected destination available when archiving fails", async () => {
    mocks.archiveConversation.mockRejectedValueOnce(new Error("archive failed"));
    const { onArchived } = renderModal();
    const dialog = await screen.findByRole("dialog", { name: "归档到文件夹" });
    const product = await within(dialog).findByRole("radio", { name: /产品设计.*3 个会话/ });
    fireEvent.click(product);
    fireEvent.click(within(dialog).getByRole("button", { name: "归档" }));

    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledOnce());
    expect(screen.getByRole("dialog", { name: "归档到文件夹" })).toBeInTheDocument();
    expect(product).toBeChecked();
    expect(onArchived).not.toHaveBeenCalled();
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "归档" })).not.toBeDisabled());
  });

  it("sends null when the conversation stays unfiled", async () => {
    renderModal();
    const dialog = await screen.findByRole("dialog", { name: "归档到文件夹" });
    await within(dialog).findByRole("radio", { name: /未分类.*默认.*2 个会话/ });
    fireEvent.click(within(dialog).getByRole("button", { name: "归档" }));

    await waitFor(() => expect(mocks.archiveConversation).toHaveBeenCalledWith("conversation-1", null));
  });

  it("does not enable archive without a conversation id", async () => {
    renderModal({ conversationId: undefined });
    const dialog = await screen.findByRole("dialog", { name: "归档到文件夹" });
    await within(dialog).findByRole("radio", { name: /未分类.*默认.*2 个会话/ });

    expect(within(dialog).getByRole("button", { name: "归档" })).toBeDisabled();
    expect(mocks.archiveConversation).not.toHaveBeenCalled();
  });
});
