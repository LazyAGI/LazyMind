import { act, createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ConfigProvider } from "antd";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SidebarGroups, { type GroupBatchSelection } from "./SidebarGroups";
import * as api from "./api";
import { CONVERSATION_DRAG, GROUP_DRAG } from "./drag";
const reorder = vi.hoisted(() => vi.fn());
const selectionSnapshot = vi.hoisted(() => vi.fn());
const t = (key: string, values?: { name?: string }) => values?.name ? `${key} ${values.name}` : key;
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({ userId: "sidebar-owner" }) }, AUTH_USER_CHANGE_EVENT: "sidebar-user-change" }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceListConversations: selectionSnapshot, conversationServiceReorder: reorder, conversationServiceGetConversationDetail: vi.fn().mockResolvedValue({ data: { conversation: { display_name: "a对话0", title_revision: 1 } } }) }) }));
vi.mock("./api", () => ({ getConversationGroup: vi.fn(), listConversationGroups: vi.fn(), updateGroupPlacement: vi.fn(), assignConversation: vi.fn(), emitConversationGroupsChanged: vi.fn(), renameGroupConversation: vi.fn(), CONVERSATION_GROUPS_CHANGED_EVENT: "group-change" }));
const groups = [{ id: "a", name: "旅行", pinned: false }, { id: "b", name: "学习", pinned: false }] as api.ConversationGroup[];
function renderGroups(searchText = "") { return render(<MemoryRouter><SidebarGroups groups={groups} searchText={searchText} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>); }
function transfer(type: string, payload: string) { return { types: [type], getData: (key: string) => key === type ? payload : "" }; }
const member = (id: string) => ({ conversation_id: id, display_name: id, pinned_at: '', membership_revision: 1 });
function pendingPage() {
  let resolve!: (value: Awaited<ReturnType<typeof api.getConversationGroup>>) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<Awaited<ReturnType<typeof api.getConversationGroup>>>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
function BatchGroups() {
  const [checkedIds, setCheckedIds] = useState(['b-0']);
  const onToggleMany: GroupBatchSelection['onToggleMany'] = (ids, checked) => setCheckedIds(old => checked ? [...new Set([...old, ...ids])] : old.filter(id => !ids.includes(id)));
  return <SidebarGroups groups={groups} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={{ checkedIds, onToggle: (id, checked) => onToggleMany([id], checked), onToggleMany }} />;
}
beforeEach(() => {
  vi.clearAllMocks();
  reorder.mockResolvedValue({ data: {} });
  selectionSnapshot.mockReset().mockImplementation(async (_query, options) => ({ data: { selection_complete: true, conversations: Array.from({ length: 6 }, (_, i) => member(`${options.params.selection_group_id}-${i}`)) } }));
  localStorage.clear();
  vi.mocked(api.updateGroupPlacement).mockImplementation(async (id, input) => groups.map(group => group.id === id ? { ...group, ...input } : group));
  vi.mocked(api.getConversationGroup).mockImplementation(async id => ({ group: groups.find(g => g.id === id)!, conversations: Array.from({ length: 6 }, (_, i) => ({ conversation_id: `${id}-${i}`, display_name: `${id}对话${i}`, pinned_at: "", membership_revision: 1 })), nextPageToken: "", total: 6 }));
  vi.mocked(api.listConversationGroups).mockResolvedValue(groups);
});
afterEach(() => vi.restoreAllMocks());
describe("group sidebar", () => {
  it.each([false, true])("shows each member's update date without inventing missing dates (batch: %s)", async (batch) => {
    vi.mocked(api.getConversationGroup).mockResolvedValue({
      group: groups[0],
      conversations: [
        { conversation_id: "dated", display_name: "有时间", membership_revision: 1, updated_at: "2026-09-29T03:13:00Z" },
        { conversation_id: "missing", display_name: "无时间", membership_revision: 1 },
        { conversation_id: "invalid", display_name: "无效时间", membership_revision: 1, updated_at: "invalid" },
      ],
      nextPageToken: "",
      total: 3,
    });
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={batch ? { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() } : undefined} /></MemoryRouter>);
    const dated = (await screen.findByText("有时间")).closest(".conversation-group-member")!;
    expect(dated.querySelector("time")).toHaveTextContent("09/29");
    expect(dated.querySelector("time")).toHaveAttribute("datetime", "2026-09-29T03:13:00Z");
    for (const title of ["无时间", "无效时间"]) {
      expect(screen.getByText(title).closest(".conversation-group-member")!.querySelector("time")).toBeNull();
    }
  });

  it("reads collapse state from the backend instead of legacy browser storage", async () => {
    localStorage.setItem("lazymind:collapsed-conversation-groups:sidebar-owner", '["b"]');
    render(<MemoryRouter><SidebarGroups groups={[{ ...groups[0], collapsed: true }, { ...groups[1], collapsed: false }]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    expect(await screen.findByTitle("旅行")).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByTitle("学习")).toHaveAttribute("aria-expanded", "true");
    expect(screen.queryByText("a对话0")).not.toBeInTheDocument();
  });

  it("saves both collapse and expansion through the placement API", async () => {
    const view = renderGroups();
    fireEvent.click(await screen.findByTitle("旅行"));
    await waitFor(() => expect(api.updateGroupPlacement).toHaveBeenCalledWith("a", { collapsed: true }));
    await waitFor(() => expect(screen.getByTitle("旅行")).toHaveAttribute("aria-expanded", "false"));
    expect(api.emitConversationGroupsChanged).toHaveBeenCalled();
    expect(localStorage.getItem("lazymind:collapsed-conversation-groups:sidebar-owner")).toBeNull();
    view.unmount();
    render(<MemoryRouter><SidebarGroups groups={[{ ...groups[0], collapsed: true }]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    fireEvent.click(await screen.findByTitle("旅行"));
    await waitFor(() => expect(api.updateGroupPlacement).toHaveBeenLastCalledWith("a", { collapsed: false }));
    await waitFor(() => expect(screen.getByTitle("旅行")).toHaveAttribute("aria-expanded", "true"));
  });

  it("keeps the saved state on failure and allows retry without duplicate in-flight requests", async () => {
    let reject!: (reason: Error) => void;
    vi.mocked(api.updateGroupPlacement).mockReturnValueOnce(new Promise((_, fail) => { reject = fail; }));
    renderGroups();
    const button = await screen.findByTitle("旅行");
    fireEvent.click(button);
    fireEvent.click(button);
    expect(api.updateGroupPlacement).toHaveBeenCalledTimes(1);
    expect(button).toBeDisabled();
    await act(async () => reject(new Error("Request failed")));
    expect(button).toHaveAttribute("aria-expanded", "true");
    expect(button).toBeEnabled();
    expect(api.emitConversationGroupsChanged).not.toHaveBeenCalled();
    fireEvent.click(button);
    await waitFor(() => expect(button).toHaveAttribute("aria-expanded", "false"));
  });

  it("applies refreshed server state and temporarily expands search results without saving", async () => {
    const renderSidebar = (collapsed: boolean, searchText = "") => <MemoryRouter><SidebarGroups groups={[{ ...groups[0], collapsed }]} searchText={searchText} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>;
    const view = render(renderSidebar(false));
    expect(await screen.findByTitle("旅行")).toHaveAttribute("aria-expanded", "true");
    view.rerender(renderSidebar(true));
    expect(screen.getByTitle("旅行")).toHaveAttribute("aria-expanded", "false");
    view.rerender(renderSidebar(true, "旅行"));
    await waitFor(() => expect(screen.getByTitle("旅行")).toHaveAttribute("aria-expanded", "true"));
    view.rerender(renderSidebar(true));
    expect(screen.getByTitle("旅行")).toHaveAttribute("aria-expanded", "false");
    expect(api.updateGroupPlacement).not.toHaveBeenCalled();
  });

  it.each([false, true])("saves ordinary group pin changes from pinned=%s", async pinned => {
    render(<MemoryRouter><SidebarGroups groups={[{ ...groups[0], pinned }]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByTitle("旅行");
    expect(Boolean(screen.queryByText("conversationOrganizer.pinnedGroups"))).toBe(pinned);
    fireEvent.click(screen.getByRole("button", { name: "conversationOrganizer.groupMore 旅行" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: pinned ? /conversationOrganizer\.unpinGroup/ : /conversationOrganizer\.pinGroup/ }));
    await waitFor(() => expect(api.updateGroupPlacement).toHaveBeenCalledWith("a", { pinned: !pinned }));
    expect(api.emitConversationGroupsChanged).toHaveBeenCalled();
  });

  it("places pinned groups before ordinary groups and preserves their collapsed state", async () => {
    const view = render(<MemoryRouter><SidebarGroups groups={[groups[0], { ...groups[1], pinned: true, collapsed: true }]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByTitle("旅行");
    expect(Array.from(view.container.querySelectorAll(".conversation-group-name")).map(node => node.textContent)).toEqual(["学习", "旅行"]);
    expect(screen.getByTitle("学习")).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("b对话0")).not.toBeInTheDocument();
  });

  it("shows a project folder and rejects member drag operations", async () => {
    const project = { ...groups[0], kind: "project", path: "/work/project" } as api.ConversationGroup;
    render(<MemoryRouter><SidebarGroups groups={[project]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    const member = await screen.findByText("a对话0");
    const projectButton = screen.getByTitle("/work/project");
    expect(projectButton.querySelector('[data-icon="folder-open"]')).not.toBeNull();
    expect(member.closest(".conversation-group-member")).toHaveAttribute("draggable", "false");
    fireEvent.drop(projectButton.closest(".conversation-group")!, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: "free-chat" })) });
    expect(api.assignConversation).not.toHaveBeenCalled();
  });

  it.each(["group", "project"])("opens the rename dialog from a %s member's context menu", async kind => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><SidebarGroups groups={[{ ...groups[0], kind }]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter></ConfigProvider>);
    const title = await screen.findByRole("button", { name: "a对话0", exact: true });
    fireEvent.contextMenu(title.closest(".conversation-group-member")!);
    fireEvent.click(await screen.findByRole("menuitem", { name: "conversationOrganizer.renameConversation" }));
    const input = await screen.findByRole("textbox", { name: "conversationRename.name" });
    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
    expect(input.closest(".conversation-group-member")).toBeNull();
    expect(title.closest(".conversation-group-member")).toHaveAttribute("draggable", "false");
    expect(title).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(input).toHaveValue("a对话0");
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.getByRole("button", { name: "a对话0", exact: true })).toBeInTheDocument();
  });

  it('selects and clears only one group, including its remaining pages', async () => {
    const member = (id: string) => ({ conversation_id: id, display_name: id, pinned_at: '', membership_revision: 1 });
    vi.mocked(api.getConversationGroup).mockImplementation(async (id, token) => ({ group: groups.find(g => g.id === id)!, conversations: id === 'b' ? [member('b-0')] : token ? [member('a-1')] : [member('a-0')], nextPageToken: id === 'a' && !token ? 'next' : '' }));
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [member('a-0'), member('a-1')] } });
    render(<MemoryRouter><BatchGroups /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'a-0' });
    const local = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(local);
    await waitFor(() => expect(local).toBeChecked());
    expect(selectionSnapshot).toHaveBeenCalledWith({ keyword: '' }, { params: { is_task_conv: 'false', selection_snapshot: true, selection_group_id: 'a' } });
    fireEvent.click(screen.getByRole('button', { name: 'conversationOrganizer.showMore' }));
    await screen.findByRole('checkbox', { name: 'a-1' });
    expect(screen.getByRole('checkbox', { name: 'a-1' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'b-0' })).toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: 'a-0' }));
    expect(local).toBePartiallyChecked();
    fireEvent.click(local);
    await waitFor(() => expect(local).toBeChecked());
    fireEvent.click(screen.getByTitle('旅行'));
    await waitFor(() => expect(screen.getByTitle('旅行')).toHaveAttribute('aria-expanded', 'false'));
    await waitFor(() => expect(local).toBeEnabled());
    fireEvent.click(local);
    await waitFor(() => expect(local).not.toBeChecked());
    fireEvent.click(screen.getByTitle('旅行'));
    expect(await screen.findByRole('checkbox', { name: 'a-1' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'b-0' })).toBeChecked();
  });
  it.each([false, true])('uses complete matching IDs to clear a collapsed paginated group after global selection (recent unchecked: %s)', async uncheckRecent => {
    const groupIds = Array.from({ length: 52 }, (_, i) => `group-${i}`);
    const stableGroups = [{ ...groups[0], collapsed: true }];
    const matchingGroupMemberIds = { a: groupIds };
    const toggleMany = vi.fn();
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [...groupIds.map(member), { ...member('pinned'), pinned_at: '2026-10-20T00:00:00Z' }] } });
    vi.mocked(api.getConversationGroup).mockResolvedValue({
      group: stableGroups[0], conversations: [{ ...member('pinned'), pinned_at: '2026-10-20T00:00:00Z' }, ...groupIds.slice(0, 49).map(member)], nextPageToken: 'next',
    });
    function CompleteBatch() {
      const [checkedIds, setCheckedIds] = useState([...groupIds, 'pinned', 'recent']);
      return <>
        <output data-testid="selected-ids">{JSON.stringify(checkedIds)}</output>
        <button onClick={() => setCheckedIds(ids => ids.filter(id => id !== 'recent'))}>Uncheck recent</button>
        <button onClick={() => setCheckedIds(ids => ids.filter(id => id !== 'group-51'))}>Uncheck unloaded member</button>
        <SidebarGroups groups={stableGroups} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={{ checkedIds, matchingGroupMemberIds, onToggle: vi.fn(), onToggleMany: (ids, checked) => {
          toggleMany(ids, checked);
          setCheckedIds(previous => checked ? [...new Set([...previous, ...ids])] : previous.filter(id => !ids.includes(id)));
        } }} />
      </>;
    }
    render(<MemoryRouter><CompleteBatch /></MemoryRouter>);
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    await waitFor(() => expect(select).toBeEnabled());
    await waitFor(() => expect(select).toBeChecked());
    expect(select).not.toBePartiallyChecked();
    expect(screen.queryByRole('checkbox', { name: 'group-0' })).not.toBeInTheDocument();
    if (uncheckRecent) fireEvent.click(screen.getByRole('button', { name: 'Uncheck recent' }));
    await waitFor(() => expect(select).toBeChecked());
    fireEvent.click(select);
    await waitFor(() => expect(toggleMany).toHaveBeenLastCalledWith(groupIds, false));
    expect(JSON.parse(screen.getByTestId('selected-ids').textContent || '[]')).toEqual(uncheckRecent ? ['pinned'] : ['pinned', 'recent']);
    expect(select).not.toBeChecked();
    expect(select).not.toBePartiallyChecked();
    fireEvent.click(select);
    await waitFor(() => expect(toggleMany).toHaveBeenLastCalledWith(groupIds, true));
    await waitFor(() => expect(select).toBeChecked());
    fireEvent.click(screen.getByRole('button', { name: 'Uncheck unloaded member' }));
    expect(select).toBePartiallyChecked();
    fireEvent.click(select);
    await waitFor(() => expect(select).toBeChecked());
    expect(api.getConversationGroup).toHaveBeenCalledTimes(1);
  });
  it('treats a missing group in the complete matching map as empty', async () => {
    vi.mocked(api.getConversationGroup).mockResolvedValue({ group: groups[0], conversations: [member('first')], nextPageToken: 'next' });
    const batchSelection = { checkedIds: ['first'], matchingGroupMemberIds: {}, onToggle: vi.fn(), onToggleMany: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'first' });
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    expect(select).toBeDisabled();
    expect(select).not.toBeChecked();
    expect(select).not.toBePartiallyChecked();
  });
  it('uses a newer global snapshot after a group member disappears and cancels the group on the first click', async () => {
    const stableGroups = [{ ...groups[0], collapsed: true }];
    const toggleMany = vi.fn();
    vi.mocked(api.getConversationGroup).mockResolvedValue({ group: stableGroups[0], conversations: [member('first')], nextPageToken: 'next' });
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [member('first'), member('removed')] } });
    function Batch() {
      const [checkedIds, setCheckedIds] = useState<string[]>([]);
      const [matchingGroupMemberIds, setMatchingGroupMemberIds] = useState<GroupBatchSelection['matchingGroupMemberIds']>();
      return <>
        <output data-testid="selected-ids">{JSON.stringify(checkedIds)}</output>
        <button onClick={() => {
          setMatchingGroupMemberIds({ a: ['first'] });
          setCheckedIds(['first', 'pinned', 'recent']);
        }}>Apply global snapshot</button>
        <SidebarGroups groups={stableGroups} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={{ checkedIds, matchingGroupMemberIds, onToggle: vi.fn(), onToggleMany: (ids, checked) => {
          toggleMany(ids, checked);
          setCheckedIds(previous => checked ? [...new Set([...previous, ...ids])] : previous.filter(id => !ids.includes(id)));
        } }} />
      </>;
    }
    render(<MemoryRouter><Batch /></MemoryRouter>);
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    await waitFor(() => expect(select).toBeEnabled());
    fireEvent.click(select);
    await waitFor(() => expect(select).toBeChecked());
    fireEvent.click(screen.getByRole('button', { name: 'Apply global snapshot' }));
    expect(select).toBeChecked();
    expect(select).not.toBePartiallyChecked();
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [member('first'), { ...member('pinned'), pinned_at: '2026-10-20T00:00:00Z' }] } });
    fireEvent.click(select);
    await waitFor(() => expect(toggleMany).toHaveBeenLastCalledWith(['first'], false));
    expect(JSON.parse(screen.getByTestId('selected-ids').textContent || '[]')).toEqual(['pinned', 'recent']);
  });
  it('retains a group snapshot read after the latest global snapshot', async () => {
    const stableGroups = [groups[0]];
    const matchingGroupMemberIds = { a: ['first'] };
    const invalidate = vi.fn();
    vi.mocked(api.getConversationGroup).mockResolvedValue({ group: stableGroups[0], conversations: [member('first')], nextPageToken: 'next' });
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [member('first'), member('second')] } });
    function Batch() {
      const [checkedIds, setCheckedIds] = useState<string[]>([]);
      return <>
        <button onClick={() => setCheckedIds(ids => ids.filter(id => id !== 'second'))}>Uncheck new member</button>
        <SidebarGroups groups={stableGroups} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={{ checkedIds, matchingGroupMemberIds, onToggle: vi.fn(), onInvalidate: invalidate, onToggleMany: ids => setCheckedIds(ids) }} />
      </>;
    }
    render(<MemoryRouter><Batch /></MemoryRouter>);
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    await waitFor(() => expect(select).toBeEnabled());
    fireEvent.click(select);
    await waitFor(() => expect(select).toBeChecked());
    expect(invalidate).toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Uncheck new member' }));
    expect(select).toBePartiallyChecked();
  });
  it('ignores a group response superseded by a newer shared selection intent and allows retry', async () => {
    const page = pendingPage();
    let current = true;
    const beginSelection = vi.fn(() => () => current);
    selectionSnapshot.mockImplementationOnce(() => page.promise.then(result => ({ data: { selection_complete: true, conversations: result.conversations } })));
    vi.mocked(api.getConversationGroup).mockResolvedValue({ group: groups[0], conversations: [member('first')], nextPageToken: 'next' });
    const selection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn(), beginSelection };
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} batchSelection={selection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    await waitFor(() => expect(select).toBeEnabled());
    fireEvent.click(select);
    expect(beginSelection).toHaveBeenCalledTimes(1);
    current = false;
    await act(async () => page.resolve({ group: groups[0], conversations: [member('stale')], nextPageToken: '' }));
    expect(selection.onToggleMany).not.toHaveBeenCalled();
    expect(select).toBeEnabled();
    current = true;
    selectionSnapshot.mockResolvedValueOnce({ data: { selection_complete: true, conversations: [member('first')] } });
    fireEvent.click(select);
    await waitFor(() => expect(selection.onToggleMany).toHaveBeenCalledWith(['first'], true));
    expect(beginSelection).toHaveBeenCalledTimes(2);
  });
  it('ignores a pending group page after a complete matching map arrives', async () => {
    const page = pendingPage();
    selectionSnapshot.mockImplementation(() => page.promise.then(result => ({ data: { selection_complete: true, conversations: result.conversations } })));
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => token ? page.promise : {
      group: groups[0], conversations: [member('first')], nextPageToken: 'next',
    });
    const stableGroups = [groups[0]];
    const batchSelection = { checkedIds: ['first'], onToggle: vi.fn(), onToggleMany: vi.fn() };
    const view = (matchingGroupMemberIds?: GroupBatchSelection['matchingGroupMemberIds']) => <MemoryRouter><SidebarGroups groups={stableGroups} batchSelection={{ ...batchSelection, matchingGroupMemberIds }} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>;
    const { rerender } = render(view());
    await screen.findByRole('checkbox', { name: 'first' });
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(select);
    expect(select).toBeDisabled();
    rerender(view({ a: ['first'] }));
    await act(async () => page.resolve({ group: groups[0], conversations: [member('stale')], nextPageToken: '' }));
    expect(batchSelection.onToggleMany).not.toHaveBeenCalled();
    expect(select).toBeChecked();
    expect(select).toBeEnabled();
    expect(screen.queryByRole('checkbox', { name: 'stale' })).not.toBeInTheDocument();
  });
  it('serializes load-more requests and deduplicates overlapping pages using the server search keyword', async () => {
    const page = pendingPage();
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => token ? page.promise : {
      group: groups[0], conversations: [member('first'), member('first')], nextPageToken: 'next',
    });
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} searchText=" 旅行 " assistants="codex" batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'first' });
    const more = screen.getByRole('button', { name: 'conversationOrganizer.showMore' });
    fireEvent.click(more);
    fireEvent.click(more);
    expect(more).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' })).toBeDisabled();
    expect(vi.mocked(api.getConversationGroup).mock.calls.filter(call => call[1] === 'next')).toEqual([['a', 'next', '旅行', 'codex']]);
    await act(async () => page.resolve({ group: groups[0], conversations: [member('first'), member('second'), member('second')], nextPageToken: '' }));
    expect(screen.getAllByRole('checkbox', { name: 'first' })).toHaveLength(1);
    expect(screen.getAllByRole('checkbox', { name: 'second' })).toHaveLength(1);
    expect(api.getConversationGroup).toHaveBeenCalledWith('a', '', '旅行', 'codex');
  });
  it('loads the next search page on the first click and hides more when all results are shown', async () => {
    vi.mocked(api.listConversationGroups).mockResolvedValue([groups[0]]);
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => ({
      group: groups[0], conversations: token ? [member('last')] : Array.from({ length: 6 }, (_, i) => member(`first-${i}`)), nextPageToken: token ? '' : 'next',
    }));
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} searchText=" 旅行 " onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByRole('button', { name: 'first-5' });
    fireEvent.click(screen.getByRole('button', { name: 'conversationOrganizer.showMore' }));
    await screen.findByRole('button', { name: 'last' });
    expect(api.getConversationGroup).toHaveBeenCalledWith('a', 'next', '旅行', undefined);
    expect(screen.queryByRole('button', { name: 'conversationOrganizer.showMore' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'conversationOrganizer.showLess' })).not.toBeInTheDocument();
  });
  it.each(['keyword', 'source', 'mode'] as const)('ignores an old load-more response after the %s changes', async change => {
    const page = pendingPage();
    let changed = false;
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => token === 'old-next' ? page.promise : {
      group: groups[0], conversations: [member(changed ? 'fresh-first' : 'old-first')], nextPageToken: changed ? '' : 'old-next',
    });
    const stableGroups = [groups[0]];
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    const view = () => <MemoryRouter><SidebarGroups groups={stableGroups} searchText={changed && change === 'keyword' ? 'new' : 'old'} assistants={changed && change === 'source' ? 'workbuddy' : 'codex'} isTaskConv={changed && change === 'mode'} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>;
    const { rerender } = render(view());
    await screen.findByRole('checkbox', { name: 'old-first' });
    fireEvent.click(screen.getByRole('button', { name: 'conversationOrganizer.showMore' }));
    changed = true;
    rerender(view());
    await screen.findByRole('checkbox', { name: 'fresh-first' });
    await act(async () => page.resolve({ group: groups[0], conversations: [member('stale-next')], nextPageToken: 'stale-token' }));
    expect(screen.queryByRole('checkbox', { name: 'stale-next' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'conversationOrganizer.showMore' })).not.toBeInTheDocument();
  });
  it.each(['cancel', 'unmount'] as const)('ignores pending load-more results after batch %s', async action => {
    const page = pendingPage();
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => token ? page.promise : {
      group: groups[0], conversations: [member('first')], nextPageToken: 'next',
    });
    const stableGroups = [groups[0]];
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    const view = (batch: boolean) => <MemoryRouter><SidebarGroups groups={stableGroups} batchSelection={batch ? batchSelection : undefined} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>;
    const { rerender, unmount } = render(view(true));
    await screen.findByRole('checkbox', { name: 'first' });
    fireEvent.click(screen.getByRole('button', { name: 'conversationOrganizer.showMore' }));
    if (action === 'unmount') unmount();
    else rerender(view(false));
    await act(async () => page.resolve({ group: groups[0], conversations: [member('stale-next')], nextPageToken: '' }));
    expect(screen.queryByText('stale-next')).not.toBeInTheDocument();
  });
  it.each(['keyword', 'source', 'mode'] as const)('clears old members immediately when the %s changes and the new query fails', async change => {
    const page = pendingPage();
    let changed = false;
    vi.mocked(api.listConversationGroups).mockResolvedValue([groups[0]]);
    vi.mocked(api.getConversationGroup).mockImplementation(async () => changed ? page.promise : {
      group: groups[0], conversations: [member('old-first')], nextPageToken: 'old-next',
    });
    const stableGroups = [groups[0]];
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    const view = () => <MemoryRouter><SidebarGroups groups={stableGroups} searchText={changed && change === 'keyword' ? 'new' : 'old'} assistants={changed && change === 'source' ? 'workbuddy' : 'codex'} isTaskConv={changed && change === 'mode'} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>;
    const { rerender } = render(view());
    await screen.findByRole('checkbox', { name: 'old-first' });
    changed = true;
    rerender(view());
    expect(screen.queryByRole('checkbox', { name: 'old-first' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'conversationOrganizer.showMore' })).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' })).toBeDisabled();
    await act(async () => page.reject(new Error('new query unavailable')));
    expect(screen.queryByRole('checkbox', { name: 'old-first' })).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' })).toBeDisabled();
    expect(batchSelection.onToggleMany).not.toHaveBeenCalled();
  });
  it('retains the load-more cursor after failure and permits retry', async () => {
    vi.mocked(api.getConversationGroup).mockImplementation(async () => ({ group: groups[0], conversations: [member('first')], nextPageToken: 'next' }));
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'first' });
    vi.mocked(api.getConversationGroup).mockRejectedValueOnce(new Error('page unavailable'));
    const more = screen.getByRole('button', { name: 'conversationOrganizer.showMore' });
    fireEvent.click(more);
    await waitFor(() => expect(more).toBeEnabled());
    expect(screen.getByRole('checkbox', { name: 'first' })).toBeInTheDocument();
    vi.mocked(api.getConversationGroup).mockResolvedValueOnce({ group: groups[0], conversations: [member('second')], nextPageToken: '' });
    fireEvent.click(more);
    await screen.findByRole('checkbox', { name: 'second' });
    expect(vi.mocked(api.getConversationGroup).mock.calls.filter(call => call[1] === 'next')).toHaveLength(2);
  });
  it('selects unique non-pinned members across pages with the same normalized query', async () => {
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => ({
      group: groups[0], conversations: token ? [member('first'), member('second'), member('second'), { ...member('pinned'), pinned_at: '2026-10-20T00:00:00Z' }] : [member('first')], nextPageToken: token ? '' : 'next',
    }));
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [member('first'), member('second'), member('second'), { ...member('pinned'), pinned_at: '2026-10-20T00:00:00Z' }] } });
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} searchText=" 旅行 " assistants="codex" batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'first' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' }));
    await waitFor(() => expect(batchSelection.onToggleMany).toHaveBeenCalledWith(['first', 'second'], true));
    expect(selectionSnapshot).toHaveBeenCalledWith({ keyword: '旅行' }, { params: { is_task_conv: 'false', selection_snapshot: true, selection_group_id: 'a', assistants: 'codex' } });
    expect(api.getConversationGroup).not.toHaveBeenCalledWith('a', 'next', '旅行', 'codex');
  });
  it('rejects an incomplete snapshot without applying a partial selection', async () => {
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => ({
      group: groups[0], conversations: [member(token ? 'second' : 'first')], nextPageToken: 'next',
    }));
    selectionSnapshot.mockResolvedValue({ data: { selection_complete: true, conversations: [member('second')], next_page_token: 'next' } });
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={[groups[0]]} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'first' });
    const select = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(select);
    await waitFor(() => expect(select).toBeEnabled());
    expect(selectionSnapshot).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.getConversationGroup).mock.calls.filter(call => call[1] === 'next')).toHaveLength(0);
    expect(batchSelection.onToggleMany).not.toHaveBeenCalled();
    expect(screen.getByRole('checkbox', { name: 'first' })).not.toBeChecked();
    expect(screen.queryByRole('checkbox', { name: 'second' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'conversationOrganizer.showMore' })).toBeEnabled();
  });
  it.each(['keyword', 'source'] as const)('cancels pending group selection when the %s changes', async change => {
    const page = pendingPage();
    selectionSnapshot.mockImplementation(() => page.promise.then(result => ({ data: { selection_complete: true, conversations: result.conversations } })));
    let changed = false;
    vi.mocked(api.getConversationGroup).mockImplementation(async (_id, token) => token ? page.promise : {
      group: groups[0], conversations: [member(changed ? 'fresh' : 'first')], nextPageToken: changed ? '' : 'next',
    });
    const stableGroups = [groups[0]];
    const batchSelection = { checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() };
    const view = () => <MemoryRouter><SidebarGroups groups={stableGroups} searchText={changed && change === 'keyword' ? 'new' : 'old'} assistants={changed && change === 'source' ? 'workbuddy' : 'codex'} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>;
    const { rerender } = render(view());
    await screen.findByRole('checkbox', { name: 'first' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' }));
    changed = true;
    rerender(view());
    await screen.findByRole('checkbox', { name: 'fresh' });
    await act(async () => page.resolve({ group: groups[0], conversations: [member('stale-next')], nextPageToken: '' }));
    expect(batchSelection.onToggleMany).not.toHaveBeenCalled();
    expect(screen.queryByRole('checkbox', { name: 'stale-next' })).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' })).toBeEnabled();
  });
  it('keeps selection unchanged on page failure and allows retry', async () => {
    vi.mocked(api.getConversationGroup).mockImplementation(async (id, token) => {
      if (token) throw new Error('page unavailable');
      return { group: groups.find(g => g.id === id)!, conversations: [{ conversation_id: `${id}-0`, display_name: `${id}-0`, pinned_at: '', membership_revision: 1 }], nextPageToken: id === 'a' ? 'next' : '' };
    });
    selectionSnapshot.mockRejectedValueOnce(new Error('snapshot unavailable'));
    render(<MemoryRouter><BatchGroups /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'a-0' });
    const local = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(local);
    await waitFor(() => expect(selectionSnapshot).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(local).toBeEnabled());
    expect(screen.getByRole('checkbox', { name: 'a-0' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'b-0' })).toBeChecked();
    selectionSnapshot.mockResolvedValueOnce({ data: { selection_complete: true, conversations: [member('a-0')] } });
    fireEvent.click(local);
    await waitFor(() => expect(local).toBeChecked());
  });
  it('disables empty groups and ignores pending selection after the selection is reset', async () => {
    let resolvePage!: (result: Awaited<ReturnType<typeof api.getConversationGroup>>) => void;
    vi.mocked(api.getConversationGroup).mockImplementation(async (id, token) => {
      if (token) return new Promise(resolve => { resolvePage = resolve; });
      return { group: groups.find(g => g.id === id)!, conversations: id === 'a' ? [{ conversation_id: 'a-0', display_name: 'a-0', pinned_at: '', membership_revision: 1 }] : [], nextPageToken: id === 'a' ? 'next' : '' };
    });
    selectionSnapshot.mockImplementation(() => new Promise<Awaited<ReturnType<typeof api.getConversationGroup>>>(resolve => { resolvePage = resolve; }).then(result => ({ data: { selection_complete: true, conversations: result.conversations } })));
    const selection = { checkedIds: ['outside'], onToggle: vi.fn(), onToggleMany: vi.fn() };
    const view = (checkedIds: string[]) => <MemoryRouter><SidebarGroups groups={groups} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={{ ...selection, checkedIds }} /></MemoryRouter>;
    const { rerender } = render(view(selection.checkedIds));
    await screen.findByRole('checkbox', { name: 'a-0' });
    expect(screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 学习' })).toBeDisabled();
    const local = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(local);
    expect(local).toBeDisabled();
    rerender(view([]));
    await act(async () => { resolvePage({ group: groups[0], conversations: [], nextPageToken: '' }); });
    expect(selection.onToggleMany).not.toHaveBeenCalled();
    expect(local).not.toBeChecked();
    expect(local).toBeEnabled();
  });
  it("shows loaded members beyond the preview limit and blocks drag changes in batch mode", async () => {
    const batchSelection = { checkedIds: ['a-5'], onToggle: vi.fn(), onToggleMany: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={groups} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    const sixth = await screen.findByRole('checkbox', { name: 'a对话5' });
    expect(sixth).toBeChecked();
    fireEvent.click(sixth);
    expect(batchSelection.onToggle).toHaveBeenCalledWith('a-5', false);
    const group = screen.getByTitle('旅行');
    fireEvent.drop(group.closest('.conversation-group')!, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'free-chat' })) });
    expect(api.assignConversation).not.toHaveBeenCalled();
    expect(group.closest('[draggable]')).toHaveAttribute('draggable', 'false');
    fireEvent.click(group);
    await waitFor(() => expect(screen.queryByRole('checkbox', { name: 'a对话5' })).not.toBeInTheDocument());
    fireEvent.click(group);
    await waitFor(() => expect(screen.getByRole('checkbox', { name: 'a对话5' })).toBeChecked());
    expect(screen.queryByRole('button', { name: 'conversationOrganizer.newGroup' })).not.toBeInTheDocument();
  });
  it('keeps the group bubble icon while expanding and collapsing members', async () => {
    renderGroups();
    const group = await screen.findByTitle('旅行');
    expect(group).toHaveAttribute('aria-expanded', 'true');
    expect(group.querySelector('[data-icon="message"]')).not.toBeNull();
    fireEvent.click(group);
    await waitFor(() => expect(group).toHaveAttribute('aria-expanded', 'false'));
    expect(group.querySelector('[data-icon="message"]')).not.toBeNull();
    expect(screen.queryByText('a对话0')).not.toBeInTheDocument();
  });
  it("shows five recent conversations and expands or collapses on demand", async () => {
    renderGroups();
    await screen.findByText("a对话4");
    expect(screen.queryByText("a对话5")).toBeNull();
    fireEvent.click(screen.getAllByRole("button", { name: "conversationOrganizer.showMore" })[0]);
    expect(screen.getByText("a对话5")).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: "conversationOrganizer.showLess" })[0]);
    expect(screen.queryByText("a对话5")).toBeNull();
  });
  it("moves a conversation without reordering members and persists group placement", async () => {
    renderGroups();
    const target = (await screen.findByTitle("旅行")).closest(".conversation-group")!;
    fireEvent.drop(target, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: "free-chat" })) });
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledWith("a", "free-chat"));
    expect(api.updateGroupPlacement).not.toHaveBeenCalled();
    fireEvent.drop(target, { clientY: 0, dataTransfer: transfer(GROUP_DRAG, "b") });
    await waitFor(() => expect(api.updateGroupPlacement).toHaveBeenCalledWith("b", { pinned: false, before_group_id: "a" }));
  });
  it('saves member drop positions within a group and across groups in one request', async () => {
    renderGroups();
    const row = (await screen.findByRole('button', { name: 'a对话1', exact: true })).closest('.conversation-group-member')!;
    vi.spyOn(row, 'getBoundingClientRect').mockReturnValue({ top: 100, height: 32 } as DOMRect);
    fireEvent.dragOver(row, { clientY: 105, dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'a-0', groupId: 'a' })) });
    expect(row).toHaveClass('member-drop-before');
    fireEvent.drop(row, { clientY: 105, dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'a-0', groupId: 'a' })) });
    await waitFor(() => expect(reorder).toHaveBeenCalledWith('a-0', 'a-1', 'before'));
    await waitFor(() => expect(row).toHaveAttribute('draggable', 'true'));
    const drop = createEvent.drop(row, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'b-0', groupId: 'b' })) });
    Object.defineProperty(drop, 'clientY', { value: 125 });
    fireEvent(row, drop);
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledWith('a', 'b-0', { target_conversation_id: 'a-1', position: 'after' }));
    expect(api.assignConversation).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(api.emitConversationGroupsChanged).toHaveBeenCalledTimes(1));
  });
  it('does not commit a failed drop or allow member drops in batch mode', async () => {
    vi.mocked(api.assignConversation).mockRejectedValueOnce(new Error('save failed'));
    const { rerender } = renderGroups();
    const row = (await screen.findByRole('button', { name: 'a对话1', exact: true })).closest('.conversation-group-member')!;
    const dataTransfer = transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'b-0', groupId: 'b' }));
    fireEvent.drop(row, { dataTransfer });
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(row).toHaveAttribute('draggable', 'true'));
    expect(api.emitConversationGroupsChanged).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'b对话0', exact: true })).toBeInTheDocument();
    rerender(<MemoryRouter><SidebarGroups groups={groups} batchSelection={{ checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn() }} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    fireEvent.drop(await screen.findByRole('checkbox', { name: 'a对话1' }), { dataTransfer });
    expect(api.assignConversation).toHaveBeenCalledTimes(1);
    expect(document.querySelector('.record-drag-handle')).toBeNull();
  });
  it("disables drag mutations while searching", async () => {
    renderGroups("旅行");
    const target = (await screen.findByTitle("旅行")).closest(".conversation-group")!;
    fireEvent.drop(target, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: "free-chat" })) });
    expect(api.assignConversation).not.toHaveBeenCalled();
  });
});

 it("disables creating groups while organizer owns the name directory", async () => {
   const onEdit = vi.fn();
   const { unmount } = render(<MemoryRouter><SidebarGroups groups={[]} namesLocked onEdit={onEdit} onRemove={vi.fn()} /></MemoryRouter>);
   const button = screen.getByRole("button", { name: "conversationOrganizer.newGroup" }) as HTMLButtonElement;
   expect(button.disabled).toBe(false);
   fireEvent.click(button);
   const groupItem = await screen.findByRole("menuitem", { name: "conversationOrganizer.newGroup" });
   expect(groupItem.getAttribute("aria-disabled")).toBe("true");
   expect(onEdit).not.toHaveBeenCalled();
   fireEvent.click(screen.getByRole("menuitem", { name: "conversationProject.new" }));
   expect(onEdit).toHaveBeenCalledWith("new-project");
   unmount();
 });

