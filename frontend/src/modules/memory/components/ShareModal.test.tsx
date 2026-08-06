import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import ShareModal from "./ShareModal";

const t = (key: string) => key;

const baseProps = {
  t,
  shareModalOpen: true,
  closeShareModal: vi.fn(),
  handleConfirmShare: vi.fn(),
  shareTarget: null,
  shareDraft: { groupIds: [], userIds: [], message: "" },
  setShareDraft: vi.fn(),
  shareLoading: false,
  shareGroups: [],
  shareUsers: [],
  shareStatusLoading: false,
  shareStatusError: "",
  shareStatusRecords: [],
  getSkillShareStatusMeta: (status: string) => ({ color: "blue", text: status }),
  formatDateTime: (value?: string) => value || "",
};

describe("ShareModal", () => {
  it("renders nothing inside the modal body when there is no share target", () => {
    render(<ShareModal {...baseProps} />);
    expect(screen.getByText("admin.memoryShareDialogTitle")).toBeInTheDocument();
    expect(screen.queryByText("admin.memoryShareGroups")).not.toBeInTheDocument();
  });

  it("renders the share form for a skill target with the sync status panel", () => {
    render(
      <ShareModal
        {...baseProps}
        shareTarget={{ tab: "skills", item: { name: "My Skill" } }}
        shareStatusRecords={[
          {
            id: "share-1",
            status: "accepted",
            recipients: [{ name: "Alice" }],
            createdAt: "2024-01-01",
            decidedAt: "2024-01-02",
            updatedAt: "2024-01-02",
          } as any,
        ]}
      />,
    );

    expect(screen.getByText("My Skill")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryShareSkillHint")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryShareSyncedRecipients")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("accepted")).toBeInTheDocument();
  });

  it("renders the experience hint and no share status panel for non-skill targets", () => {
    render(
      <ShareModal
        {...baseProps}
        shareTarget={{ tab: "experience", item: { title: "My Experience" } }}
      />,
    );
    expect(screen.getByText("My Experience")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryShareExperienceHint")).toBeInTheDocument();
    expect(screen.queryByText("admin.memoryShareSyncedRecipients")).not.toBeInTheDocument();
  });

  it("shows the empty recipients hint and updates message on change", () => {
    render(
      <ShareModal
        {...baseProps}
        shareTarget={{ tab: "experience", item: { title: "My Experience" } }}
      />,
    );
    expect(screen.getByText("admin.memoryShareEmptyRecipients")).toBeInTheDocument();
    fireEvent.change(
      screen.getByPlaceholderText("admin.memorySkillShareMessagePlaceholder"),
      { target: { value: "hello" } },
    );
    expect(baseProps.setShareDraft).toHaveBeenCalled();
  });

  it("renders selected group and user tags when ids are present", () => {
    render(
      <ShareModal
        {...baseProps}
        shareTarget={{ tab: "experience", item: { title: "My Experience" } }}
        shareDraft={{ groupIds: ["g1"], userIds: ["u1"], message: "" }}
        shareGroups={[{ group_id: "g1", group_name: "Group One" }]}
        shareUsers={[{ user_id: "u1", username: "bob", display_name: "Bob" }]}
      />,
    );
    const selectedTags = document.querySelector(".memory-share-selected-tags");
    expect(selectedTags).not.toBeNull();
    expect(selectedTags).toHaveTextContent("Group One");
    expect(selectedTags).toHaveTextContent("Bob");
  });

  it("shows the share status error message when loading the sync status fails", () => {
    render(
      <ShareModal
        {...baseProps}
        shareTarget={{ tab: "skills", item: { name: "My Skill" } }}
        shareStatusError="load failed"
      />,
    );
    expect(screen.getByText("load failed")).toBeInTheDocument();
  });
});
