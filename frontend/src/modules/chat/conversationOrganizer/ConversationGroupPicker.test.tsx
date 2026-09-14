import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ConversationGroupPicker from "./ConversationGroupPicker";
import * as api from "./api";
const t = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("./api", () => ({ CONVERSATION_GROUPS_CHANGED_EVENT: "groups-changed", getLatestOrganizerState: vi.fn(async () => ({ run: null })), listConversationGroups: vi.fn(), assignConversation: vi.fn(), removeConversation: vi.fn(), emitConversationGroupsChanged: vi.fn() }));
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listConversationGroups).mockResolvedValue(Array.from({ length: 15 }, (_, i) => ({ id: `g${i}`, name: `Group ${i}` })) as api.ConversationGroup[]);
});
describe("conversation group picker", () => {
  it("searches all groups, including beyond the first ten, and moves a free conversation directly", async () => {
    render(<ConversationGroupPicker conversation={{ conversationId: "free" }} onCreate={vi.fn()} />);
    await screen.findByRole("button", { name: "Group 14" });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Group 14" } });
    expect(screen.queryByRole("button", { name: "Group 0" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Group 14" }));
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledWith("g14", "free"));
    expect(api.emitConversationGroupsChanged).toHaveBeenCalled();
  });
  it("marks the current group and allows moving a grouped conversation back to free", async () => {
    render(<ConversationGroupPicker conversation={{ conversationId: "chat", groupId: "g0" }} onCreate={vi.fn()} />);
    expect(await screen.findByRole("button", { name: /Group 0/ })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "conversationOrganizer.freeConversation" }));
    await waitFor(() => expect(api.removeConversation).toHaveBeenCalledWith("g0", "chat"));
  });
  it("keeps creation available when search has no matches", async () => {
    const create = vi.fn();
    render(<ConversationGroupPicker conversation={{ conversationId: "chat", groupId: "g0" }} onCreate={create} />);
    await screen.findByRole("button", { name: "Group 14" });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "missing" } });
    fireEvent.click(screen.getByRole("button", { name: "conversationOrganizer.newAndMoveEllipsis" }));
    expect(create).toHaveBeenCalledOnce();
    expect(api.assignConversation).not.toHaveBeenCalled();
  });
});
