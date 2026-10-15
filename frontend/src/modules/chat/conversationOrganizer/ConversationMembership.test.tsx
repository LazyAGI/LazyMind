import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ConversationMembership from "./ConversationMembership";
import { getChatConversationPath } from "@/modules/chat/constants/chat";

const mocks = vi.hoisted(() => ({ deleteConversation: vi.fn(), groupsChanged: vi.fn(), error: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceDeleteConversation: mocks.deleteConversation }) }));
vi.mock("./api", () => ({ emitConversationGroupsChanged: mocks.groupsChanged }));
vi.mock("./ConversationGroupPicker", () => ({ conversationGroupSubmenu: () => [] }));
vi.mock("./ConversationMembershipModal", () => ({ default: () => null }));
vi.mock("@/modules/chat/components/ArchiveConversationModal", () => ({ default: () => null }));
vi.mock("antd", async () => ({ ...await vi.importActual<typeof import("antd")>("antd"), message: { error: mocks.error, success: vi.fn() } }));

function Location() {
  return <output data-testid="location">{useLocation().pathname}</output>;
}

async function openConfirmation(groupKind: string) {
  render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[getChatConversationPath("member")]}>
    <ConversationMembership conversationId="member" groupId="group-1" groupKind={groupKind} title="组内对话" onRename={vi.fn()} />
    <Location />
  </MemoryRouter></ConfigProvider>);
  fireEvent.click(screen.getByRole("button", { name: "conversationOrganizer.conversationMore" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "common.delete" }));
  return screen.findByRole("dialog", { name: "settingsPage.recovery.moveToTrashTitle" });
}

describe("ConversationMembership Trash confirmation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.deleteConversation.mockResolvedValue({});
  });

  it.each(["group", "project"])("shows the shared content and cancels without deleting a %s member", async kind => {
    const dialog = await openConfirmation(kind);
    expect(within(dialog).getByText("组内对话")).toHaveClass("conversation-trash-confirm-name");
    expect(within(dialog).getByText("settingsPage.recovery.moveToTrashDescription")).toBeInTheDocument();
    expect(within(dialog).getByText("chat.fork.deleteNotice")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "common.cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(mocks.deleteConversation).not.toHaveBeenCalled();
    expect(mocks.groupsChanged).not.toHaveBeenCalled();
    expect(screen.getByTestId("location")).toHaveTextContent(getChatConversationPath("member"));
  });

  it.each(["group", "project"])("keeps a failed %s deletion retryable and navigates only after success", async kind => {
    mocks.deleteConversation.mockRejectedValueOnce(new Error("offline"));
    const dialog = await openConfirmation(kind);
    const submit = within(dialog).getByRole("button", { name: "common.delete" });
    fireEvent.click(submit);
    await waitFor(() => expect(mocks.error).toHaveBeenCalledWith("settingsPage.recovery.operationFailed"));
    await waitFor(() => expect(submit).not.toHaveClass("ant-btn-loading"));
    expect(dialog).toBeInTheDocument();
    expect(mocks.groupsChanged).not.toHaveBeenCalled();
    expect(screen.getByTestId("location")).toHaveTextContent(getChatConversationPath("member"));

    let resolveDelete!: (value: object) => void;
    mocks.deleteConversation.mockImplementationOnce(() => new Promise(resolve => { resolveDelete = resolve; }));
    fireEvent.click(submit);
    await waitFor(() => expect(submit).toHaveClass("ant-btn-loading"));
    fireEvent.click(submit);
    expect(mocks.deleteConversation).toHaveBeenCalledTimes(2);
    await act(async () => resolveDelete({}));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(mocks.deleteConversation).toHaveBeenLastCalledWith({ conversation: "member" });
    expect(mocks.groupsChanged).toHaveBeenCalledOnce();
    expect(screen.getByTestId("location").textContent).toBe("/agent/chat/home");
  });
});
