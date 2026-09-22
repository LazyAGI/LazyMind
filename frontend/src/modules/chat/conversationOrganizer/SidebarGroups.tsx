import ConversationPreview from "../components/ConversationPreview";
import { MessageOutlined, FolderOpenOutlined, PlusOutlined, EllipsisOutlined, PushpinOutlined, LoadingOutlined, HolderOutlined } from "@ant-design/icons";
import { Button, Checkbox, Dropdown } from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import type { ConversationGroupMember } from "@/api/generated/core-client";
import { getChatConversationPath } from "@/modules/chat/constants/chat";
import { assignConversation, getConversationGroup, listConversationGroups, updateGroupPlacement, emitConversationGroupsChanged, type ConversationGroup } from "./api";
import ConversationMembership from "./ConversationMembership";
import ConversationTitleEditor from "../components/ConversationTitleEditor";
import ConversationGroupDropZone from "./ConversationGroupDropZone";
import { CONVERSATION_DRAG, GROUP_DRAG, readConversationDrag, startConversationDrag } from "./drag";

export type GroupBatchSelection = {
  checkedIds: string[];
  onToggle: (id: string, checked: boolean) => void;
  onToggleMany: (ids: string[], checked: boolean) => void;
  onMembersChange: (members: ConversationGroupMember[]) => void;
};
type Props = { batchSelection?: GroupBatchSelection; namesLocked?: boolean; groups: ConversationGroup[]; searchText?: string; currentConversationId?: string; onNew?: (id: string) => void; onEdit: (group: ConversationGroup | "new" | "new-project") => void; onRemove: (group: ConversationGroup) => void };
export default function SidebarGroups({ groups, searchText = "", currentConversationId, onNew, onEdit, onRemove, namesLocked = false, batchSelection }: Props) {
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
  const [memberDropTarget, setMemberDropTarget] = useState<{ id: string; position: 'before' | 'after' } | null>(null);
  const [busy, setBusy] = useState(false);
  const [selectingGroup, setSelectingGroup] = useState('');
  const selectionVersion = useRef(0);
  const onMembersChange = batchSelection?.onMembersChange;
  useEffect(() => {
    onMembersChange?.(groups.filter(g => !matches || matches.has(g.id)).flatMap(g =>
      (members[g.id] || []).filter(c => !c.pinned_at),
    ));
  }, [groups, matches, members, onMembersChange]);
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
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const batchMode = Boolean(batchSelection);
  useEffect(() => {
    setSelectingGroup('');
    return () => { selectionVersion.current++; };
  }, [load, batchMode, batchSelection?.checkedIds]);
  const toggle = (setter: typeof setCollapsed, id: string) => setter(old => { const next = new Set(old); if (next.has(id)) next.delete(id); else next.add(id); return next; });
  const placement = async (id: string, input: { pinned?: boolean; before_group_id?: string }) => {
    setBusy(true);
    try { await updateGroupPlacement(id, input); emitConversationGroupsChanged(); } finally { setBusy(false); }
  };
  const more = async (g: ConversationGroup) => {
    if (!batchSelection && expanded.has(g.id) && !tokens[g.id]) { toggle(setExpanded, g.id); return; }
    if (!batchSelection && !expanded.has(g.id)) { toggle(setExpanded, g.id); return; }
    const detail = await getConversationGroup(g.id, tokens[g.id], g.name.toLowerCase().includes(searchText.toLowerCase()) ? "" : searchText);
    pageCounts.current[g.id] = (pageCounts.current[g.id] || 1) + 1;
    setMembers(old => ({ ...old, [g.id]: [...(old[g.id] || []), ...detail.conversations] }));
    setTokens(old => ({ ...old, [g.id]: detail.nextPageToken }));
  };
  const moveMember = async (groupId: string, id: string, target?: { target_conversation_id: string; position: 'before' | 'after' }) => {
    setBusy(true);
    try {
      if (target) await assignConversation(groupId, id, target);
      else await assignConversation(groupId, id);
      emitConversationGroupsChanged();
    } catch { /* The shared request interceptor displays the API error. */ }
    finally { setBusy(false); }
  };
  const selectGroup = async (g: ConversationGroup, checked: boolean) => {
    if (!batchSelection || selectingGroup) return;
    const version = selectionVersion.current;
    const conversations = [...(members[g.id] || [])];
    let token = tokens[g.id];
    let pages = pageCounts.current[g.id] || 1;
    setSelectingGroup(g.id);
    try {
      while (checked && token) {
        const detail = await getConversationGroup(g.id, token, g.name.toLowerCase().includes(searchText.toLowerCase()) ? '' : searchText);
        if (version !== selectionVersion.current) return;
        conversations.push(...detail.conversations);
        token = detail.nextPageToken;
        pages++;
      }
      pageCounts.current[g.id] = pages;
      setMembers(old => ({ ...old, [g.id]: conversations }));
      setTokens(old => ({ ...old, [g.id]: token }));
      batchSelection.onToggleMany(conversations.filter(c => !c.pinned_at).map(c => c.conversation_id), checked);
    } catch { /* The shared request interceptor displays the API error; keep selection unchanged. */ }
    finally { if (version === selectionVersion.current) setSelectingGroup(''); }
  };
  const block = (g: ConversationGroup) => {
    const all = (members[g.id] || []).filter(c => !c.pinned_at);
    const selectedCount = all.filter(c => batchSelection?.checkedIds.includes(c.conversation_id)).length;
    const allSelected = all.length > 0 && selectedCount === all.length && !tokens[g.id];
    const open = Boolean(searchText) || !collapsed.has(g.id);
    const visible = batchSelection || searchText || expanded.has(g.id) ? all : all.slice(0, 5);
    const selected = location.pathname.endsWith(`/groups/${g.id}`);
    return <ConversationGroupDropZone key={g.id} groupId={g.id} groupName={g.name} disabled={g.kind === "project" || Boolean(batchSelection || searchText || busy)} className={`conversation-group ${dropTarget === g.id ? "group-drop-target" : ""}`} onDragOver={e => { if (!batchSelection && !searchText && !busy && ((g.kind !== "project" && e.dataTransfer.types.includes(CONVERSATION_DRAG)) || e.dataTransfer.types.includes(GROUP_DRAG))) { e.preventDefault(); setDropTarget(g.id); } }} onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDropTarget(""); }} onDrop={async e => {
      e.preventDefault(); e.stopPropagation(); setDropTarget(""); if (batchSelection || searchText || busy) return;
      const source = e.dataTransfer.getData(GROUP_DRAG), conversation = readConversationDrag(e);
      try {
        if (source && source !== g.id) {
          const bucket = groups.filter(group => Boolean(group.pinned) === Boolean(g.pinned) && group.id !== source);
          const row = e.currentTarget.querySelector(".conversation-group-row")!.getBoundingClientRect();
          const after = e.clientY > row.top + row.height / 2;
          const anchor = after ? bucket[bucket.findIndex(group => group.id === g.id) + 1]?.id || "" : g.id;
          await placement(source, { pinned: g.pinned, before_group_id: anchor });
        }
        else if (g.kind !== "project" && conversation && !groups.some(group => group.id === conversation.groupId && group.kind === "project") && conversation.groupId !== g.id) { await moveMember(g.id, conversation.id); }
      } catch { /* The shared request interceptor displays the API error. */ }
    }}>
      <div className={`conversation-group-row ${selected ? "is-selected" : ""}`} draggable={!batchSelection && !searchText && !busy} onDragStart={e => { e.dataTransfer.setData(GROUP_DRAG, g.id); e.dataTransfer.effectAllowed = "move"; }} onDragEnd={() => { setDropTarget(''); setMemberDropTarget(null); }}>
        {batchSelection && <Checkbox aria-label={t('conversationOrganizer.selectAllInGroup', { name: g.name })} title={t('conversationOrganizer.selectAllInGroup', { name: g.name })} checked={allSelected} indeterminate={selectedCount > 0 && !allSelected} disabled={Boolean(selectingGroup) || !members[g.id] || (!all.length && !tokens[g.id])} onChange={event => void selectGroup(g, event.target.checked)} />}
        <button className="conversation-group-name" title={g.path || g.name} aria-expanded={open} onClick={() => toggle(setCollapsed, g.id)}>{g.kind === "project" ? <FolderOpenOutlined /> : <MessageOutlined />}<span>{g.name}</span></button>
        {selectingGroup === g.id && <LoadingOutlined aria-label={t('common.loading')} />}
        {!batchSelection && <><Button type="text" size="small" icon={<PlusOutlined />} aria-label={t("conversationOrganizer.newChatInNamedGroup", { name: g.name })} onClick={() => onNew?.(g.id)} />
        <Dropdown trigger={["click"]} menu={{ items: [
          { key: "open", label: t(g.kind === "project" ? "conversationProject.open" : "conversationOrganizer.openGroup"), onClick: () => navigate(`/agent/chat/groups/${g.id}`) },
          { key: "pin", icon: <PushpinOutlined />, label: t(g.kind === "project" ? (g.pinned ? "conversationProject.unpin" : "conversationProject.pin") : g.pinned ? "conversationOrganizer.unpinGroup" : "conversationOrganizer.pinGroup"), onClick: () => void placement(g.id, { pinned: !g.pinned }) },
          { key: "edit", label: t(g.kind === "project" ? "conversationProject.edit" : "conversationOrganizer.editGroup"), onClick: () => onEdit(g) },
          { key: "remove", danger: true, label: t(g.kind === "project" ? "conversationProject.remove" : "conversationOrganizer.removeGroup"), onClick: () => onRemove(g) },
        ] }}><Button type="text" size="small" icon={<EllipsisOutlined />} aria-label={t("conversationOrganizer.groupMore", { name: g.name })} /></Dropdown></>}
      </div>
      {open && <div className="conversation-group-members">
        {visible.map(c => <ConversationPreview key={c.conversation_id} conversationId={c.conversation_id} title={c.display_name || c.conversation_id} summary={c.summary} updateTime={c.updated_at} isTask={Boolean(c.is_task_conv)} disabled={Boolean(batchSelection) || renamingId === c.conversation_id}><div className={`conversation-group-member ${c.conversation_id === currentConversationId ? "active" : ""} ${memberDropTarget?.id === c.conversation_id ? `member-drop-${memberDropTarget.position}` : ''}`} draggable={g.kind !== "project" && renamingId !== c.conversation_id && !batchSelection && !searchText && !busy}
          onDragStart={e => startConversationDrag(e, c.conversation_id, g.id)}
          onDragEnd={() => { setDropTarget(''); setMemberDropTarget(null); }}
          onDragOver={e => {
            if (g.kind === "project" || batchSelection || searchText || busy || !e.dataTransfer.types.includes(CONVERSATION_DRAG)) return;
            e.preventDefault(); e.stopPropagation(); setDropTarget('');
            const row = e.currentTarget.getBoundingClientRect();
            setMemberDropTarget({ id: c.conversation_id, position: e.clientY > row.top + row.height / 2 ? 'after' : 'before' });
          }}
          onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setMemberDropTarget(null); }}
          onDrop={e => {
            if (!e.dataTransfer.types.includes(CONVERSATION_DRAG)) return;
            e.preventDefault(); e.stopPropagation(); setMemberDropTarget(null); setDropTarget('');
            if (g.kind === "project" || batchSelection || searchText || busy) return;
            const source = readConversationDrag(e);
            if (!source || source.id === c.conversation_id) return;
            const row = e.currentTarget.getBoundingClientRect();
            void moveMember(g.id, source.id, { target_conversation_id: c.conversation_id, position: e.clientY > row.top + row.height / 2 ? 'after' : 'before' });
          }}>
          <>{batchSelection ? <Checkbox className="conversation-group-batch-checkbox" checked={batchSelection.checkedIds.includes(c.conversation_id)} onChange={event => batchSelection.onToggle(c.conversation_id, event.target.checked)}><span title={c.display_name}>{c.display_name || c.conversation_id}</span></Checkbox> : renamingId === c.conversation_id ? <ConversationTitleEditor key={c.conversation_id} conversationId={c.conversation_id} initialTitle={c.display_name} onClose={() => setRenamingId(null)} /> : <><button onClick={() => navigate(getChatConversationPath(c.conversation_id))}>{c.display_name || c.conversation_id}</button><ConversationMembership onRename={() => setRenamingId(c.conversation_id)} pinned={Boolean(c.pinned_at)} conversationId={c.conversation_id} groupId={g.id} groupKind={g.kind} title={c.display_name} /></>}</>
          {g.kind !== "project" && !batchSelection && !searchText && renamingId !== c.conversation_id && <span className="conversation-member-drag-handle" title={t('conversationOrganizer.memberDragHint')}><HolderOutlined /></span>}
        </div></ConversationPreview>)}
        {(batchSelection ? Boolean(tokens[g.id]) : all.length > 5 || tokens[g.id]) && <Button type="link" size="small" className="conversation-group-more" disabled={Boolean(selectingGroup)} onClick={() => void more(g)}>{t(!batchSelection && expanded.has(g.id) && !tokens[g.id] ? "conversationOrganizer.showLess" : "conversationOrganizer.showMore")}</Button>}
      </div>}
    </ConversationGroupDropZone>;
  };
  const visibleGroups = groups.filter(g => !matches || matches.has(g.id));
  return <>
    {visibleGroups.some(g => g.pinned) && <><div className="conversation-groups-heading">{t("conversationOrganizer.pinnedGroups")}</div>{visibleGroups.filter(g => g.pinned).map(block)}</>}
    <div className="conversation-groups-heading"><span>{t("conversationOrganizer.groups")}</span>{!batchSelection && <Dropdown trigger={["click"]} menu={{ items: [
      { key: "group", label: t("conversationOrganizer.newGroup"), disabled: namesLocked, onClick: () => onEdit("new") },
      { key: "project", label: t("conversationProject.new"), onClick: () => onEdit("new-project") },
    ] }}><Button type="text" size="small" icon={<PlusOutlined />} aria-label={t("conversationOrganizer.newGroup")} /></Dropdown>}</div>
    {visibleGroups.filter(g => !g.pinned).map(block)}
    {!visibleGroups.length && <div className="conversation-group-empty" title={t(searchText ? "conversationOrganizer.noSearchResults" : "conversationOrganizer.emptyGroups")}>{t(searchText ? "conversationOrganizer.noSearchResults" : "conversationOrganizer.emptyGroups")}</div>}
  </>;
}
