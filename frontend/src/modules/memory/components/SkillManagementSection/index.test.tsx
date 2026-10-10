import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const skillApiMocks = vi.hoisted(() => ({
  deleteSkillMarketItem: vi.fn(),
  getRunningSkillOrganizeTask: vi.fn(),
  getSkillMarketItem: vi.fn(),
  installSkillFromMarket: vi.fn(),
  isSkillOrganizeTerminalStatus: (status: string) =>
    ["completed", "done", "failed", "skipped"].includes(status),
  listBuiltinSkills: vi.fn(),
  listSkillMarketPage: vi.fn(),
  listSkillMarketTags: vi.fn(),
  organizeSkills: vi.fn(),
  cancelSkillOrganizeTask: vi.fn(),
  listSkillOrganizeTasks: vi.fn(),
  waitForSkillOrganize: vi.fn(),
}));
const viewMocks = vi.hoisted(() => ({ props: {} as Record<string, any>, review: {} as Record<string, any>, approval: {} as Record<string, any> }));
const contextMocks = vi.hoisted(() => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

vi.mock("../../skillApi", () => skillApiMocks);
vi.mock("../../context", () => contextMocks);
vi.mock("./skillDraftReview", () => ({ listPendingSkillDrafts: vi.fn().mockResolvedValue([]) }));
vi.mock("./SkillDraftReviewPanel", () => ({ default: (props: Record<string, any>) => { viewMocks.review = props; return <span>draft-review</span>; } }));
vi.mock("./SkillOrganizeApprovalPanel", () => ({ default: (props: Record<string, any>) => { viewMocks.approval = props; return props.expanded ? <span>organize-approval</span> : null; } }));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "user" }) },
}));
vi.mock("./SkillManagementToolbar", () => ({
  default: ({
    organizeDisabled,
    organizeStatus,
    onOrganizeSkills,
    onOrganizeCancelRun,
  }: {
    organizeDisabled: boolean;
    organizeStatus: string;
    onOrganizeSkills: (mode: "light" | "deep") => void;
    onOrganizeCancelRun: () => void;
  }) => (
    <div>
      <button onClick={() => onOrganizeSkills("light")}>organize</button>
      <button onClick={() => onOrganizeSkills("deep")}>organize-deep</button>
      <button onClick={onOrganizeCancelRun}>cancel-run</button>
      <span data-testid="organize-status">{organizeStatus}</span>
      <span data-testid="organize-disabled">{String(organizeDisabled)}</span>
    </div>
  ),
}));
vi.mock("./SkillInstalledView", () => ({ default: (props: Record<string, any>) => { viewMocks.props = props; return <output data-testid="selected">{props.selectedOrganizeSkillIds.join(",")}</output>; } }));
vi.mock("./SkillMarketView", () => ({ default: () => null }));
vi.mock("./SkillAdminPublishModal", () => ({ default: () => null }));
vi.mock("./WorkflowInstalledView", () => ({ default: () => null }));
vi.mock("./skillHelpers", () => ({
  collectMarketTags: () => [],
  filterMarketSkills: () => [],
}));
vi.mock("./skillMarketMockData", () => ({
  mapMarketSkillRecordToAsset: (record: unknown) => record,
}));
vi.mock("./collaborationVisibility", () => ({
  shouldShowSkillMessageCenter: () => false,
}));
vi.mock("./skillCategoryIcon", () => ({ renderSkillCategoryIcon: () => null }));
vi.mock("@/modules/workflow/components/NewWorkflowModal", () => ({
  default: () => null,
}));

import SkillManagementSection from ".";

const refreshSkillAssets = vi.fn();

