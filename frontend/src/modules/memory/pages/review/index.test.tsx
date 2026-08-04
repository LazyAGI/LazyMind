import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import MemoryReviewPage from "./index";
import { useMemoryManagementOutletContext } from "../../context";

vi.mock("../../context", () => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<
  typeof vi.fn
>;

const baseContext = {
  t,
  isReviewRouteRequested: true,
  activeProposal: null,
  isBackendSuggestionReviewMode: false,
  activeReviewStep: 0,
  goToReviewChoose: vi.fn(),
  goToReviewPreview: vi.fn(),
  closeChangeReview: vi.fn(),
  backendDraftSubmitting: "",
  discardBackendDraftAndReturn: vi.fn(),
  backendDraftLoading: false,
  approvedBackendSuggestionIds: [],
  isAnyBackendSuggestionMutating: false,
  confirmBackendDraft: vi.fn(),
  allBackendSuggestionsSelected: false,
  hasPartialBackendSuggestionSelection: false,
  setAllBackendSuggestionsSelected: vi.fn(),
  backendRejectedSuggestionCount: 0,
  activeBackendSuggestions: [],
  activeBackendSuggestionSourceText: "",
  selectedBackendSuggestionCount: 0,
  backendSuggestionBatchSubmitting: "",
  handleBackendBatchAccept: vi.fn(),
  handleBackendBatchRejectWithConfirm: vi.fn(),
  backendSuggestionHasMore: false,
  backendSuggestionLoadingMore: false,
  backendSuggestionLoadMoreError: "",
  loadMoreBackendSuggestions: vi.fn(),
  clearSelectedBackendSuggestions: vi.fn(),
  backendSuggestionSubmitting: {},
  selectedBackendSuggestionIds: [],
  isBackendSuggestionSelectable: vi.fn().mockReturnValue(true),
  setBackendSuggestionSelected: vi.fn(),
  submitBackendSuggestionDecision: vi.fn(),
  backendDraftDiffLines: [],
  backendDraftPreview: null,
  backendDraftHunkSubmitting: {},
  backendDraftReviewUndoing: false,
  submitBackendDraftHunkDecision: vi.fn(),
  undoBackendDraftReview: vi.fn(),
  backendDraftReady: false,
  qaQuestionDraft: "",
  setQaQuestionDraft: vi.fn(),
  handleReviewQuestionKeyDown: vi.fn(),
  sendReviewQuestion: vi.fn(),
  activeProposalDiff: null,
  reviewSuggestionSubmitting: false,
  approveChangeProposal: vi.fn(),
  hasEffectiveChange: true,
  allSelectableFieldsSelected: false,
  hasPartialFieldSelection: false,
  setAllFieldsSelected: vi.fn(),
  acceptedFieldCount: 0,
  rejectedFieldCount: 0,
  pendingFieldCount: 0,
  handleBatchAcceptAndGoPreview: vi.fn(),
  handleBatchRejectWithConfirm: vi.fn(),
  clearSelectedFields: vi.fn(),
  activeProposalFieldChanges: [],
  proposalFieldDecisions: {},
  getFieldDecisionActionKey: vi.fn().mockReturnValue("key"),
  fieldDecisionSubmitting: {},
  selectedFieldKeys: [],
  setFieldSelected: vi.fn(),
  submitFieldDecision: vi.fn(),
  normalizeSuggestionValue: (value: unknown) => String(value),
  isPreviewContentEditing: false,
  startPreviewContentEdit: vi.fn(),
  savePreviewContentEdit: vi.fn(),
  manualPreviewContentDraft: "",
  setManualPreviewContentDraft: vi.fn(),
};

describe("MemoryReviewPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContext.mockReturnValue(baseContext);
  });

  it("shows a loading state while the proposal is not yet resolved", () => {
    render(<MemoryReviewPage />);
    expect(screen.getByText("admin.memoryDiffDialogTitle")).toBeInTheDocument();
  });

  it("renders nothing when there is no active proposal and not requested", () => {
    mockContext.mockReturnValue({
      ...baseContext,
      isReviewRouteRequested: false,
      activeProposal: null,
    });
    const { container } = render(<MemoryReviewPage />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the backend suggestion choose step with suggestions", () => {
    const proposal = {
      tab: "preference",
      before: { content: "old content", autoEvo: false },
      backendSuggestions: [{ id: "s-1" }],
    };
    mockContext.mockReturnValue({
      ...baseContext,
      activeProposal: proposal,
      isBackendSuggestionReviewMode: true,
      activeReviewStep: 0,
      activeBackendSuggestions: [
        { id: "s-1", title: "Suggestion 1", content: "new content", action: "update" },
      ],
    });
    render(<MemoryReviewPage />);
    expect(
      screen.getByText((content) => content.includes("Suggestion 1")),
    ).toBeInTheDocument();
    expect(screen.getByText("admin.memoryDiffBatchAcceptAll")).toBeInTheDocument();
  });

  it("closes the review when clicking the close button", () => {
    const closeChangeReview = vi.fn();
    const proposal = {
      tab: "experience",
      before: { content: "old", autoEvo: false },
    };
    mockContext.mockReturnValue({
      ...baseContext,
      activeProposal: proposal,
      activeProposalDiff: { lines: [], isPreference: false },
      closeChangeReview,
    });
    render(<MemoryReviewPage />);
    fireEvent.click(screen.getByText("common.close"));
    expect(closeChangeReview).toHaveBeenCalledTimes(1);
  });

  it("renders the field-based proposal choose step with no changes empty state", () => {
    const proposal = {
      tab: "experience",
      before: { content: "old", autoEvo: false },
    };
    mockContext.mockReturnValue({
      ...baseContext,
      activeProposal: proposal,
      activeProposalDiff: { lines: [], isPreference: false },
    });
    render(<MemoryReviewPage />);
    expect(screen.getByText("admin.memoryDiffNoContentChange")).toBeInTheDocument();
  });

  it("submits a review question from the preview step", () => {
    const sendReviewQuestion = vi.fn();
    const setQaQuestionDraft = vi.fn();
    const proposal = {
      tab: "experience",
      before: { content: "old", autoEvo: false },
    };
    mockContext.mockReturnValue({
      ...baseContext,
      activeProposal: proposal,
      activeProposalDiff: { lines: [], isPreference: false },
      activeReviewStep: 1,
      qaQuestionDraft: "why?",
      sendReviewQuestion,
      setQaQuestionDraft,
    });
    render(<MemoryReviewPage />);
    fireEvent.click(screen.getByLabelText("chat.send"));
    expect(sendReviewQuestion).toHaveBeenCalledTimes(1);
  });
});
