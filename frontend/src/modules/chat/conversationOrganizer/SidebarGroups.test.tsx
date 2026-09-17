import { act, createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SidebarGroups, { type GroupBatchSelection } from "./SidebarGroups";
import * as api from "./api";
import { CONVERSATION_DRAG, GROUP_DRAG } from "./drag";
const t = (key: string, values?: { name?: string }) => values?.name ? `${key} ${values.name}` : key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceGetConversationDetail: vi.fn().mockResolvedValue({ data: { conversation: { display_name: "a对话0", title_revision: 1 } } }) }) }));
vi.mock("./ConversationMembership", () => ({ default: ({ title, onRename }: { title?: string; onRename: () => void }) => <button onClick={onRename}>重命名 {title}</button> }));
vi.mock("./api", () => ({ getConversationGroup: vi.fn(), listConversationGroups: vi.fn(), updateGroupPlacement: vi.fn(), assignConversation: vi.fn(), emitConversationGroupsChanged: vi.fn(), CONVERSATION_GROUPS_CHANGED_EVENT: "group-change" }));
const groups = [{ id: "a", name: "旅行", pinned: false }, { id: "b", name: "学习", pinned: false }] as api.ConversationGroup[];
function renderGroups(searchText = "") { return render(<MemoryRouter><SidebarGroups groups={groups} searchText={searchText} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>); }
function transfer(type: string, payload: string) { return { types: [type], getData: (key: string) => key === type ? payload : "" }; }
function BatchGroups() {
  const [checkedIds, setCheckedIds] = useState(['b-0']);
  const onToggleMany: GroupBatchSelection['onToggleMany'] = (ids, checked) => setCheckedIds(old => checked ? [...new Set([...old, ...ids])] : old.filter(id => !ids.includes(id)));
  return <SidebarGroups groups={groups} onEdit={vi.fn()} onRemove={vi.fn()} batchSelection={{ checkedIds, onToggle: (id, checked) => onToggleMany([id], checked), onToggleMany, onMembersChange: vi.fn() }} />;
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getConversationGroup).mockImplementation(async id => ({ group: groups.find(g => g.id === id)!, conversations: Array.from({ length: 6 }, (_, i) => ({ conversation_id: `${id}-${i}`, display_name: `${id}对话${i}`, pinned_at: "", membership_revision: 1 })), nextPageToken: "", total: 6 }));
  vi.mocked(api.listConversationGroups).mockResolvedValue(groups);
});
describe("group sidebar", () => {
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

  it("replaces the selected conversation title with an inline editor", async () => {
    renderGroups();
    fireEvent.click(await screen.findByRole("button", { name: "重命名 a对话0" }));
    const input = await screen.findByRole("textbox", { name: "conversationRename.name" });
    expect(input.closest(".conversation-group-member")).toHaveAttribute("draggable", "false");
    expect(screen.queryByRole("button", { name: "a对话0", exact: true })).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.getByRole("button", { name: "a对话0", exact: true })).toBeInTheDocument();
  });

  it('selects and clears only one group, including its remaining pages', async () => {
    const member = (id: string) => ({ conversation_id: id, display_name: id, pinned_at: '', membership_revision: 1 });
    vi.mocked(api.getConversationGroup).mockImplementation(async (id, token) => ({ group: groups.find(g => g.id === id)!, conversations: id === 'b' ? [member('b-0')] : token ? [member('a-1')] : [member('a-0')], nextPageToken: id === 'a' && !token ? 'next' : '' }));
    render(<MemoryRouter><BatchGroups /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'a-0' });
    const local = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(local);
    await waitFor(() => expect(local).toBeChecked());
    expect(api.getConversationGroup).toHaveBeenCalledWith('a', 'next', '');
    expect(screen.getByRole('checkbox', { name: 'a-1' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'b-0' })).toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: 'a-0' }));
    expect(local).toBePartiallyChecked();
    fireEvent.click(local);
    expect(local).toBeChecked();
    fireEvent.click(screen.getByTitle('旅行'));
    fireEvent.click(local);
    expect(local).not.toBeChecked();
    fireEvent.click(screen.getByTitle('旅行'));
    expect(screen.getByRole('checkbox', { name: 'a-1' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'b-0' })).toBeChecked();
  });
  it('keeps selection unchanged on page failure and allows retry', async () => {
    vi.mocked(api.getConversationGroup).mockImplementation(async (id, token) => {
      if (token) throw new Error('page unavailable');
      return { group: groups.find(g => g.id === id)!, conversations: [{ conversation_id: `${id}-0`, display_name: `${id}-0`, pinned_at: '', membership_revision: 1 }], nextPageToken: id === 'a' ? 'next' : '' };
    });
    render(<MemoryRouter><BatchGroups /></MemoryRouter>);
    await screen.findByRole('checkbox', { name: 'a-0' });
    const local = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup 旅行' });
    fireEvent.click(local);
    await waitFor(() => expect(api.getConversationGroup).toHaveBeenCalledWith('a', 'next', ''));
    await waitFor(() => expect(local).toBeEnabled());
    expect(screen.getByRole('checkbox', { name: 'a-0' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'b-0' })).toBeChecked();
    vi.mocked(api.getConversationGroup).mockResolvedValueOnce({ group: groups[0], conversations: [], nextPageToken: '' });
    fireEvent.click(local);
    await waitFor(() => expect(local).toBeChecked());
  });
  it('disables empty groups and ignores pending selection after the selection is reset', async () => {
    let resolvePage!: (result: Awaited<ReturnType<typeof api.getConversationGroup>>) => void;
    vi.mocked(api.getConversationGroup).mockImplementation(async (id, token) => {
      if (token) return new Promise(resolve => { resolvePage = resolve; });
      return { group: groups.find(g => g.id === id)!, conversations: id === 'a' ? [{ conversation_id: 'a-0', display_name: 'a-0', pinned_at: '', membership_revision: 1 }] : [], nextPageToken: id === 'a' ? 'next' : '' };
    });
    const selection = { checkedIds: ['outside'], onToggle: vi.fn(), onToggleMany: vi.fn(), onMembersChange: vi.fn() };
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
    const batchSelection = { checkedIds: ['a-5'], onToggle: vi.fn(), onToggleMany: vi.fn(), onMembersChange: vi.fn() };
    render(<MemoryRouter><SidebarGroups groups={groups} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    const sixth = await screen.findByRole('checkbox', { name: 'a对话5' });
    expect(sixth).toBeChecked();
    fireEvent.click(sixth);
    expect(batchSelection.onToggle).toHaveBeenCalledWith('a-5', false);
    expect(batchSelection.onMembersChange).toHaveBeenLastCalledWith(expect.arrayContaining([expect.objectContaining({ conversation_id: 'a-5' })]));
    const group = screen.getByTitle('旅行');
    fireEvent.drop(group.closest('.conversation-group')!, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'free-chat' })) });
    expect(api.assignConversation).not.toHaveBeenCalled();
    expect(group.closest('[draggable]')).toHaveAttribute('draggable', 'false');
    fireEvent.click(group);
    expect(screen.queryByRole('checkbox', { name: 'a对话5' })).not.toBeInTheDocument();
    fireEvent.click(group);
    expect(screen.getByRole('checkbox', { name: 'a对话5' })).toBeChecked();
    expect(screen.queryByRole('button', { name: 'conversationOrganizer.newGroup' })).not.toBeInTheDocument();
  });
  it('keeps the group bubble icon while expanding and collapsing members', async () => {
    renderGroups();
    const group = await screen.findByTitle('旅行');
    expect(group).toHaveAttribute('aria-expanded', 'true');
    expect(group.querySelector('[data-icon="message"]')).not.toBeNull();
    fireEvent.click(group);
    expect(group).toHaveAttribute('aria-expanded', 'false');
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
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledWith('a', 'a-0', { target_conversation_id: 'a-1', position: 'before' }));
    await waitFor(() => expect(row).toHaveAttribute('draggable', 'true'));
    const drop = createEvent.drop(row, { dataTransfer: transfer(CONVERSATION_DRAG, JSON.stringify({ id: 'b-0', groupId: 'b' })) });
    Object.defineProperty(drop, 'clientY', { value: 125 });
    fireEvent(row, drop);
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledWith('a', 'b-0', { target_conversation_id: 'a-1', position: 'after' }));
    expect(api.assignConversation).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(api.emitConversationGroupsChanged).toHaveBeenCalledTimes(2));
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
    rerender(<MemoryRouter><SidebarGroups groups={groups} batchSelection={{ checkedIds: [], onToggle: vi.fn(), onToggleMany: vi.fn(), onMembersChange: vi.fn() }} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>);
    fireEvent.drop(await screen.findByRole('checkbox', { name: 'a对话1' }), { dataTransfer });
    expect(api.assignConversation).toHaveBeenCalledTimes(1);
    expect(document.querySelector('.conversation-member-drag-handle')).toBeNull();
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
