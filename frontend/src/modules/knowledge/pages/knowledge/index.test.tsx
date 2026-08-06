import { describe, it, expect, vi, beforeEach } from "vitest";
import { waitFor, screen, renderWithProviders } from "@/test/testUtils";
import Detail from "./index";

const getDocumentMock = vi.fn();
const getDatasetMock = vi.fn();
const getSegmentMock = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return {
    ...actual,
    useParams: () => ({ knowledgeBaseId: "ds-1", knowledgeId: "doc-1" }),
    useSearchParams: () => [new URLSearchParams()],
    useNavigate: () => vi.fn(),
  };
});

vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceGetDocument: (...args: unknown[]) =>
      getDocumentMock(...args),
  }),
  SegmentServiceApi: () => ({
    segmentServiceGetSegment: (...args: unknown[]) => getSegmentMock(...args),
  }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceGetDataset: (...args: unknown[]) => getDatasetMock(...args),
  }),
  normalizeProxyableUrl: (url: string) => url,
}));

// This barrel re-exports RenderPdf, which pulls in pdfjs-dist (crashes in
// jsdom: no DOMMatrix). Stub with a minimal DetailPageHeader implementation.
vi.mock("@/components/ui", () => ({
  DetailPageHeader: (props: {
    title?: React.ReactNode;
    breadcrumbs?: Array<{ title?: React.ReactNode }>;
  }) => (
    <div data-testid="detail-page-header">
      <div data-testid="page-title">{props.title}</div>
    </div>
  ),
}));

vi.mock("@/modules/knowledge/components/FileViewer", () => ({
  __esModule: true,
  default: () => <div data-testid="file-viewer-stub" />,
}));

vi.mock("./components/KnowledgeTabs", () => ({
  __esModule: true,
  default: (props: { knowledgeDetail: { display_name?: string } }) => (
    <div data-testid="knowledge-tabs-stub">
      {props.knowledgeDetail.display_name}
    </div>
  ),
}));

describe("Detail (pages/knowledge)", () => {
  beforeEach(() => {
    getDocumentMock.mockReset().mockResolvedValue({
      data: {
        dataset_id: "ds-1",
        document_id: "doc-1",
        display_name: "report.pdf",
        file_url: "/files/report.pdf",
        tags: ["a"],
      },
    });
    getDatasetMock.mockReset().mockResolvedValue({
      data: { dataset_id: "ds-1", display_name: "My KB", acl: [] },
    });
    getSegmentMock.mockReset().mockResolvedValue({ data: {} });
  });

  it("fetches document and dataset details on mount and renders the file viewer and tabs", async () => {
    renderWithProviders(<Detail />);

    await waitFor(() => {
      expect(getDocumentMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        document: "doc-1",
      });
      expect(getDatasetMock).toHaveBeenCalledWith({ dataset: "ds-1" });
    });

    expect(await screen.findByTestId("file-viewer-stub")).toBeInTheDocument();
    expect(await screen.findByTestId("knowledge-tabs-stub")).toHaveTextContent(
      "report.pdf",
    );
    expect(screen.getByTestId("page-title")).toHaveTextContent("report.pdf");
  });

  it("does not render KnowledgeTabs until the document detail has loaded", () => {
    getDocumentMock.mockReturnValue(new Promise(() => {}));

    renderWithProviders(<Detail />);

    expect(screen.queryByTestId("knowledge-tabs-stub")).not.toBeInTheDocument();
  });
});
