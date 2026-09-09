import { Button, Dropdown, Input, Modal } from "antd";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import ArchiveConversationModal from "@/modules/chat/components/ArchiveConversationModal";
import { renameGroupConversation } from "./api";
import { conversationGroupSubmenu } from "./ConversationGroupPicker";
import ConversationMembershipModal from "./ConversationMembershipModal";
import { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { getChatConversationPath } from "@/modules/chat/constants/chat";
import { useTranslation } from "react-i18next";
import { emitConversationGroupsChanged } from "./api";

export default function ConversationMembership({ conversationId, groupId, title, disabled = false, pinned = false }: {
  conversationId: string; groupId?: string | null; title?: string; disabled?: boolean; pinned?: boolean;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const removed = () => { emitConversationGroupsChanged(); if (location.pathname === getChatConversationPath(conversationId)) navigate("/agent/chat/home", { replace: true }); };
  const [open, setOpen] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [rename, setRename] = useState<{ title: string; revision: number } | null>(null);
  return <>
    <Dropdown destroyPopupOnHide trigger={["click"]} menu={{ items: [
      { key: "pin", label: t(pinned ? "chat.unpinConversation" : "chat.pinConversation"), onClick: async () => { await ChatServiceApi().conversationServiceSetPinned(conversationId, !pinned); emitConversationGroupsChanged(); } },
      { key: "rename", label: t("conversationOrganizer.renameConversation"), onClick: async () => { const response = await ChatServiceApi().conversationServiceGetConversationDetail({ conversation: conversationId }); const item = response.data.conversation as { display_name?: string; title_revision?: number }; setRename({ title: item.display_name || title || "", revision: item.title_revision || 0 }); } },
      { key: "move", label: t("conversationOrganizer.adjustMembership"), disabled, children: conversationGroupSubmenu({ conversationId, groupId, title }, () => setOpen(true)) },
      { key: "archive", label: t("settingsPage.recovery.archiveAction"), disabled, onClick: () => setArchiveOpen(true) },
      { key: "trash", label: t("settingsPage.recovery.moveToTrash"), disabled, danger: true, onClick: () => Modal.confirm({ title: t("settingsPage.recovery.moveToTrashTitle", { name: title }), okButtonProps: { danger: true }, onOk: async () => { await ChatServiceApi().conversationServiceDeleteConversation({ conversation: conversationId }); removed(); } }) },
    ] }}><Button size="small" type="text" aria-label={t("conversationOrganizer.conversationMore", { name: title })}>···</Button></Dropdown>
    <ArchiveConversationModal open={archiveOpen} conversationId={conversationId} title={title} onCancel={() => setArchiveOpen(false)} onArchived={() => { setArchiveOpen(false); removed(); }} />
    <Modal open={Boolean(rename)} title={t("conversationOrganizer.renameConversation")} onCancel={() => setRename(null)} okButtonProps={{ disabled: !rename?.title.trim() }} onOk={async () => { if (!rename) return; await renameGroupConversation(conversationId, rename.title.trim(), rename.revision); setRename(null); emitConversationGroupsChanged(); }}><Input value={rename?.title || ""} maxLength={255} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setRename(current => current ? { ...current, title: e.target.value } : null)} /></Modal>
    <ConversationMembershipModal conversation={open ? { conversationId, groupId, title } : null} onClose={() => setOpen(false)} />
  </>;
}
