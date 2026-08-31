import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const skillApiMocks = vi.hoisted(() => ({
  deleteSkillMarketItem: vi.fn(),
  getRunningSkillOrganizeTask: vi.fn(),
  getSkillMarketItem: vi.fn(),
  installSkillFromMarket: vi.fn(),
  listBuiltinSkills: vi.fn(),
  listSkillMarketPage: vi.fn(),
  listSkillMarketTags: vi.fn(),
  organizeSkills: vi.fn(),
  waitForSkillOrganize: vi.fn(),
}));
const contextMocks = vi.hoisted(() => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

vi.mock("../../skillApi", () => skillApiMocks);
vi.mock("../../context", () => contextMocks);
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "user" }) },
}));
vi.mock("./SkillManagementToolbar", () => ({
  default: ({
    organizeDisabled,
    organizeStatus,
  }: {
    organizeDisabled: boolean;
    organizeStatus: string;
  }) => (
    <div>
      <span data-testid="organize-status">{organizeStatus}</span>
      <span data-testid="organize-disabled">{String(organizeDisabled)}</span>
    </div>
  ),
}));
vi.mock("./SkillInstalledView", () => ({ default: () => null }));
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
    refreshSkillAssets.mockResolvedValue(undefined);
    contextMocks.useMemoryManagementOutletContext.mockReturnValue({
      t: (key: string) => key,
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
});
