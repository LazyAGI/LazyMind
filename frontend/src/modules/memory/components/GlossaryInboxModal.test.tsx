import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, within } from "@/test/testUtils";
import GlossaryInboxModal from "./GlossaryInboxModal";
import type { GlossaryChangeProposal } from "../shared";

const t = (key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key;

const baseProposal: GlossaryChangeProposal = {
  id: "proposal-1",
  reason: "detected in conversation",
  before: undefined,
  after: {
    id: "asset-new",
    term: "术语A",
    aliases: ["别名A"],
    content: "内容A",
    group: "",
    source: "ai",
  },
  backendConflictGroupIds: [],
  backendConflictGroups: [],
} as unknown as GlossaryChangeProposal;

const baseProps = {
  t,
  glossaryInboxOpen: true,
  setGlossaryInboxOpen: vi.fn(),
  glossaryChangeProposals: [] as GlossaryChangeProposal[],
  glossaryInboxLoading: false,
  glossaryInboxError: "",
  glossaryInboxSubmitting: "" as const,
  refreshGlossaryConflicts: vi.fn(),
  glossarySourceColorMap: { user: "blue", ai: "purple" } as Record<string, string>,
  glossarySourceLabelMap: { user: "User", ai: "AI" } as Record<string, string>,
  rejectGlossaryProposals: vi.fn(),
  applyGlossaryProposals: vi.fn(),
};

describe("GlossaryInboxModal", () => {
  it("renders an empty state when there are no proposals", () => {
    renderWithProviders(<GlossaryInboxModal {...baseProps} />);
    expect(screen.getByText("admin.memoryGlossaryInboxEmpty")).toBeInTheDocument();
  });

  it("renders a loading spinner when glossaryInboxLoading is true", () => {
    renderWithProviders(<GlossaryInboxModal {...baseProps} glossaryInboxLoading />);
    expect(screen.getByText("common.loading")).toBeInTheDocument();
  });

  it("renders a proposal card with its term and default action mode", () => {
    renderWithProviders(
      <GlossaryInboxModal
        {...baseProps}
        glossaryChangeProposals={[baseProposal]}
      />,
    );
    expect(screen.getByText("术语A")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryGlossaryInboxTypeAdd")).toBeInTheDocument();
  });

  it("shows an error alert with a retry action when glossaryInboxError is set", () => {
    const refreshGlossaryConflicts = vi.fn();
    renderWithProviders(
      <GlossaryInboxModal
        {...baseProps}
        glossaryInboxError="load failed"
        refreshGlossaryConflicts={refreshGlossaryConflicts}
      />,
    );
    expect(screen.getByText("load failed")).toBeInTheDocument();
    fireEvent.click(screen.getByText("common.retry"));
    expect(refreshGlossaryConflicts).toHaveBeenCalledWith({ showErrorToast: true });
  });

  it("calls rejectGlossaryProposals when confirming the reject action for a proposal without conflicts", () => {
    const rejectGlossaryProposals = vi.fn();
    renderWithProviders(
      <GlossaryInboxModal
        {...baseProps}
        glossaryChangeProposals={[baseProposal]}
        rejectGlossaryProposals={rejectGlossaryProposals}
      />,
    );

    fireEvent.click(screen.getByText("admin.memoryGlossaryInboxReject"));
    fireEvent.click(screen.getByText("admin.memoryGlossaryInboxActionRejectTitle"));
    expect(rejectGlossaryProposals).toHaveBeenCalledWith([baseProposal]);
  });

  it("switches to create mode and calls applyGlossaryProposals when a term is filled in and confirmed", () => {
    const applyGlossaryProposals = vi.fn();
    renderWithProviders(
      <GlossaryInboxModal
        {...baseProps}
        glossaryChangeProposals={[baseProposal]}
        applyGlossaryProposals={applyGlossaryProposals}
      />,
    );

    fireEvent.click(screen.getByText("admin.memoryGlossaryInboxActionLabelCreate"));
    const termInput = screen.getByPlaceholderText(
      "admin.memoryGlossaryInboxNewGroupPlaceholder",
    );
    fireEvent.change(termInput, { target: { value: "新术语" } });
    fireEvent.click(screen.getByText("admin.memoryGlossaryInboxConfirmCreate"));
    fireEvent.click(screen.getByText("common.confirm"));

    expect(applyGlossaryProposals).toHaveBeenCalledTimes(1);
    const [[proposals, resolutions]] = applyGlossaryProposals.mock.calls;
    expect(proposals).toEqual([baseProposal]);
    expect(resolutions?.["proposal-1"]?.mode).toBe("create");
    expect(resolutions?.["proposal-1"]?.newGroupTerm).toBe("新术语");
  });

  it("disables the separate action option when there are no conflicting target groups", () => {
    renderWithProviders(
      <GlossaryInboxModal
        {...baseProps}
        glossaryChangeProposals={[baseProposal]}
      />,
    );
    const separateOption = screen
      .getByText("admin.memoryGlossaryInboxActionLabelSeparate:{\"word\":\"术语A\"}")
      .closest("label");
    const checkbox = separateOption
      ? within(separateOption).getByRole("checkbox")
      : null;
    expect(checkbox).toHaveAttribute("disabled");
  });
});
