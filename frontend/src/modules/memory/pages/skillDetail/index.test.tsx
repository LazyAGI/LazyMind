import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import MemorySkillDetailPage from "./index";
import { useMemoryManagementOutletContext } from "../../context";
import { getSkillAssetDetail, patchSkillAsset } from "../../skillApi";
import type { StructuredAsset } from "../../shared";

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

vi.mock("../../skillApi", async () => {
  const actual = await vi.importActual<typeof import("../../skillApi")>(
    "../../skillApi",
  );
  return {
    ...actual,
    getSkillAssetDetail: vi.fn(),
    patchSkillAsset: vi.fn(),
  };
});

vi.mock("../../components/skillPackage/SkillPackageEditor", () => ({
  default: ({ skillId }: { skillId: string }) => (
    <div data-testid="skill-package-editor">{skillId}</div>
  ),
}));

vi.mock("../../components/ResourceVersionDrawer", () => ({
  default: () => null,
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

const mockContext = useMemoryManagementOutletContext as unknown as ReturnType<
  typeof vi.fn
>;

function renderPage(itemId = "skill-1") {
  return render(
    <MemoryRouter initialEntries={[`/memory/skills/${itemId}`]}>
      <Routes>
        <Route path="/memory/skills/:itemId" element={<MemorySkillDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("MemorySkillDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContext.mockReturnValue({
      t,
      skillAssets: [makeAsset()],
      skillsInitialized: true,
      navigateToMemoryList: vi.fn(),
      refreshSkillAssets: vi.fn().mockResolvedValue(undefined),
    });
    (getSkillAssetDetail as any).mockResolvedValue(makeAsset());
  });

  it("renders the skill package editor for the resolved skill", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("skill-package-editor")).toHaveTextContent("skill-1");
    });
    expect(screen.getByText("Alpha Skill")).toBeInTheDocument();
  });

  it("shows an empty state when the skill cannot be found", async () => {
    mockContext.mockReturnValue({
      t,
      skillAssets: [],
      skillsInitialized: true,
      navigateToMemoryList: vi.fn(),
      refreshSkillAssets: vi.fn().mockResolvedValue(undefined),
    });
    (getSkillAssetDetail as any).mockResolvedValue(null);
    renderPage("missing-id");
    await waitFor(() => {
      expect(screen.getByText("admin.memoryDiffTargetMissing")).toBeInTheDocument();
    });
  });

  it("enters title edit mode and saves via patchSkillAsset", async () => {
    (patchSkillAsset as any).mockResolvedValue(undefined);
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Alpha Skill")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("Alpha Skill"));
    const input = document.querySelector("input") as HTMLInputElement;
    expect(input).not.toBeNull();
    fireEvent.change(input, { target: { value: "Updated Skill" } });
    fireEvent.click(screen.getByText("common.save"));
    await waitFor(() => {
      expect(patchSkillAsset).toHaveBeenCalledWith(
        "skill-1",
        expect.objectContaining({}),
      );
    });
  });

  it("shows a loading state while the skill list is not initialized", () => {
    mockContext.mockReturnValue({
      t,
      skillAssets: [],
      skillsInitialized: false,
      navigateToMemoryList: vi.fn(),
      refreshSkillAssets: vi.fn().mockResolvedValue(undefined),
    });
    renderPage();
    expect(screen.getByText("admin.memorySkillDetailTitle")).toBeInTheDocument();
  });
});
