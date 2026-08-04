import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import MemoryExperienceDetailPage from "./index";
import { useMemoryManagementOutletContext } from "../../context";

vi.mock("../../context", () => ({
  useMemoryManagementOutletContext: vi.fn(),
}));

// @/components/ui is a barrel that also re-exports RenderPdf, which pulls in
// react-pdf/pdfjs-dist and needs browser canvas APIs jsdom lacks. Mock the
// barrel down to a minimal DetailPageHeader stub instead.
vi.mock("@/components/ui", () => ({
  DetailPageHeader: ({
    title,
    description,
    settingsMenu,
    onBack,
  }: {
    title?: ReactNode;
    description?: ReactNode;
    settingsMenu?: ReactNode;
    onBack?: () => void;
  }) => (
    <div>
      <button type="button" onClick={onBack}>
        back
      </button>
      <div>{title}</div>
      <div>{description}</div>
      <div>{settingsMenu}</div>
    </div>
  ),
}));

vi.mock("../../components/personalResource/PersonalResourceContentEditor", () => ({
  default: ({ resourceType }: { resourceType: string }) => (
    <div data-testid="content-editor">{resourceType}</div>
  ),
}));

vi.mock("../../components/ResourceVersionDrawer", () => ({
  default: ({ open }: { open: boolean }) =>
    open ? <div data-testid="version-drawer" /> : null,
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<
  typeof vi.fn
>;

const experience = {
  id: "exp-1",
  title: "My Experience",
  content: "content",
  resourceType: "memory",
  protect: false,
};

const baseContext = {
  t,
  experienceAssets: [experience],
  experienceInitialized: true,
  navigateToMemoryList: vi.fn(),
  refreshExperienceSection: vi.fn().mockResolvedValue(undefined),
};

function renderPage(itemId = "exp-1") {
  return render(
    <MemoryRouter initialEntries={[`/memory/experience/${itemId}`]}>
      <Routes>
        <Route
          path="/memory/experience/:itemId"
          element={<MemoryExperienceDetailPage />}
        />
      </Routes>
    </MemoryRouter>,
  );
}

describe("MemoryExperienceDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContext.mockReturnValue(baseContext);
  });

  it("renders the experience title and content editor", () => {
    renderPage();
    expect(screen.getAllByText("My Experience").length).toBeGreaterThan(0);
    expect(screen.getByTestId("content-editor")).toHaveTextContent("memory");
  });

  it("shows an empty state when the experience cannot be found", () => {
    renderPage("missing-id");
    expect(
      screen.getAllByText("admin.memoryDiffTargetMissing").length,
    ).toBeGreaterThan(0);
  });

  it("shows a loading state while experience data is not initialized", () => {
    mockContext.mockReturnValue({
      ...baseContext,
      experienceAssets: [],
      experienceInitialized: false,
    });
    renderPage();
    expect(
      screen.getByText("admin.memoryExperienceDetailTitle"),
    ).toBeInTheDocument();
  });

  it("opens the version history drawer when clicking the history button", () => {
    renderPage();
    fireEvent.click(screen.getByText("admin.memoryVersionHistoryButton"));
    expect(screen.getByTestId("version-drawer")).toBeInTheDocument();
  });
});
