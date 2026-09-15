import { Button, Input, Spin } from "antd";
import { CheckOutlined } from "@ant-design/icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { assignConversation, emitConversationGroupsChanged, listConversationGroups, removeConversation, type ConversationGroup } from "./api";
import type { MembershipConversation } from "./ConversationMembershipModal";
import "./ConversationGroupPicker.scss";
import useOrganizerNameLock from "./useOrganizerNameLock";

export default function ConversationGroupPicker({ conversation, onCreate }: {
  conversation: MembershipConversation;
  onCreate: () => void;
}) {
  const { t } = useTranslation();
  const namesLocked = useOrganizerNameLock();
  const [groups, setGroups] = useState<ConversationGroup[]>([]);
  const [keyword, setKeyword] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  useEffect(() => {
    let disposed = false;
    void listConversationGroups().then(items => { if (!disposed) setGroups(items); })
      .catch(() => undefined).finally(() => { if (!disposed) setLoading(false); });
    return () => { disposed = true; };
  }, []);
  const move = async (groupId?: string) => {
    if (pending.current || groupId === conversation.groupId) return;
    pending.current = true;
    setBusy(true);
    try {
      if (groupId) await assignConversation(groupId, conversation.conversationId);
      else if (conversation.groupId) await removeConversation(conversation.groupId, conversation.conversationId);
      emitConversationGroupsChanged();
    } catch { /* Request interceptor displays the server error. */ }
    finally { pending.current = false; setBusy(false); }
  };
  const filtered = groups.filter(group => group.name.toLocaleLowerCase().includes(keyword.trim().toLocaleLowerCase()));
  return <div className="conversation-group-picker" onKeyDown={event => { if (event.key !== "Escape") event.stopPropagation(); }}>
    <div className="conversation-group-picker-search" onClick={event => event.stopPropagation()}>
      <Input allowClear placeholder={t("conversationOrganizer.searchGroups")} aria-label={t("conversationOrganizer.searchGroups")} value={keyword} onChange={event => setKeyword(event.target.value)} />
    </div>
    <Spin spinning={loading}>
      <div className="conversation-group-picker-list" role="group" aria-label={t("conversationOrganizer.groupPickerLabel")}>
        {filtered.map(group => <Button key={group.id} type="text" title={group.name} disabled={busy || group.id === conversation.groupId} onClick={() => void move(group.id)}>
          <span>{group.name}</span>{group.id === conversation.groupId && <CheckOutlined />}
        </Button>)}
        {!loading && !filtered.length && <div className="conversation-group-picker-empty">{t("settingsPage.recovery.noResults")}</div>}
      </div>
    </Spin>
    <div className="conversation-group-picker-footer">
      {conversation.groupId && <Button type="text" disabled={busy} onClick={() => void move()}>{t("conversationOrganizer.freeConversation")}</Button>}
      <Button type="text" disabled={busy || namesLocked} title={namesLocked ? t("conversationOrganizer.namesLocked") : undefined} onClick={onCreate}>{t("conversationOrganizer.newAndMoveEllipsis")}</Button>
    </div>
  </div>;
}

export function conversationGroupSubmenu(conversation: MembershipConversation, onCreate: () => void) {
  return [{ key: "group-picker", style: { height: "auto", padding: 0, cursor: "default" }, label: <ConversationGroupPicker conversation={conversation} onCreate={onCreate} /> }];
}
