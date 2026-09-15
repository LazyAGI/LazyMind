import { Form, Modal } from "antd";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { assignConversation, createConversationGroup, emitConversationGroupsChanged } from "./api";
import GroupFields, { normalizeGroupValues, type GroupValues } from "./GroupFields";

import useOrganizerNameLock from "./useOrganizerNameLock";

export type MembershipConversation = { conversationId: string; groupId?: string | null; title?: string };
export default function ConversationMembershipModal({ conversation, onClose }: {
  conversation: MembershipConversation | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [createdGroupId, setCreatedGroupId] = useState<string | null>(null);
  const [form] = Form.useForm<GroupValues>();
  const id = conversation?.conversationId;
  const namesLocked = useOrganizerNameLock(Boolean(id));
  useEffect(() => {
    if (!id) return;
    setCreatedGroupId(null);
    form.resetFields();
  }, [id, form]);

  const save = async () => {
    if (!conversation || busy || (namesLocked && !createdGroupId)) return;
    const values = normalizeGroupValues(await form.validateFields());
    setBusy(true);
    try {
      let destination = createdGroupId;
      if (!destination) {
        const created = await createConversationGroup(values);
        destination = created.id;
        setCreatedGroupId(destination);
        emitConversationGroupsChanged();
      }
      await assignConversation(destination, conversation.conversationId);
      emitConversationGroupsChanged();
      onClose();
    } finally { setBusy(false); }
  };

  return <Modal open={Boolean(conversation)} title={t("conversationOrganizer.newAndMove")} onCancel={() => !busy && onClose()} onOk={save} okButtonProps={{ disabled: namesLocked && !createdGroupId }} confirmLoading={busy} okText={t("conversationOrganizer.save")} cancelText={t("common.cancel")}>
    <p className="membership-conversation-title">{conversation?.title}</p>
    <Form form={form} layout="vertical" disabled={busy || Boolean(createdGroupId)}><GroupFields nameDisabled={namesLocked} scopeHint={namesLocked ? t("conversationOrganizer.namesLocked") : undefined} /></Form>
  </Modal>;
}
