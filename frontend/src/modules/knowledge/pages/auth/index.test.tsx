import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import Authorize from "./index";

const getDatasetMock = vi.fn();
const navigateMock = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return { ...actual, useNavigate: () => navigateMock };
});

vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceGetDataset: (...args: unknown[]) => getDatasetMock(...args),
  }),
}));

vi.mock("./components/MemberList", () => ({
  default: (props: { memberType: number }) => (
    <div data-testid={`member-list-${props.memberType}`} />
  ),
}));

// `@/components/ui`'s barrel file re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). This page only needs
// DetailPageHeader, so stub the barrel with a minimal implementation that
// preserves the title text and back-button click behavior used in tests.
vi.mock("@/components/ui", () => ({
  DetailPageHeader: (props: { title?: ReactNode; onBack?: () => void }) => (
    <div>
      <span>{props.title}</span>
      <button aria-label="back" onClick={() => props.onBack?.()}>
        back
      </button>
    </div>
  ),
}));

function renderAuthorize(initialPath = "/lib/knowledge/1/authorize") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/lib/knowledge/:id/authorize" element={<Authorize />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("Authorize page", () => {
  beforeEach(() => {
    getDatasetMock.mockReset().mockResolvedValue({
      data: { dataset_id: "1", display_name: "My KB" },
    });
    navigateMock.mockReset();
  });

  it("fetches the dataset detail and renders the title with its name", async () => {
    renderAuthorize();

    await waitFor(() => {
      expect(getDatasetMock).toHaveBeenCalledWith({ dataset: "1" });
    });
    await waitFor(() => {
      expect(screen.getByText("knowledge.authorizeTitle")).toBeInTheDocument();
    });
  });

  it("renders the user member list tab by default", async () => {
    renderAuthorize();

    await waitFor(() => {
      expect(screen.getByTestId("member-list-1")).toBeInTheDocument();
    });
  });

  it("switches to the group tab when clicked", async () => {
    renderAuthorize();

    await waitFor(() => {
      expect(screen.getByTestId("member-list-1")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("knowledge.groups"));

    await waitFor(() => {
      expect(screen.getByTestId("member-list-2")).toBeInTheDocument();
    });
  });

  it("navigates back when the header back button is clicked", async () => {
    renderAuthorize();

    await waitFor(() => {
      expect(screen.getByTestId("member-list-1")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "back" }));

    expect(navigateMock).toHaveBeenCalledWith(-1);
  });
});
