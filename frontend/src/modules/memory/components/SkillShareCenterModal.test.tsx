import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import SkillShareCenterModal from "./SkillShareCenterModal";

const t = (key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key;

const baseProps = {
  t,
  skillShareCenterOpen: true,
  closeSkillShareCenter: vi.fn(),
  skillShareCenterTab: "incoming" as const,
  setSkillShareCenterTab: vi.fn(),
  incomingPendingCount: 1,
  outgoingSkillShares: [],
  skillShareCenterLoading: false,
  refreshSkillShareCenter: vi.fn(),
  skillShareCenterError: "",
  currentSkillShareList: [] as any[],
  skillShareActionState: {},
  getSkillShareStatusMeta: (status: string) => ({ color: "blue", text: status }),
  formatDateTime: (value?: string) => value || "",
  previewSkillShare: vi.fn(),
  rejectIncomingSkillShare: vi.fn(),
  acceptIncomingSkillShare: vi.fn(),
  isSkillShareActionable: () => true,
};

describe("SkillShareCenterModal", () => {
  it("renders an empty state when there are no shares", () => {
    renderWithProviders(<SkillShareCenterModal {...baseProps} />);
    expect(
      screen.getByText("admin.memorySkillShareEmptyIncoming"),
    ).toBeInTheDocument();
  });

  it("renders share cards with sender info for the incoming tab", () => {
    renderWithProviders(
      <SkillShareCenterModal
        {...baseProps}
        currentSkillShareList={[
          {
            id: "share-1",
            status: "pending",
            skillName: "My Skill",
            skillDescription: "desc",
            sender: { name: "Alice" },
            recipients: [{ type: "user", id: "u1", name: "Bob" }],
            createdAt: "2024-01-01",
          },
        ]}
      />,
    );
    expect(screen.getByText("My Skill")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Bob")).toBeInTheDocument();
  });

  it("calls acceptIncomingSkillShare when the accept button is clicked", () => {
    const acceptIncomingSkillShare = vi.fn();
    renderWithProviders(
      <SkillShareCenterModal
        {...baseProps}
        acceptIncomingSkillShare={acceptIncomingSkillShare}
        currentSkillShareList={[
          {
            id: "share-1",
            status: "pending",
            skillName: "My Skill",
            recipients: [],
            createdAt: "2024-01-01",
          },
        ]}
      />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillShareAccept"));
    expect(acceptIncomingSkillShare).toHaveBeenCalled();
  });

  it("shows an error alert with a retry button when skillShareCenterError is set", () => {
    const refreshSkillShareCenter = vi.fn();
    renderWithProviders(
      <SkillShareCenterModal
        {...baseProps}
        skillShareCenterError="failed to load"
        refreshSkillShareCenter={refreshSkillShareCenter}
      />,
    );
    expect(screen.getByText("failed to load")).toBeInTheDocument();
    fireEvent.click(screen.getByText("common.retry"));
    expect(refreshSkillShareCenter).toHaveBeenCalledWith({ showErrorToast: true });
  });

  it("does not render sender info or accept/reject actions on the outgoing tab", () => {
    renderWithProviders(
      <SkillShareCenterModal
        {...baseProps}
        skillShareCenterTab="outgoing"
        currentSkillShareList={[
          {
            id: "share-2",
            status: "accepted",
            skillName: "Shared Skill",
            recipients: [],
            createdAt: "2024-01-01",
          },
        ]}
      />,
    );
    expect(screen.queryByText("admin.memorySkillShareSender")).not.toBeInTheDocument();
    expect(screen.queryByText("admin.memorySkillShareAccept")).not.toBeInTheDocument();
  });
});
