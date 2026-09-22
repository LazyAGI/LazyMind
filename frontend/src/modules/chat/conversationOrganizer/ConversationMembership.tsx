import { Button, Dropdown, Modal, message } from "antd";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import ArchiveConversationModal from "@/modules/chat/components/ArchiveConversationModal";
import { conversationGroupSubmenu } from "./ConversationGroupPicker";
import ConversationMembershipModal from "./ConversationMembershipModal";
import { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { getChatConversationPath } from "@/modules/chat/constants/chat";
import { useTranslation } from "react-i18next";
import { emitConversationGroupsChanged } from "./api";

export default function ConversationMembership({ conversationId, groupId, groupKind, title, disabled = false, pinned = false, onRename }: {
  conversationId: string; groupId?: string | null; groupKind?: string; title?: string; disabled?: boolean; pinned?: boolean; onRename: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [modal, modalContextHolder] = Modal.useModal();
  const location = useLocation();
  const removed = () => { emitConversationGroupsChanged(); if (location.pathname === getChatConversationPath(conversationId)) navigate("/agent/chat/home", { replace: true }); };
  const [open, setOpen] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  return <>
    {modalContextHolder}
    <Dropdown destroyPopupOnHide trigger={["click"]} menu={{ items: [
      { key: "pin", label: t(pinned ? "chat.unpinConversation" : "chat.pinConversation"), onClick: async () => { await ChatServiceApi().conversationServiceSetPinned(conversationId, !pinned); emitConversationGroupsChanged(); } },
      { key: "rename", label: t("conversationOrganizer.renameConversation"), onClick: onRename },
      ...(groupKind === "project" ? [] : [{ key: "move", label: t("conversationOrganizer.adjustMembership"), disabled, children: conversationGroupSubmenu({ conversationId, groupId, title }, () => setOpen(true)) }]),
      { key: "archive", label: t("settingsPage.recovery.archiveAction"), disabled, onClick: () => setArchiveOpen(true) },
      { key: "trash", label: t("common.delete"), disabled, danger: true, onClick: async () => { await modal.confirm({
        title: t("settingsPage.recovery.moveToTrashTitle"),
        content: t("settingsPage.recovery.moveToTrashDescription") + " " + t("chat.fork.deleteNotice"),
        okText: t("common.delete"),
        cancelText: t("common.cancel"),
        okButtonProps: { danger: true },
        onOk: async () => {
          try {
            await ChatServiceApi().conversationServiceDeleteConversation({ conversation: conversationId });
            message.success(t("chat.deleteConversationSuccess"));
            removed();
          } catch (error) {
            message.error(t("settingsPage.recovery.operationFailed"));
            throw error;
          }
        },
      }); } },
    ] }}><Button size="small" type="text" aria-label={t("conversationOrganizer.conversationMore", { name: title })}>···</Button></Dropdown>
    <ArchiveConversationModal open={archiveOpen} conversationId={conversationId} title={title} onCancel={() => setArchiveOpen(false)} onArchived={() => { setArchiveOpen(false); message.success(t("settingsPage.recovery.archivedSuccess")); removed(); }} />
    <ConversationMembershipModal conversation={open ? { conversationId, groupId, title } : null} onClose={() => setOpen(false)} />
  </>;
}
