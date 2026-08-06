import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import MemoryGlossaryDetailPage from "./index";
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
    onBack,
  }: {
    title?: ReactNode;
    description?: ReactNode;
    onBack?: () => void;
  }) => (
    <div>
      <button type="button" onClick={onBack}>
        back
      </button>
      <div>{title}</div>
      <div>{description}</div>
    </div>
  ),
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<
  typeof vi.fn
>;

const glossaryTarget = {
  id: "glossary-1",
  term: "API",
  aliases: ["Application Programming Interface"],
  content: "An interface for programmatic access.",
  source: "manual",
};

const baseContext = {
  t,
  glossaryRouteItemId: "glossary-1",
  glossaryDetailTarget: glossaryTarget,
  glossaryDetailExists: true,
  closeGlossaryDetail: vi.fn(),
  openModal: vi.fn(),
  glossarySourceColorMap: { manual: "blue" },
  glossarySourceLabelMap: { manual: "Manual" },
};

describe("MemoryGlossaryDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContext.mockReturnValue(baseContext);
  });

  it("renders the glossary term, aliases and content", () => {
    render(<MemoryGlossaryDetailPage />);
    expect(screen.getAllByText("API").length).toBeGreaterThan(0);
    expect(
      screen.getByText("Application Programming Interface"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("An interface for programmatic access."),
    ).toBeInTheDocument();
  });

  it("opens the edit modal when clicking the edit button", () => {
    const openModal = vi.fn();
    mockContext.mockReturnValue({ ...baseContext, openModal });
    render(<MemoryGlossaryDetailPage />);
    fireEvent.click(screen.getByText("admin.memoryEditItem"));
    expect(openModal).toHaveBeenCalledWith("edit", glossaryTarget);
  });

  it("shows a loading state while the target is not yet resolved", () => {
    mockContext.mockReturnValue({
      ...baseContext,
      glossaryDetailTarget: null,
    });
    render(<MemoryGlossaryDetailPage />);
    expect(
      screen.getByText("admin.memoryGlossaryDetailTitle"),
    ).toBeInTheDocument();
  });

  it("renders nothing when there is no route item id and no target", () => {
    mockContext.mockReturnValue({
      ...baseContext,
      glossaryRouteItemId: "",
      glossaryDetailTarget: null,
    });
    const { container } = render(<MemoryGlossaryDetailPage />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a placeholder dash when there are no aliases", () => {
    mockContext.mockReturnValue({
      ...baseContext,
      glossaryDetailTarget: { ...glossaryTarget, aliases: [] },
    });
    render(<MemoryGlossaryDetailPage />);
    expect(screen.getByText("-")).toBeInTheDocument();
  });
});
