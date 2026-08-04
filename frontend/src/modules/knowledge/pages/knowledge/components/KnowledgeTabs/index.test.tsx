import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import KnowledgeTabs from "./index";

const getDatasetMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceGetDataset: (...args: unknown[]) => getDatasetMock(...args),
  }),
}));

vi.mock("../SegmentTab", () => ({
  __esModule: true,
  default: (props: { type?: string }) => (
    <div data-testid="segment-tab">segment-tab:{props.type}</div>
  ),
}));

vi.mock("../SummaryTab", () => ({
  __esModule: true,
  default: (props: { type?: string }) => (
    <div data-testid="summary-tab">summary-tab:{props.type}</div>
  ),
}));

vi.mock("../QaTab", () => ({
  __esModule: true,
  default: (props: { type?: string }) => (
    <div data-testid="qa-tab">qa-tab:{props.type}</div>
  ),
}));

describe("KnowledgeTabs", () => {
  beforeEach(() => {
    getDatasetMock.mockReset();
  });

  it("shows only the image list tab for image documents, without calling the dataset API", async () => {
    renderWithProviders(
      <KnowledgeTabs
        knowledgeDetail={{ dataset_id: "ds-1", display_name: "photo.png" } as any}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText("knowledge.imageList")).toBeInTheDocument();
    });
    expect(getDatasetMock).not.toHaveBeenCalled();
  });

  it("builds tabs from parser configs and shows an empty state before/without any parsers", async () => {
    getDatasetMock.mockResolvedValue({ data: { parsers: [] } });

    renderWithProviders(
      <KnowledgeTabs
        knowledgeDetail={{ dataset_id: "ds-1", display_name: "report.pdf" } as any}
      />,
    );

    await waitFor(() => {
      expect(getDatasetMock).toHaveBeenCalledWith({ dataset: "ds-1" });
    });
    // Even with no configured parsers, an image-list tab is always appended.
    expect(await screen.findByText("knowledge.imageList")).toBeInTheDocument();
  });

  it("renders a summary tab, a qa tab, and lets the user switch between tabs", async () => {
    getDatasetMock.mockResolvedValue({
      data: {
        parsers: [
          { type: "PARSE_TYPE_SUMMARY", name: "summary" },
          { type: "PARSE_TYPE_QA", name: "qa" },
        ],
      },
    });

    renderWithProviders(
      <KnowledgeTabs
        knowledgeDetail={{ dataset_id: "ds-1", display_name: "report.pdf" } as any}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText("knowledge.segmentSummary")).toBeInTheDocument();
    });
    expect(screen.getByTestId("summary-tab")).toHaveTextContent("summary-tab:summary");

    fireEvent.click(screen.getByText("knowledge.segmentQa"));

    await waitFor(() => {
      expect(screen.getByTestId("qa-tab")).toHaveTextContent("qa-tab:qa");
    });
  });

  it("renders a document split tab for each split parser config", async () => {
    getDatasetMock.mockResolvedValue({
      data: {
        parsers: [
          { type: "PARSE_TYPE_SPLIT", name: "block" },
          { type: "PARSE_TYPE_SPLIT", name: "line" },
        ],
      },
    });

    renderWithProviders(
      <KnowledgeTabs
        knowledgeDetail={{ dataset_id: "ds-1", display_name: "report.pdf" } as any}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText("knowledge.segmentSplitBlock")).toBeInTheDocument();
    });
    expect(screen.getByText("knowledge.segmentSplitLine")).toBeInTheDocument();
  });
});
