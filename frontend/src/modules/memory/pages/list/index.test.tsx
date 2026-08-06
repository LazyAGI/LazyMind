import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import MemoryManagementListPage from "./index";
import { useMemoryManagementOutletContext } from "../../context";

vi.mock("../../context", () => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

vi.mock("../../components/SkillManagementSection", () => ({
  default: () => <div data-testid="skill-management-section" />,
}));

vi.mock("../../components/ExperienceOverview", () => ({
  default: () => <div data-testid="experience-overview" />,
}));

vi.mock("../../components/GlossaryListSection", () => ({
  default: () => <div data-testid="glossary-list-section" />,
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<
  typeof vi.fn
>;

const baseContext = {
  t,
  activeTab: "glossary",
  glossaryChangeProposals: [],
  openModal: vi.fn(),
  currentTabMeta: { unit: "entry" },
  memoryTabOrder: ["glossary", "skills", "experience"],
  tabMeta: {
    glossary: { icon: "G", title: "Glossary", description: "glossary desc" },
    skills: { icon: "S", title: "Skills", description: "skills desc" },
    experience: { icon: "E", title: "Experience", description: "experience desc" },
  },
  setActiveTab: vi.fn(),
  setGlossaryDetailTarget: vi.fn(),
  setGlossaryInboxOpen: vi.fn(),
  resetFilters: vi.fn(),
  navigateToMemoryList: vi.fn(),
  searchInput: "",
  setSearchInput: vi.fn(),
  query: "",
  setQuery: vi.fn(),
  glossarySource: undefined,
  setGlossarySource: vi.fn(),
  availableGlossarySourceOptions: [],
  selectedGlossaryAssets: [],
  glossaryAssets: [],
  glossaryLoading: false,
  glossaryListPage: 1,
  glossaryListPageSize: 12,
  glossaryListTotal: 0,
  glossaryLoadError: "",
  refreshGlossaryAssets: vi.fn(),
  handleBatchMergeGlossary: vi.fn(),
  handleBatchDeleteGlossary: vi.fn(),
  filteredGlossaryItems: [],
  glossaryColumns: [],
  selectedGlossaryAssetIds: [],
  setGlossaryListPage: vi.fn(),
  setGlossaryListPageSize: vi.fn(),
  setSelectedGlossaryAssetIds: vi.fn(),
};

describe("MemoryManagementListPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContext.mockReturnValue(baseContext);
  });

  it("renders the glossary section when activeTab is glossary", () => {
    render(<MemoryManagementListPage />);
    expect(screen.getByTestId("glossary-list-section")).toBeInTheDocument();
    expect(screen.getByText("Glossary")).toBeInTheDocument();
  });

  it("renders the skill management section when activeTab is skills", () => {
    mockContext.mockReturnValue({ ...baseContext, activeTab: "skills" });
    render(<MemoryManagementListPage />);
    expect(screen.getByTestId("skill-management-section")).toBeInTheDocument();
  });

  it("renders the experience overview when activeTab is experience", () => {
    mockContext.mockReturnValue({ ...baseContext, activeTab: "experience" });
    render(<MemoryManagementListPage />);
    expect(screen.getByTestId("experience-overview")).toBeInTheDocument();
  });

  it("switches tabs and resets filters when clicking a tab card", () => {
    const setActiveTab = vi.fn();
    const resetFilters = vi.fn();
    const navigateToMemoryList = vi.fn();
    mockContext.mockReturnValue({
      ...baseContext,
      setActiveTab,
      resetFilters,
      navigateToMemoryList,
    });
    render(<MemoryManagementListPage />);
    fireEvent.click(screen.getByText("Skills"));
    expect(setActiveTab).toHaveBeenCalledWith("skills");
    expect(resetFilters).toHaveBeenCalledTimes(1);
    expect(navigateToMemoryList).toHaveBeenCalledWith("skills");
  });

  it("opens the glossary inbox when clicking the inbox button", () => {
    const setGlossaryInboxOpen = vi.fn();
    mockContext.mockReturnValue({ ...baseContext, setGlossaryInboxOpen });
    render(<MemoryManagementListPage />);
    fireEvent.click(
      screen.getByText(
        'admin.memoryGlossaryInboxButton:{"count":0}',
      ),
    );
    expect(setGlossaryInboxOpen).toHaveBeenCalledWith(true);
  });

  it("triggers the reset button in the filter bar", () => {
    const resetFilters = vi.fn();
    mockContext.mockReturnValue({ ...baseContext, resetFilters });
    render(<MemoryManagementListPage />);
    fireEvent.click(screen.getByText("admin.memoryReset"));
    expect(resetFilters).toHaveBeenCalledTimes(1);
  });
});
