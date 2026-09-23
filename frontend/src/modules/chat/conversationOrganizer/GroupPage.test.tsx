import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import GroupPage from "./GroupPage";
import * as api from "./api";
import { readChatConversationFilters, CHAT_NEW_RUN_IN_BACKGROUND_KEY, CHAT_PENDING_CONVERSATION_GROUP_KEY } from "../constants/chat";

const t = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("../hooks/useChatModelProviderGuard", () => ({ useChatModelProviderGuard: () => ({ canChat: true }) }));
vi.mock("../components/ChatInput", () => ({ default: ({ runInBackground, setIsChatContent }: any) => <button onClick={() => setIsChatContent(true)}>{runInBackground ? "Start task" : "Start chat"}</button> }));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "", getLocalizedErrorMessage: () => "Save failed" }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({}) }));
vi.mock("../components/ConversationTitleEditor", () => ({ default: () => null }));
vi.mock("./useOrganizerNameLock", () => ({ default: () => false }));
vi.mock("./ConversationMembership", () => ({ default: () => null }));
vi.mock("./api", () => ({ getConversationGroup: vi.fn(), CONVERSATION_GROUPS_CHANGED_EVENT: "groups-changed", deleteConversationGroup: vi.fn(), updateConversationGroup: vi.fn(), updateGroupPlacement: vi.fn(), emitConversationGroupsChanged: vi.fn() }));
const detail = (id: string, task: boolean) => ({ group: { id, name: id, kind: "group", is_task_conv: task, member_count: 0 }, conversations: [], nextPageToken: "" }) as any;
beforeEach(() => { vi.clearAllMocks(); sessionStorage.clear(); });
function SwitchGroup() { const navigate = useNavigate(); return <button onClick={() => navigate('/groups/task')}>Switch to task</button>; }
function view(id = 'chat') { return render(<MemoryRouter initialEntries={['/groups/' + id]}><SwitchGroup /><Routes><Route path="/groups/:groupId" element={<GroupPage />} /><Route path="/agent/chat/home" element={<div>Home</div>} /></Routes></MemoryRouter>); }
it.each(["group", "project"])("opens task %s in task mode and starts a task there", async kind => {
  const response = detail("task", true);
  response.group.kind = kind;
  vi.mocked(api.getConversationGroup).mockResolvedValue(response);
  view("task");
  const start = await screen.findByRole("button", { name: "Start task" });
  expect(screen.getByRole("button", { name: "settingsPage.recovery.task" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "conversationOrganizer.home" })).not.toBeInTheDocument();
  expect(readChatConversationFilters().filter).toBe("task");
  fireEvent.click(start);
  await screen.findByText("Home");
  expect(sessionStorage.getItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY)).toBe("1");
  expect(sessionStorage.getItem(CHAT_PENDING_CONVERSATION_GROUP_KEY)).toBe("task");
});
it("ignores an old detail response after switching group routes", async () => {
  let finish!: (value: any) => void;
  vi.mocked(api.getConversationGroup).mockImplementation(id => id === "chat" ? new Promise(resolve => { finish = resolve; }) : Promise.resolve(detail("task", true)));
  view();
  await waitFor(() => expect(api.getConversationGroup).toHaveBeenCalledWith("chat", "", "", undefined));
  fireEvent.click(screen.getByText("Switch to task"));
  await screen.findByRole("button", { name: "Start task" });
  await act(async () => finish(detail("chat", false)));
  expect(screen.queryByRole("button", { name: "Start chat" })).not.toBeInTheDocument();
  expect(readChatConversationFilters().filter).toBe("task");
});
it("reloads group detail when the source filter changes", async () => {
  vi.mocked(api.getConversationGroup).mockImplementation(async (id, _token, _keyword, assistants) => ({ ...detail(id, false), conversations: [{ conversation_id: assistants || 'all', display_name: assistants || 'all', membership_revision: 1 }] }));
  view();
  await screen.findByText('all');
  const { selectChatConversationSources } = await import('../constants/chat');
  act(() => selectChatConversationSources(['codex']));
  await screen.findByText('codex');
  expect(screen.queryByText('all')).not.toBeInTheDocument();
  expect(api.getConversationGroup).toHaveBeenLastCalledWith('chat', '', '', 'codex');
});
