import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ConversationMembershipModal from "./ConversationMembershipModal";
import * as api from "./api";

const t = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("./api", () => ({ CONVERSATION_GROUPS_CHANGED_EVENT: "groups-changed", getLatestOrganizerState: vi.fn(async () => ({ run: null })), listConversationGroups: vi.fn(), createConversationGroup: vi.fn(), assignConversation: vi.fn(), removeConversation: vi.fn(), emitConversationGroupsChanged: vi.fn() }));
const group = { id: "travel", name: "旅行" } as api.ConversationGroup;
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listConversationGroups).mockResolvedValue([group]);
  vi.mocked(api.createConversationGroup).mockResolvedValue(group);
});

describe("shared create-and-move dialog", () => {
  it("creates a group with normalized fields before moving the conversation", async () => {
    const onClose = vi.fn();
    render(<ConversationMembershipModal conversation={{ conversationId: "chat" }} onClose={onClose} />);
    fireEvent.change(screen.getByLabelText("conversationOrganizer.groupName"), { target: { value: "  旅行  " } });
    fireEvent.change(screen.getByLabelText("conversationOrganizer.scope"), { target: { value: "  行程安排  " } });
    fireEvent.click(screen.getByRole("button", { name: "conversationOrganizer.save" }));
    await waitFor(() => expect(api.assignConversation).toHaveBeenCalledWith("travel", "chat"));
    expect(api.createConversationGroup).toHaveBeenCalledWith({ name: "旅行", scope: "行程安排" });
    expect(onClose).toHaveBeenCalledOnce();
  });
});
