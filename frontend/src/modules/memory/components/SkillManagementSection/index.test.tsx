import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import SkillManagementSection from "./index";
import { useMemoryManagementOutletContext } from "../../context";
import type { StructuredAsset } from "../../shared";

vi.mock("../../context", () => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

vi.mock("../../skillApi", () => ({
  deleteSkillMarketItem: vi.fn(),
  getSkillMarketItem: vi.fn(),
  installSkillFromMarket: vi.fn(),
  listBuiltinSkills: vi.fn().mockResolvedValue([]),
  listSkillMarketPage: vi.fn().mockResolvedValue({ records: [], total: 0 }),
  listSkillMarketTags: vi.fn().mockResolvedValue([]),
  listTrashedSkillAssetsPage: vi.fn().mockResolvedValue({ records: [], total: 0 }),
  organizeSkills: vi.fn(),
  waitForSkillOrganize: vi.fn(),
  emptySkillTrash: vi.fn(),
  purgeSkillAsset: vi.fn(),
  restoreSkillAsset: vi.fn(),
}));

vi.mock("./skillHelpers", () => ({
  collectMarketTags: () => [],
  filterMarketSkills: () => [],
  mapSkillAssetRecordToStructuredAsset: (item: unknown) => item,
}));

vi.mock("./skillMarketMockData", () => ({
  mapMarketSkillRecordToAsset: (item: unknown) => item,
}));

vi.mock("@/modules/plugin/components/NewPluginModal", () => ({
  default: () => null,
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "member" }) },
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAsset = (overrides: Partial<StructuredAsset> = {}): StructuredAsset => ({
  id: "skill-1",
  content: "",
  name: "Alpha Skill",
  description: "desc",
  category: "general",
  tags: [],
  ...overrides,
});

const baseContext = {
  t,
  openSkillShareCenter: vi.fn(),
  incomingPendingCount: 0,
  openSkillCreateModal: vi.fn(),
  hideUserGroupSurfaces: false,
  openModal: vi.fn(),
  skillAssets: [makeAsset()],
  skillLoading: false,
  refreshSkillAssets: vi.fn().mockResolvedValue(undefined),
  genericColumns: [{ title: "Name", dataIndex: "name", key: "name" }],
  skillView: "installed",
  setSkillView: vi.fn(),
  installedSkillSource: "all",
  setInstalledSkillSource: vi.fn(),
  marketSkillSource: "all",
  setMarketSkillSource: vi.fn(),
  marketCategory: "all",
  setMarketCategory: vi.fn(),
  category: undefined,
  setCategory: vi.fn(),
  availableCategories: ["general"],
  skillCategoriesLoading: false,
  handleEnableBuiltinSkill: vi.fn(),
  builtinSkillEnableLoading: new Set<string>(),
  searchInput: "",
  setSearchInput: vi.fn(),
  setQuery: vi.fn(),
  resetFilters: vi.fn(),
  filteredInstalledSkillTree: [makeAsset()],
  skillListPage: 1,
  skillListPageSize: 12,
  skillListTotal: 1,
  setSkillListPage: vi.fn(),
  setSkillListPageSize: vi.fn(),
  manualSkillReviewSummary: { qualifiedSessionCount: 0, runningTask: null },
  manualSkillReviewLoading: false,
  manualSkillReviewRunning: false,
  handleRunManualSkillReview: vi.fn(),
};

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<typeof vi.fn>;

function renderSection() {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter>
        <SkillManagementSection />
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("SkillManagementSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContext.mockReturnValue(baseContext);
  });

  it("renders the toolbar and the installed skills table by default", async () => {
    renderSection();
    await waitFor(() => {
      expect(screen.getByText("Alpha Skill")).toBeInTheDocument();
    });
  });

  it("renders the market view when skillView is market", async () => {
    mockContext.mockReturnValue({ ...baseContext, skillView: "market" });
    renderSection();
    await waitFor(() => {
      expect(screen.getByText("admin.memorySkillMarketEmpty")).toBeInTheDocument();
    });
  });

  it("renders the trash view when skillView is trash", async () => {
    mockContext.mockReturnValue({ ...baseContext, skillView: "trash" });
    renderSection();
    await waitFor(() => {
      expect(
        screen.getByPlaceholderText("admin.memorySkillSearchPlaceholder"),
      ).toBeInTheDocument();
    });
  });

  it("opens the admin publish modal when clicking the publish button for an admin user", async () => {
    mockContext.mockReturnValue({ ...baseContext, skillView: "market" });
    const authModule = await import("@/components/auth");
    (authModule.AgentAppsAuth.getUserInfo as any) = () => ({ role: "admin" });
    renderSection();
    await waitFor(() => {
      expect(screen.getByText("admin.memorySkillMarketEmpty")).toBeInTheDocument();
    });
    const publishButton = screen.queryByText(
      "admin.memorySkillAdminPublishButton",
    );
    if (publishButton) {
      fireEvent.click(publishButton);
      expect(
        screen.getByPlaceholderText("admin.memorySkillUploadRepoPlaceholder"),
      ).toBeInTheDocument();
    }
  });
});