it("rejects conversation drops into projects and keeps project members immovable", async () => {
 const project = { ...groups[0], kind: "project" as const, path: "/code/demo" };
 render(<MemoryRouter><SidebarGroups groups={[project]} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
 const target = (await screen.findByTitle("/code/demo")).closest(".conversation-group")!;
 fireEvent.drop(target, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: "free-chat" })) });
 expect(api.assignConversation).not.toHaveBeenCalled();
 const member = await screen.findByText("a对话0");
 expect(member.closest(".conversation-group-member")?.getAttribute("draggable")).toBe("false");
});

it.each([
  ["b", "p", 105, "p", false],
  ["b", "p", 125, "c", false],
  ["p", "a", 105, "a", false],
  ["p", "a", 125, "b", false],
  ["b", "pin", 105, "pin", true],
  ["b", "pin", 125, "pinned-group", true],
  ["pinned-group", "pin", 105, "pin", true],
  ["pinned-group", "a", 125, "p", false],
])("orders %s around %s within the mixed list", async (source, targetID, clientY, anchor, pinned) => {
  const mixed = [
    { id: "a", name: "A", kind: "group", pinned: false },
    { id: "p", name: "P", kind: "project", path: "/work/p", pinned: false },
    { id: "task", name: "Task", kind: "group", is_task_conv: true, pinned: false },
    { id: "pin", name: "Pin", kind: "project", pinned: true },
    { id: "pinned-group", name: "Pinned group", kind: "group", pinned: true },
    { id: "b", name: "B", kind: "group", pinned: false },
    { id: "c", name: "C", kind: "group", pinned: false },
  ] as api.ConversationGroup[];
  vi.mocked(api.getConversationGroup).mockImplementation(async id => ({ group: mixed.find(g => g.id === id)!, conversations: [], nextPageToken: "" }));
  render(<MemoryRouter><SidebarGroups groups={mixed} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
  const targetGroup = mixed.find(group => group.id === targetID)!;
  const target = (await screen.findByTitle(targetGroup.path || targetGroup.name)).closest(".conversation-group")!;
  vi.spyOn(target.querySelector(".conversation-group-row")!, "getBoundingClientRect").mockReturnValue({ top: 100, height: 32 } as DOMRect);
  fireEvent.drop(target, { dataTransfer: transfer(GROUP_DRAG, "task") });
  expect(api.updateGroupPlacement).not.toHaveBeenCalled();
  const drop = createEvent.drop(target, { dataTransfer: transfer(GROUP_DRAG, source) });
  Object.defineProperty(drop, "clientY", { value: clientY });
  fireEvent(target, drop);
  await waitFor(() => expect(api.updateGroupPlacement).toHaveBeenCalledWith(source, { pinned, before_group_id: anchor }));
});
