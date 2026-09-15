import { EditOutlined, FolderOpenOutlined, MessageOutlined, MoreOutlined, PlusOutlined } from "@ant-design/icons";
import { Button, Dropdown, Empty, Form, Modal, Spin, message } from "antd";
import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { CHAT_PENDING_CONVERSATION_GROUP_KEY, CHAT_PENDING_CONVERSATION_PROMPT_KEY, getChatConversationPath } from "@/modules/chat/constants/chat";
import {
  getConversationGroup,
  deleteConversationGroup,
  updateConversationGroup,
  type ConversationGroup,
  emitConversationGroupsChanged,
  CONVERSATION_GROUPS_CHANGED_EVENT,
  updateGroupPlacement,
} from "./api";
import type { ConversationGroupMember } from "@/api/generated/core-client";
import "./index.scss";
import useOrganizerNameLock from "./useOrganizerNameLock";
import GroupFields, { normalizeGroupValues } from "./GroupFields";
import ConversationMembership from "./ConversationMembership";
import ChatInput from "@/modules/chat/components/ChatInput";
import { useChatModelProviderGuard } from "@/modules/chat/hooks/useChatModelProviderGuard";
import type { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { startConversationDrag } from "./drag";
import "./groupPage.scss";

export default function ConversationGroupPage() {
  const { groupId = "" } = useParams<{ groupId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const namesLocked = useOrganizerNameLock();
  const modelGuard = useChatModelProviderGuard();
  const [group, setGroup] = useState<ConversationGroup | null>(null);
  const [conversations, setConversations] = useState<ConversationGroupMember[]>([]);
  const [nextPageToken, setNextPageToken] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [editing, setEditing] = useState(false);
  const [chatConfig, setChatConfig] = useState<ChatConfig>({});
  const [prompt, setPrompt] = useState("");
  const [form] = Form.useForm<{ name: string; scope?: string }>();

  const load = useCallback(async (append = false, token = "") => {
    if (!groupId) return;
    setLoading(true);
    try {
      const detail = await getConversationGroup(groupId, token);
      setGroup(detail.group);
      setConversations((current) => append ? [...current, ...detail.conversations] : detail.conversations);
      setNextPageToken(detail.nextPageToken);
      setLoadError(false);
    } catch {
      setLoadError(true);
    } finally { setLoading(false); }
  }, [groupId]);

  useEffect(() => {
    const refresh = () => void load();
    refresh(); window.addEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, refresh); window.addEventListener("focus", refresh);
    return () => { window.removeEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, refresh); window.removeEventListener("focus", refresh); };
  }, [load]);

  const startChat = () => {
    sessionStorage.setItem(CHAT_PENDING_CONVERSATION_GROUP_KEY, groupId);
    sessionStorage.removeItem(CHAT_PENDING_CONVERSATION_PROMPT_KEY);
    navigate("/agent/chat/home");
  };

  const save = async () => {
    if (!group) return;
    const values = await form.validateFields();
    await updateConversationGroup(group.id, normalizeGroupValues(values));
    setEditing(false);
    emitConversationGroupsChanged();
    await load();
    message.success(t("conversationOrganizer.updated"));
  };

  if (loading && !group) return <div className="conversation-group-page"><Spin /></div>;
  if (loadError && !group) return <div className="conversation-group-page"><Empty description={t("conversationOrganizer.loadFailed")} /><Button onClick={() => void load()}>{t("conversationOrganizer.retryLoad")}</Button></div>;
  if (!group) return <div className="conversation-group-page"><Empty description={t("conversationOrganizer.notFound")} /></div>;

  return <main className="conversation-group-page">
    <div className="conversation-group-page-breadcrumb"><button onClick={() => navigate("/agent/chat/home")}>{t("conversationOrganizer.home")}</button><span>/</span><span>{group.name}</span></div>
    <header className="conversation-group-page-header">
      <div className="conversation-group-page-symbol"><FolderOpenOutlined /></div>
      <div className="conversation-group-page-heading"><h1>{group.name}</h1><small>{t("conversationOrganizer.count", { count: group.member_count })} · {t("conversationOrganizer.independentContexts")}</small></div>
      <Dropdown trigger={["click"]} menu={{ items: [
        { key: "pin", label: t(group.pinned ? "conversationOrganizer.unpinGroup" : "conversationOrganizer.pinGroup"), onClick: async () => { await updateGroupPlacement(group.id, { pinned: !group.pinned }); emitConversationGroupsChanged(); } },
        { key: "edit", label: t("conversationOrganizer.editGroup"), onClick: () => { form.setFieldsValue({ name: group.name, scope: group.scope }); setEditing(true); } },
        { key: "remove", label: t("conversationOrganizer.removeGroup"), danger: true, onClick: () => Modal.confirm({ title: t("conversationOrganizer.removeConfirm", { name: group.name }), content: t("conversationOrganizer.removeHint"), okText: t("conversationOrganizer.removeGroup"), cancelText: t("common.cancel"), onOk: async () => { await deleteConversationGroup(group.id); emitConversationGroupsChanged(); navigate("/agent/chat/home"); } }) },
      ] }}><Button type="text" icon={<MoreOutlined />} aria-label={t("conversationOrganizer.groupMore", { name: group.name })} /></Dropdown>
    </header>
    <div className="conversation-group-page-scope">
      {group.scope && <span>{group.scope}</span>}
      <Button type="link" size="small" icon={<EditOutlined />} onClick={() => { form.setFieldsValue({ name: group.name, scope: group.scope }); setEditing(true); }}>{t(group.scope ? "conversationOrganizer.editGroupShort" : "conversationOrganizer.fillScope")}</Button>
    </div>
    <div className="conversation-group-page-composer-hint">{t("conversationOrganizer.groupComposerHint", { name: group.name })}</div>
    <div className="conversation-group-page-composer"><ChatInput disabled={!modelGuard.canChat} disabledReason={t(modelGuard.isChecking ? "chat.modelProviderChecking" : "chat.modelProviderRequiredTitle")} embeddingReady={modelGuard.embeddingReady} multimodalEmbeddingReady={modelGuard.multimodalEmbeddingReady} rerankReady={modelGuard.rerankReady} value={prompt} onChange={setPrompt} isChatContent={false} showHistoryButton={false} showHistoryList={false} showPromptSuggestions={false} chatConfig={chatConfig} setChatConfig={setChatConfig} placeholder={t("conversationOrganizer.composerPlaceholder", { name: group.name })} setIsChatContent={value => { if (value) startChat(); }} /></div>
    <section className="conversation-group-page-list">
      <div className="conversation-group-page-list-heading">{t("conversationOrganizer.groupConversations")}<span>{group.member_count}</span></div>
      {conversations.length === 0 ? <div className="conversation-group-page-empty"><Empty description={t("conversationOrganizer.empty")} /><Button type="primary" icon={<PlusOutlined />} onClick={startChat}>{t("conversationOrganizer.startFirst")}</Button></div> : conversations.map((conversation) => <div className="conversation-group-page-item" key={conversation.conversation_id} draggable onDragStart={e => startConversationDrag(e, conversation.conversation_id, group.id)}>
        <button onClick={() => navigate(getChatConversationPath(conversation.conversation_id))}><MessageOutlined /><span>{conversation.display_name || conversation.conversation_id}<small>{conversation.summary}</small></span></button>
        <time>{conversation.updated_at ? new Date(conversation.updated_at).toLocaleDateString() : ""}</time>
        <ConversationMembership pinned={Boolean(conversation.pinned_at)} conversationId={conversation.conversation_id} groupId={group.id} title={conversation.display_name} />
      </div>)}
      {nextPageToken && <Button block loading={loading} onClick={() => void load(true, nextPageToken)}>{t("conversationOrganizer.loadMore")}</Button>}
    </section>
    <Modal open={editing} title={t("conversationOrganizer.editGroup")} okText={t("conversationOrganizer.save")} cancelText={t("common.cancel")} onOk={() => void save()} onCancel={() => setEditing(false)}>
      <Form form={form} layout="vertical"><GroupFields nameDisabled={namesLocked} scopeHint={t(namesLocked ? "conversationOrganizer.namesLocked" : "conversationOrganizer.scopeEditHint")} /></Form>
    </Modal>
  </main>;
}
