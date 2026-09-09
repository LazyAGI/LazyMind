import { Form, Modal, Select } from "antd";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { assignConversation, createConversationGroup, emitConversationGroupsChanged, listConversationGroups, removeConversation, type ConversationGroup } from "./api";
import GroupFields, { normalizeGroupValues, type GroupValues } from "./GroupFields";

export type MembershipConversation = { conversationId: string; groupId?: string | null; title?: string };
export default function ConversationMembershipModal({ conversation, onClose }: {
  conversation: MembershipConversation | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<ConversationGroup[]>([]);
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState("free");
  const [form] = Form.useForm<GroupValues>();
  const id = conversation?.conversationId;
  const groupId = conversation?.groupId;
  useEffect(() => {
    if (!id) return;
    let disposed = false;
    setTarget(groupId || "free");
    form.resetFields();
    void listConversationGroups().then(next => { if (!disposed) setGroups(next); }).catch(() => undefined);
    return () => { disposed = true; };
  }, [id, groupId, form]);

  const save = async () => {
    if (!conversation) return;
    const values = target === "new" ? normalizeGroupValues(await form.validateFields()) : null;
    setBusy(true);
    try {
      let destination = target;
      if (values) {
        const created = await createConversationGroup(values);
        destination = created.id;
        setGroups(current => [...current, created]);
        setTarget(destination);
      }
      if (destination === "free") {
        if (groupId) await removeConversation(groupId, conversation.conversationId);
      } else if (destination !== groupId) {
        await assignConversation(destination, conversation.conversationId);
      }
      emitConversationGroupsChanged();
      onClose();
    } finally { setBusy(false); }
  };

  return <Modal open={Boolean(conversation)} title={t("conversationOrganizer.adjustMembership")} onCancel={() => !busy && onClose()} onOk={save} confirmLoading={busy} okText={t("conversationOrganizer.save")} cancelText={t("common.cancel")}>
    <p className="membership-conversation-title">{conversation?.title}</p>
    <Select style={{ width: "100%" }} showSearch optionFilterProp="label" aria-label={t("conversationOrganizer.groupPickerLabel")} value={target} onChange={setTarget} disabled={busy} options={[
      { value: "free", label: t("conversationOrganizer.keepFree") },
      ...groups.map(group => ({ value: group.id, label: group.name })),
      { value: "new", label: t("conversationOrganizer.newAndMove") },
    ]} />
    <Form form={form} layout="vertical" style={target === "new" ? { marginTop: 18 } : undefined}>{target === "new" && <GroupFields />}</Form>
  </Modal>;
}
