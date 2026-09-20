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
  waitForSkillOrganize: vi.fn(),
}));
const viewMocks = vi.hoisted(() => ({ props: {} as Record<string, any> }));
const contextMocks = vi.hoisted(() => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

vi.mock("../../skillApi", () => skillApiMocks);
vi.mock("../../context", () => contextMocks);
vi.mock("./skillDraftReview", () => ({ listPendingSkillDrafts: vi.fn().mockResolvedValue([]) }));
vi.mock("./SkillDraftReviewPanel", () => ({ default: () => null }));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "user" }) },
}));
vi.mock("./SkillManagementToolbar", () => ({
  default: ({
    organizeDisabled,
    organizeStatus,
    onOrganizeSkills,
  }: {
    organizeDisabled: boolean;
    organizeStatus: string;
    onOrganizeSkills: (mode: "light" | "deep") => void;
  }) => (
    <div>
      <button onClick={() => onOrganizeSkills("light")}>organize</button>
      <button onClick={() => onOrganizeSkills("deep")}>organize-deep</button>
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
    refreshSkillAssets.mockResolvedValue(undefined);
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
    });
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
