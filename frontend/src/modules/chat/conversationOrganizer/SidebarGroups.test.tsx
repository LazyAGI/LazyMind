import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SidebarGroups from "./SidebarGroups";
import * as api from "./api";
import { CONVERSATION_DRAG, GROUP_DRAG } from "./drag";
const t = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceGetConversationDetail: vi.fn() }) }));
vi.mock("./ConversationMembership", () => ({ default: () => null }));
vi.mock("./api", () => ({ getConversationGroup: vi.fn(), listConversationGroups: vi.fn(), updateGroupPlacement: vi.fn(), assignConversation: vi.fn(), emitConversationGroupsChanged: vi.fn(), CONVERSATION_GROUPS_CHANGED_EVENT: "group-change" }));
const groups = [{ id: "a", name: "旅行", pinned: false }, { id: "b", name: "学习", pinned: false }] as api.ConversationGroup[];
function renderGroups(searchText = "") { return render(<MemoryRouter><SidebarGroups groups={groups} searchText={searchText} onEdit={vi.fn()} onRemove={vi.fn()} /></MemoryRouter>); }
function transfer(type: string, payload: string) { return { types: [type], getData: (key: string) => key === type ? payload : "" }; }
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getConversationGroup).mockImplementation(async id => ({ group: groups.find(g => g.id === id)!, conversations: Array.from({ length: 6 }, (_, i) => ({ conversation_id: `${id}-${i}`, display_name: `${id}对话${i}`, pinned_at: "", membership_revision: 1 })), nextPageToken: "", total: 6 }));
  vi.mocked(api.listConversationGroups).mockResolvedValue(groups);
});
describe("group sidebar", () => {
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
   expect(button.disabled).toBe(true);
   fireEvent.click(button);
   expect(onEdit).not.toHaveBeenCalled();
   unmount();
 });
