import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import ExperienceOverview from "./index";
import { useMemoryManagementOutletContext } from "../../context";
import type { ExperienceAsset } from "../../shared";

vi.mock("../../context", () => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAsset = (overrides: Partial<ExperienceAsset> = {}): ExperienceAsset => ({
  id: "asset-1",
  title: "Working memory",
  content: "some content",
  ...overrides,
} as ExperienceAsset);

const baseContext = {
  t,
  filteredExperienceItems: [] as ExperienceAsset[],
  experienceLoading: false,
  experienceAutoEvoLoading: new Set<string>(),
  experienceFeatureEnabled: true,
  experienceSettingSaving: false,
  handleExperienceFeatureToggle: vi.fn(),
  handleExperienceAutoEvoToggle: vi.fn(),
  openChangeReview: vi.fn(),
  navigateToExperienceDetail: vi.fn(),
};

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<typeof vi.fn>;

describe("ExperienceOverview", () => {
  it("renders an empty state when there are no experience items", () => {
    mockContext.mockReturnValue(baseContext);
    render(<ExperienceOverview />);
    expect(screen.getByText("admin.memoryEmpty")).toBeInTheDocument();
  });

  it("renders a loading skeleton while loading with no items yet", () => {
    mockContext.mockReturnValue({ ...baseContext, experienceLoading: true });
    const { container } = render(<ExperienceOverview />);
    expect(container.querySelector(".is-loading")).not.toBeNull();
  });

  it("renders asset cards and pending section for pending items", () => {
    const pendingAsset = makeAsset({
      id: "asset-2",
      draftStatus: "pending",
      resourceType: "experience",
    } as any);
    mockContext.mockReturnValue({
      ...baseContext,
      filteredExperienceItems: [makeAsset(), pendingAsset],
    });
    render(<ExperienceOverview />);
    expect(screen.getByText("admin.memoryExperiencePendingTitle:{\"count\":1}")).toBeInTheDocument();
    expect(
      screen.getByText("admin.memoryExperiencePendingResources"),
    ).toBeInTheDocument();
  });

  it("renders the profile section for a user preference asset and edits fields", () => {
    const profileAsset = makeAsset({
      id: "profile-1",
      resourceType: "user_preference",
      agentPersona: "friendly",
      preferredName: "Alex",
    } as any);
    const navigateToExperienceDetail = vi.fn();
    mockContext.mockReturnValue({
      ...baseContext,
      filteredExperienceItems: [profileAsset],
      navigateToExperienceDetail,
    });
    render(<ExperienceOverview />);
    expect(screen.getByText("friendly")).toBeInTheDocument();
    expect(screen.getByText("Alex")).toBeInTheDocument();
    fireEvent.click(
      screen.getByLabelText(
        'admin.memoryProfileEditTitle:{"field":"admin.memoryProfileAgentPersona"}',
      ),
    );
    expect(navigateToExperienceDetail).toHaveBeenCalledWith("profile-1");
  });

  it("toggles the apply-in-answers switch", () => {
    const handleExperienceFeatureToggle = vi.fn();
    mockContext.mockReturnValue({
      ...baseContext,
      filteredExperienceItems: [makeAsset()],
      handleExperienceFeatureToggle,
    });
    render(<ExperienceOverview />);
    fireEvent.click(
      screen.getByLabelText("admin.memoryExperienceApplyInAnswers"),
    );
    expect(handleExperienceFeatureToggle).toHaveBeenCalledWith(false);
  });
});
