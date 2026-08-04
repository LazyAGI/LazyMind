import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import KnowledgePage from "./index";

const listDatasetsMock = vi.fn();
const allDatasetTagsMock = vi.fn();
const searchAllDocumentsMock = vi.fn();
const deleteDatasetMock = vi.fn();
const axiosGetMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: (...args: unknown[]) =>
      listDatasetsMock(...args),
    datasetServiceAllDatasetTags: (...args: unknown[]) =>
      allDatasetTagsMock(...args),
    datasetServiceDeleteDataset: (...args: unknown[]) =>
      deleteDatasetMock(...args),
    datasetServiceUpdateDataset: vi.fn().mockResolvedValue({}),
    datasetServiceCreateDataset: vi.fn().mockResolvedValue({}),
  }),
  DocumentServiceApi: () => ({
    documentServiceSearchAllDocuments: (...args: unknown[]) =>
      searchAllDocumentsMock(...args),
  }),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: (...args: unknown[]) => axiosGetMock(...args) },
  BASE_URL: "",
  localizeErrorCode: (code?: string) => code || "",
}));

vi.mock("@/hooks/useModelFeatures", () => ({
  MODEL_FEATURES_CHANGED_EVENT: "test:model-features-changed",
  fetchModelFeatures: vi.fn().mockResolvedValue({ image_embed_enabled: true }),
  isImageEmbedRequired: () => false,
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceScanApi: {
    listSources: vi.fn().mockResolvedValue({ data: { items: [], total: 0 } }),
    getSource: vi.fn(),
    getSourceSummary: vi.fn(),
  },
}));

vi.mock("@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation", () => ({
  useSyncKnowledgeBaseCreation: () => ({
    openEditWizard: vi.fn(),
  }),
}));

vi.mock("@/modules/knowledge/components/SyncKnowledgeBaseCreationFlow", () => ({
  __esModule: true,
  default: () => <div data-testid="sync-flow-stub" />,
  useSyncKnowledgeBaseCreation: () => ({
    openEditWizard: vi.fn(),
  }),
}));

vi.mock("@/modules/knowledge/components/CreateKnowledgeBaseModal", () => ({
  __esModule: true,
  default: () => <div data-testid="create-kb-modal-stub" />,
}));

vi.mock("@/modules/knowledge/components/UpdateModal", () => ({
  __esModule: true,
  default: () => <div data-testid="update-modal-stub" />,
}));

vi.mock("@/modules/knowledge/pages/detail/components/KnowledgeTable/editTags", () => ({
  __esModule: true,
  default: () => <div data-testid="edit-tags-stub" />,
}));

// The `@/components/ui` barrel re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). Only ListPageTable is
// needed here, so stub the barrel with a real antd Table.
vi.mock("@/components/ui", async () => {
  const { Table } = await import("antd");
  return { ListPageTable: Table };
});

describe("KnowledgePage (pages/list)", () => {
  beforeEach(() => {
    listDatasetsMock.mockReset().mockResolvedValue({
      data: {
        datasets: [
          { dataset_id: "ds-1", display_name: "My KB", tags: [], acl: [] },
        ],
        total_size: 1,
      },
    });
    allDatasetTagsMock.mockReset().mockResolvedValue({ data: { tags: [] } });
    searchAllDocumentsMock.mockReset().mockResolvedValue({
      data: { documents: [], total_size: 0 },
    });
    deleteDatasetMock.mockReset().mockResolvedValue({});
    axiosGetMock.mockReset().mockResolvedValue({ data: { ready: true } });
  });

  it("loads and renders the knowledge base list on mount", async () => {
    renderWithProviders(<KnowledgePage />);

    await waitFor(() => {
      expect(listDatasetsMock).toHaveBeenCalled();
    });
    expect(await screen.findByText("My KB")).toBeInTheDocument();
  });

  it("shows an embedding-not-ready banner when the embed model is not ready", async () => {
    axiosGetMock.mockResolvedValue({ data: { ready: false } });

    renderWithProviders(<KnowledgePage />);

    expect(
      await screen.findByText("knowledge.embeddingNotReadyBanner"),
    ).toBeInTheDocument();
  });

  it("switches to the document list view when the type selector changes", async () => {
    renderWithProviders(<KnowledgePage />);

    await waitFor(() => expect(listDatasetsMock).toHaveBeenCalled());

    const typeSelect = screen.getAllByRole("combobox")[0];
    fireEvent.mouseDown(typeSelect);

    await waitFor(() => {
      expect(screen.getByText("knowledge.knowledge")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("knowledge.knowledge"));

    await waitFor(() => {
      expect(searchAllDocumentsMock).toHaveBeenCalled();
    });
  });
});
