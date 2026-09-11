import { FolderOpenOutlined, PlusOutlined, EllipsisOutlined, PushpinOutlined } from "@ant-design/icons";
import { Button, Dropdown } from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import type { ConversationGroupMember } from "@/api/generated/core-client";
import { getChatConversationPath } from "@/modules/chat/constants/chat";
import { assignConversation, getConversationGroup, listConversationGroups, updateGroupPlacement, emitConversationGroupsChanged, type ConversationGroup } from "./api";
import ConversationMembership from "./ConversationMembership";
import { CONVERSATION_DRAG, GROUP_DRAG, readConversationDrag, startConversationDrag } from "./drag";

type Props = { namesLocked?: boolean; groups: ConversationGroup[]; searchText?: string; currentConversationId?: string; onNew?: (id: string) => void; onEdit: (group: ConversationGroup | "new") => void; onRemove: (group: ConversationGroup) => void };
export default function SidebarGroups({ groups, searchText = "", currentConversationId, onNew, onEdit, onRemove, namesLocked = false }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [members, setMembers] = useState<Record<string, ConversationGroupMember[]>>({});
  const pageCounts = useRef<Record<string, number>>({});
  useEffect(() => { pageCounts.current = {}; }, [searchText]);
  const [tokens, setTokens] = useState<Record<string, string>>({});
  const [matches, setMatches] = useState<Set<string> | null>(null);
  const [dropTarget, setDropTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    const matched = searchText ? await listConversationGroups(searchText) : groups;
    const details = await Promise.all(matched.map(async g => {
      const keyword = g.name.toLowerCase().includes(searchText.toLowerCase()) ? "" : searchText;
      const detail = await getConversationGroup(g.id, "", keyword);
      for (let page = 1; page < (pageCounts.current[g.id] || 1) && detail.nextPageToken; page++) {
        const next = await getConversationGroup(g.id, detail.nextPageToken, keyword);
        detail.conversations.push(...next.conversations);
        detail.nextPageToken = next.nextPageToken;
      }
      return [g.id, detail] as const;
    }));
    return { matched, details };
  }, [groups, searchText]);
  useEffect(() => {
    let disposed = false;
    const refresh = () => void load().then(({ matched, details }) => {
      if (disposed) return;
      setMatches(new Set(matched.map(g => g.id)));
      setMembers(Object.fromEntries(details.map(([id, detail]) => [id, detail.conversations])));
      setTokens(Object.fromEntries(details.map(([id, detail]) => [id, detail.nextPageToken])));
    }).catch(() => undefined);
    refresh();
    return () => { disposed = true; };
  }, [load]);
  const toggle = (setter: typeof setCollapsed, id: string) => setter(old => { const next = new Set(old); if (next.has(id)) next.delete(id); else next.add(id); return next; });
  const placement = async (id: string, input: { pinned?: boolean; before_group_id?: string }) => {
    setBusy(true);
    try { await updateGroupPlacement(id, input); emitConversationGroupsChanged(); } finally { setBusy(false); }
  };
  const more = async (g: ConversationGroup) => {
    if (expanded.has(g.id) && !tokens[g.id]) { toggle(setExpanded, g.id); return; }
    if (!expanded.has(g.id)) { toggle(setExpanded, g.id); return; }
    const detail = await getConversationGroup(g.id, tokens[g.id], g.name.toLowerCase().includes(searchText.toLowerCase()) ? "" : searchText);
    pageCounts.current[g.id] = (pageCounts.current[g.id] || 1) + 1;
    setMembers(old => ({ ...old, [g.id]: [...(old[g.id] || []), ...detail.conversations] }));
    setTokens(old => ({ ...old, [g.id]: detail.nextPageToken }));
  };
  const block = (g: ConversationGroup) => {
    const all = (members[g.id] || []).filter(c => !c.pinned_at);
    const open = Boolean(searchText) || !collapsed.has(g.id);
    const visible = searchText || expanded.has(g.id) ? all : all.slice(0, 5);
    const selected = location.pathname.endsWith(`/groups/${g.id}`);
    return <div key={g.id} className={`conversation-group ${dropTarget === g.id ? "group-drop-target" : ""}`} onDragOver={e => { if (!searchText && !busy && (e.dataTransfer.types.includes(CONVERSATION_DRAG) || e.dataTransfer.types.includes(GROUP_DRAG))) { e.preventDefault(); setDropTarget(g.id); } }} onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDropTarget(""); }} onDrop={async e => {
      e.preventDefault(); e.stopPropagation(); setDropTarget(""); if (searchText || busy) return;
      const source = e.dataTransfer.getData(GROUP_DRAG), conversation = readConversationDrag(e);
      try {
        if (source && source !== g.id) {
          const bucket = groups.filter(group => Boolean(group.pinned) === Boolean(g.pinned) && group.id !== source);
          const row = e.currentTarget.querySelector(".conversation-group-row")!.getBoundingClientRect();
          const after = e.clientY > row.top + row.height / 2;
          const anchor = after ? bucket[bucket.findIndex(group => group.id === g.id) + 1]?.id || "" : g.id;
          await placement(source, { pinned: g.pinned, before_group_id: anchor });
        }
        else if (conversation && conversation.groupId !== g.id) { await assignConversation(g.id, conversation.id); emitConversationGroupsChanged(); }
      } catch { /* The shared request interceptor displays the API error. */ }
    }}>
      <div className={`conversation-group-row ${selected ? "is-selected" : ""}`} draggable={!searchText && !busy} onDragStart={e => { e.dataTransfer.setData(GROUP_DRAG, g.id); e.dataTransfer.effectAllowed = "move"; }}>
        <button className="conversation-group-name" title={g.name} aria-expanded={open} onClick={() => toggle(setCollapsed, g.id)}><FolderOpenOutlined /><span>{g.name}</span></button>
        <Button type="text" size="small" icon={<PlusOutlined />} aria-label={t("conversationOrganizer.newChatInNamedGroup", { name: g.name })} onClick={() => onNew?.(g.id)} />
        <Dropdown trigger={["click"]} menu={{ items: [
          { key: "open", label: t("conversationOrganizer.openGroup"), onClick: () => navigate(`/agent/chat/groups/${g.id}`) },
          { key: "pin", icon: <PushpinOutlined />, label: t(g.pinned ? "conversationOrganizer.unpinGroup" : "conversationOrganizer.pinGroup"), onClick: () => void placement(g.id, { pinned: !g.pinned }) },
          { key: "edit", label: t("conversationOrganizer.editGroup"), onClick: () => onEdit(g) },
          { key: "remove", danger: true, label: t("conversationOrganizer.removeGroup"), onClick: () => onRemove(g) },
        ] }}><Button type="text" size="small" icon={<EllipsisOutlined />} aria-label={t("conversationOrganizer.groupMore", { name: g.name })} /></Dropdown>
      </div>
      {open && <div className="conversation-group-members">
        {visible.map(c => <div key={c.conversation_id} className={`conversation-group-member ${c.conversation_id === currentConversationId ? "active" : ""}`} draggable={!searchText} onDragStart={e => startConversationDrag(e, c.conversation_id, g.id)}>
          <button title={c.display_name} onClick={() => navigate(getChatConversationPath(c.conversation_id))}>{c.display_name || c.conversation_id}</button><ConversationMembership pinned={Boolean(c.pinned_at)} conversationId={c.conversation_id} groupId={g.id} title={c.display_name} />
        </div>)}
        {(all.length > 5 || tokens[g.id]) && <Button type="link" size="small" className="conversation-group-more" onClick={() => void more(g)}>{t(expanded.has(g.id) && !tokens[g.id] ? "conversationOrganizer.showLess" : "conversationOrganizer.showMore")}</Button>}
      </div>}
    </div>;
  };
  const visibleGroups = groups.filter(g => !matches || matches.has(g.id));
  return <>
    {visibleGroups.some(g => g.pinned) && <><div className="conversation-groups-heading">{t("conversationOrganizer.pinnedGroups")}</div>{visibleGroups.filter(g => g.pinned).map(block)}</>}
    <div className="conversation-groups-heading"><span>{t("conversationOrganizer.groups")}</span><Button type="text" size="small" icon={<PlusOutlined />} disabled={namesLocked} title={namesLocked ? t("conversationOrganizer.namesLocked") : undefined} aria-label={t("conversationOrganizer.newGroup")} onClick={() => onEdit("new")} /></div>
    {visibleGroups.filter(g => !g.pinned).map(block)}
    {!visibleGroups.length && <div className="conversation-group-empty">{t(searchText ? "conversationOrganizer.noSearchResults" : "conversationOrganizer.emptyGroups")}</div>}
  </>;
}