describe("SkillManagementSection organize task recovery", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue(null);
    refreshSkillAssets.mockReset().mockResolvedValue(undefined);
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({
      t: (key: string, options?: Record<string, unknown>) => options ? `${key}: ${JSON.stringify(options)}` : key,
      openSkillShareCenter: vi.fn(),
      incomingPendingCount: 0,
      openSkillCreateModal: vi.fn(),
      hideUserGroupSurfaces: false,
      openModal: vi.fn(),
      skillAssets: [],
      skillLoading: false,
      refreshSkillAssets,
      genericColumns: [],
      skillView: "installed",
      setSkillView: vi.fn(),
      marketSkillSource: "all",
      setMarketSkillSource: vi.fn(),
      marketCategory: "all",
      setMarketCategory: vi.fn(),
      category: undefined,
      setCategory: vi.fn(),
      availableCategories: [],
      skillCategoriesLoading: false,
      handleEnableBuiltinSkill: vi.fn(),
      builtinSkillEnableLoading: new Set<string>(),
      searchInput: "",
      setSearchInput: vi.fn(),
      setQuery: vi.fn(),
      resetFilters: vi.fn(),
      filteredInstalledSkillTree: [],
      skillListPage: 1,
      skillListPageSize: 10,
      skillListTotal: 2,
      setSkillListPage: vi.fn(),
      setSkillListPageSize: vi.fn(),
      manualSkillReviewSummary: null,
      manualSkillReviewLoading: false,
      manualSkillReviewRunning: false,
      handleRunManualSkillReview: vi.fn(),
    });
  });

  it("shows blocking skills from a 409 and opens existing draft review with refresh after acceptance", async () => {
    skillApiMocks.organizeSkills.mockRejectedValue({response: {status: 409, data: {message: "draft conflict", data: {
      code: "skill_organize_draft_conflict", blocking_skills: ["skills/internal/B", "skills/internal/C"],
    }}}});
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", {name: "organize"}));
    act(() => viewMocks.props.onOrganizeSelectionChange(["B", "C"].map(id => ({id, name: id, category: "internal"})), true));
    await act(async () => viewMocks.props.onOrganizeSubmit("light"));
    expect(screen.getByText("skills/internal/B")).toBeVisible();
    expect(screen.getByText("skills/internal/C")).toBeVisible();
    fireEvent.click(screen.getByRole("button", {name: "admin.memorySkillDraftReviewTitle"}));
    expect(screen.getByText("draft-review")).toBeVisible();
    await act(async () => viewMocks.review.onApplied());
    expect(refreshSkillAssets).toHaveBeenCalledWith({preserveChangeProposals: true});
  });

  it("cancels a recovered task and ignores late polling after a new task starts", async () => {
    let resolveOld!: (task: unknown) => void;
    let confirmCancel!: () => void;
    let rejectRefresh!: (error: Error) => void;
    refreshSkillAssets.mockImplementationOnce(() => new Promise((_resolve, reject) => {rejectRefresh = reject;}));
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "old", status: "organize_draft"});
    skillApiMocks.waitForSkillOrganize.mockImplementation(() => new Promise(resolve => {resolveOld = resolve;}));
    skillApiMocks.cancelSkillOrganizeTask.mockImplementation(() => new Promise<void>(resolve => {confirmCancel = resolve;}));
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await waitFor(() => expect(screen.getByTestId("organize-status")).toHaveTextContent("running"));
    fireEvent.click(screen.getByRole("button", {name: "cancel-run"}));
    expect(screen.getByTestId("organize-disabled")).toHaveTextContent("true");
    await act(async () => confirmCancel());
    await waitFor(() => expect(screen.getByTestId("organize-disabled")).toHaveTextContent("false"));
    expect(skillApiMocks.cancelSkillOrganizeTask).toHaveBeenCalledWith("old");
    expect(refreshSkillAssets).toHaveBeenCalledWith({page: 1, preserveChangeProposals: true, background: true, signal: expect.any(AbortSignal)});
    const refreshSignal = refreshSkillAssets.mock.calls[0][0].signal as AbortSignal;
    skillApiMocks.organizeSkills.mockResolvedValue({requestId: "new", taskId: "new-task"});
    skillApiMocks.waitForSkillOrganize.mockReturnValue(new Promise(() => {}));
    fireEvent.click(screen.getByRole("button", {name: "organize"}));
    act(() => viewMocks.props.onOrganizeSelectionChange(["A", "B"].map(id => ({id, name: id, category: "internal"})), true));
    act(() => { void viewMocks.props.onOrganizeSubmit("light"); });
    await waitFor(() => expect(skillApiMocks.waitForSkillOrganize).toHaveBeenCalledWith("new", expect.any(AbortSignal), expect.any(Function)));
    expect(refreshSignal.aborted).toBe(true);
    await act(async () => rejectRefresh(new Error("late refresh failure")));
    await act(async () => resolveOld({status: "completed"}));
    expect(screen.getByTestId("organize-status")).toHaveTextContent("running");
    expect(screen.getByTestId("organize-disabled")).toHaveTextContent("true");
    expect(refreshSkillAssets).toHaveBeenCalledTimes(1);
  });

  it("preserves a running task's later failure when draft acceptance refresh finishes", async () => {
    const context = contextMocks.useMemoryManagementOutletContext.getMockImplementation()!();
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({...context, skillAssets: [{id: "draft", draft: {hasUncommittedDraft: true}}]});
    let finishTask!: (task: unknown) => void;
    let finishRefresh!: () => void;
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "running", status: "organize_draft"});
    skillApiMocks.waitForSkillOrganize.mockImplementation(() => new Promise(resolve => {finishTask = resolve;}));
    refreshSkillAssets.mockImplementationOnce(() => new Promise<void>(resolve => {finishRefresh = resolve;}));
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await waitFor(() => expect(screen.getByTestId("organize-status")).toHaveTextContent("running"));
    fireEvent.click(screen.getByRole("button", {name: "admin.memorySkillDraftReviewTitle"}));
    let applied!: Promise<void>;
    act(() => { applied = viewMocks.review.onApplied(); });
    await act(async () => finishTask({status: "failed", errorCode: "skill_organize_model_timeout", error: "timeout"}));
    await act(async () => { finishRefresh(); await applied; });
    act(() => viewMocks.review.onClose());
    expect(screen.getByTestId("organize-status")).toHaveTextContent("error");
    expect(screen.getByText("admin.memorySkillOrganizeModelTimeout")).toBeVisible();
  });

  it("refreshes cancelled partial drafts so the same skills can be organized again", async () => {
    const drafts = ["A", "B"].map(id => ({id, name: id, category: "internal", draft: {hasUncommittedDraft: true, taskId: "old", version: 1}}));
    const context = contextMocks.useMemoryManagementOutletContext.getMockImplementation()!();
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({...context, skillAssets: drafts});
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "old", status: "organize_draft"});
    skillApiMocks.waitForSkillOrganize.mockReturnValue(new Promise(() => {}));
    skillApiMocks.cancelSkillOrganizeTask.mockResolvedValue(undefined);
    const {rerender} = render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await waitFor(() => expect(screen.getByTestId("organize-status")).toHaveTextContent("running"));
    act(() => viewMocks.props.onSkillSelectionChange(drafts, true));
    refreshSkillAssets.mockImplementationOnce(async () => {
      contextMocks.useMemoryManagementOutletContext.mockReturnValue({...context, skillAssets: drafts.map(skill => ({...skill, draft: {...skill.draft, hasUncommittedDraft: false}}))});
      rerender(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    });
    fireEvent.click(screen.getByRole("button", {name: "cancel-run"}));
    await waitFor(() => expect(screen.getByTestId("organize-disabled")).toHaveTextContent("false"));
    fireEvent.click(screen.getByRole("button", {name: "organize"}));
    expect(screen.getByTestId("selected")).toHaveTextContent(/^A,B$/);
  });

  it("keeps polling and blocks new work when cancellation fails, then permits a confirmed retry", async () => {
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "old", status: "organize_draft"});
    skillApiMocks.waitForSkillOrganize.mockReturnValue(new Promise(() => {}));
    skillApiMocks.cancelSkillOrganizeTask.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await waitFor(() => expect(screen.getByTestId("organize-status")).toHaveTextContent("running"));
    const signal = skillApiMocks.waitForSkillOrganize.mock.calls[0][1] as AbortSignal;
    fireEvent.click(screen.getByRole("button", {name: "cancel-run"}));
    await screen.findByText("admin.memorySkillOrganizeFailed");
    expect(signal.aborted).toBe(false);
    expect(screen.getByTestId("organize-disabled")).toHaveTextContent("true");
    fireEvent.click(screen.getByRole("button", {name: "cancel-run"}));
    await waitFor(() => expect(screen.getByTestId("organize-disabled")).toHaveTextContent("false"));
    expect(signal.aborted).toBe(true);
  });

  it("displays a typed failure reported by task polling", async () => {
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "failed-model", status: "organize_plan"});
    skillApiMocks.waitForSkillOrganize.mockResolvedValue({status: "failed", errorCode: "skill_organize_model_transport", error: "provider unavailable"});
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    expect(await screen.findByText("admin.memorySkillOrganizeModelUnavailable")).toBeVisible();
  });

  it.each(["response", "polling"])("warns about retained drafts via %s without blocking other organize work", async (source) => {
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "old", status: "organize_draft"});
    skillApiMocks.waitForSkillOrganize.mockReturnValue(source === "polling"
      ? Promise.resolve({status: "cancelled", pendingReview: true})
      : new Promise(() => {}));
    skillApiMocks.cancelSkillOrganizeTask.mockResolvedValue({pendingReview: true});
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    if (source === "response") {
      await waitFor(() => expect(screen.getByTestId("organize-status")).toHaveTextContent("running"));
      fireEvent.click(screen.getByRole("button", {name: "cancel-run"}));
    }
    expect(await screen.findByText("admin.memorySkillOrganizeCancelledPendingReview")).toBeVisible();
    expect(screen.getByTestId("organize-disabled")).toHaveTextContent("false");
    fireEvent.click(screen.getByRole("button", {name: "organize"}));
    expect(viewMocks.props.organizeMode).toBe(true);
    expect(screen.getByText("admin.memorySkillOrganizeCancelledPendingReview")).toBeVisible();
    act(() => viewMocks.props.onOrganizeSelectionChange(["A", "B"].map(id => ({id, name: id, category: "internal"})), true));
    skillApiMocks.organizeSkills.mockResolvedValue({requestId: "next", taskId: "next-task"});
    skillApiMocks.waitForSkillOrganize.mockResolvedValue({status: "failed", errorCode: "skill_organize_model_timeout", error: "timeout"});
    await act(async () => viewMocks.props.onOrganizeSubmit("light"));
    expect(skillApiMocks.organizeSkills).toHaveBeenCalledWith(["skills/internal/A", "skills/internal/B"], "light");
    expect(screen.getByText("admin.memorySkillOrganizeModelTimeout")).toBeVisible();
    expect(screen.getByText("admin.memorySkillOrganizeCancelledPendingReview")).toBeVisible();
  });

  it("does not warn about preserved drafts after a complete cancellation rollback", async () => {
    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({requestId: "old", status: "organize_draft"});
    skillApiMocks.waitForSkillOrganize.mockResolvedValue({status: "cancelled", pendingReview: false});
    render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await waitFor(() => expect(screen.getByTestId("organize-status")).toHaveTextContent("skipped"));
    expect(screen.queryByText("admin.memorySkillOrganizeCancelledPendingReview")).not.toBeInTheDocument();
    expect(screen.getByTestId("organize-disabled")).toHaveTextContent("false");
  });

  it("refreshes selected draft eligibility after accepting one of three packages", async () => {
    const drafts = ["A", "B", "C"].map(id => ({id, name: id, category: "internal", draft: {hasUncommittedDraft: true, taskId: "org_previous", version: 1}}));
    const context = contextMocks.useMemoryManagementOutletContext.getMockImplementation()!();
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({...context, skillAssets: drafts});
    const {rerender} = render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await act(async () => {});
    act(() => viewMocks.props.onSkillSelectionChange(drafts, true));
    const accepted = {...drafts[0], draft: {...drafts[0].draft, hasUncommittedDraft: false}};
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({...context, skillAssets: [accepted, drafts[1], drafts[2]]});
    rerender(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", {name: "organize"}));
    expect(screen.getByTestId("selected")).toHaveTextContent(/^A$/);
    expect(screen.getByText("admin.memorySkillOrganizePendingDraftHint")).toBeVisible();
  });

  it("restores and follows a running organize task when the page mounts", async () => {
    let completeTask: ((task: {
      task: null;
      requestId: string;
      status: "completed";
      runStatus: string;
      resultCount: number;
    }) => void) | undefined;

    skillApiMocks.getRunningSkillOrganizeTask.mockResolvedValue({
      task: null,
      requestId: "request-running",
      status: "organize_draft",
      runStatus: "organize_draft",
      resultCount: 0,
    });
    skillApiMocks.waitForSkillOrganize.mockReturnValue(
      new Promise((resolve) => {
        completeTask = resolve;
      }),
    );

    render(
      <MemoryRouter>
        <SkillManagementSection />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByTestId("organize-status")).toHaveTextContent("running");
      expect(screen.getByTestId("organize-disabled")).toHaveTextContent("true");
    });
    expect(skillApiMocks.waitForSkillOrganize).toHaveBeenCalledWith(
      "request-running",
      expect.any(AbortSignal),
      expect.any(Function),
    );

    await act(async () => {
      completeTask?.({
        task: null,
        requestId: "request-running",
        status: "completed",
        runStatus: "completed",
        resultCount: 1,
      });
    });

    await waitFor(() => {
      expect(screen.getByTestId("organize-status")).toHaveTextContent("success");
      expect(screen.getByTestId("organize-disabled")).toHaveTextContent("false");
      expect(screen.getByText("organize-approval")).toBeVisible();
    });
    expect(viewMocks.approval.hiddenRequestId).toBe("");
    expect(refreshSkillAssets).toHaveBeenCalledWith({ page: 1 });
  });

  it("inherits all editable local rows for light and reports off-page removals for deep", async () => {
    const skill = (id: string, category: string, extra = {}) => ({ id, name: id, category, content: "", description: "", tags: [], ...extra });
    const firstPage = [skill("internal-a", "internal"), skill("builtin-a", "internal", { originBuiltinSkillUid: "builtin" }), skill("external-a", "external")];
    const nextPage = [skill("internal-b", "internal"), skill("legacy-b", "learning"), skill("readonly", "internal", { readonly: true }), skill("cloud", "internal", { cloudResourceId: "cloud" })];
    const context = contextMocks.useMemoryManagementOutletContext.getMockImplementation()!();
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({ ...context, skillAssets: firstPage, filteredInstalledSkillTree: firstPage });
    const { rerender } = render(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    await act(async () => {});
    act(() => viewMocks.props.onSkillSelectionChange(firstPage, true));
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({ ...context, skillAssets: nextPage, filteredInstalledSkillTree: nextPage, skillListPage: 2 });
    rerender(<MemoryRouter><SkillManagementSection /></MemoryRouter>);
    act(() => viewMocks.props.onSkillSelectionChange(nextPage, true));
    fireEvent.click(screen.getByRole("button", { name: "organize" }));
    expect(viewMocks.props.organizeDepth).toBe("light");
    expect(context.setCategory).not.toHaveBeenCalledWith("internal");
    expect(screen.getByTestId("selected")).toHaveTextContent("internal-a,builtin-a,external-a,internal-b,legacy-b");
    fireEvent.click(screen.getByRole("button", { name: "organize-deep" }));
    expect(viewMocks.props.organizeDepth).toBe("deep");
    expect(context.setCategory).toHaveBeenCalledWith("internal");
    expect(screen.getByTestId("selected")).toHaveTextContent("internal-a,internal-b");
    const notice = await screen.findByText(/memorySkillOrganizeSelectionRemoved/);
    expect(notice).toHaveTextContent('"count":3');
    for (const name of ["builtin-a", "external-a", "legacy-b"]) expect(notice).toHaveTextContent(name);
    act(() => viewMocks.props.onOrganizeSelectionChange([firstPage[1], nextPage[1]], true));
    expect(screen.getByTestId("selected")).toHaveTextContent(/^internal-a,internal-b$/);
    await act(async () => viewMocks.props.onOrganizeSubmit("light"));
    expect(skillApiMocks.organizeSkills).not.toHaveBeenCalled();
    skillApiMocks.organizeSkills.mockResolvedValue({ requestId: "r", taskId: "t" });
    skillApiMocks.waitForSkillOrganize.mockResolvedValue({ status: "completed", resultCount: 1 });
    await act(async () => viewMocks.props.onOrganizeSubmit("deep"));
    expect(skillApiMocks.organizeSkills).toHaveBeenCalledWith(["skills/internal/internal-a", "skills/internal/internal-b"], "deep");
  });
});
