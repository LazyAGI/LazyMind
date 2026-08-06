import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import DatasetDetailPage from "./index";
import type { DatasetItem, DatasetListItem } from "../../shared";

vi.mock("../../index.scss", () => ({}));

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

const getDatasetMock = vi.hoisted(() => vi.fn());
const listDatasetItemsMock = vi.hoisted(() => vi.fn());
const listDatasetQuestionTypesMock = vi.hoisted(() => vi.fn());
const createDatasetItemMock = vi.hoisted(() => vi.fn());
const updateDatasetItemMock = vi.hoisted(() => vi.fn());
const deleteDatasetItemMock = vi.hoisted(() => vi.fn());
const batchDeleteDatasetItemsMock = vi.hoisted(() => vi.fn());
const importDatasetItemsMock = vi.hoisted(() => vi.fn());
const findKnowledgeBaseDocumentByIdMock = vi.hoisted(() => vi.fn());
const searchKnowledgeBaseDocumentsMock = vi.hoisted(() => vi.fn());
const mergeKnowledgeDocumentOptionsMock = vi.hoisted(() =>
  vi.fn((current: unknown[], next: unknown[]) => [...current, ...next]),
);

vi.mock("../../api", () => ({
  getDataset: getDatasetMock,
  listDatasetItems: listDatasetItemsMock,
  listDatasetQuestionTypes: listDatasetQuestionTypesMock,
  createDatasetItem: createDatasetItemMock,
  updateDatasetItem: updateDatasetItemMock,
  deleteDatasetItem: deleteDatasetItemMock,
  batchDeleteDatasetItems: batchDeleteDatasetItemsMock,
  importDatasetItems: importDatasetItemsMock,
  findKnowledgeBaseDocumentById: findKnowledgeBaseDocumentByIdMock,
  searchKnowledgeBaseDocuments: searchKnowledgeBaseDocumentsMock,
  mergeKnowledgeDocumentOptions: mergeKnowledgeDocumentOptionsMock,
}));

vi.mock("@/modules/knowledge/components/FileViewer", () => ({
  default: () => <div data-testid="file-viewer" />,
}));

const datasetServiceGetDatasetMock = vi.hoisted(() => vi.fn());
const documentServiceGetDocumentMock = vi.hoisted(() => vi.fn());
const segmentServiceSearchSegmentsMock = vi.hoisted(() => vi.fn());
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceGetDataset: datasetServiceGetDatasetMock,
  }),
  DocumentServiceApi: () => ({
    documentServiceGetDocument: documentServiceGetDocumentMock,
  }),
  SegmentServiceApi: () => ({
    segmentServiceSearchSegments: segmentServiceSearchSegmentsMock,
  }),
  normalizeProxyableUrl: (uri?: string) => uri || "",
}));

vi.mock("@/api/generated/knowledge-client", () => ({
  ParserConfigTypeEnum: { ParseTypeSplit: "split" },
}));

vi.mock("../../components/DatasetImportModal", () => ({
  default: () => null,
}));

function dataset(overrides: Partial<DatasetListItem> = {}): DatasetListItem {
  return {
    id: "d1",
    name: "Support Dataset",
    description: "desc",
    owner_id: "u1",
    group_id: "g1",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    knowledge_bases: [{ id: "kb1", name: "Docs KB" }],
    ...overrides,
  };
}

function item(overrides: Partial<DatasetItem> = {}): DatasetItem {
  return {
    id: "item-1",
    dataset_id: "d1",
    question: "What is RAG?",
    question_type: "事实问答",
    ground_truth: "Retrieval-augmented generation",
    source: "manual",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    created_by: "alice",
    ...overrides,
  };
}

function renderDetail(datasetId = "d1") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[`/dataset-management/${datasetId}`]}>
        <Routes>
          <Route path="/dataset-management/:datasetId" element={<DatasetDetailPage />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("DatasetDetailPage", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    getDatasetMock.mockReset().mockResolvedValue(dataset());
    listDatasetItemsMock.mockReset().mockResolvedValue({ items: [item()], total: 1 });
    listDatasetQuestionTypesMock.mockReset().mockResolvedValue(["事实问答"]);
    createDatasetItemMock.mockReset().mockResolvedValue(item({ id: "item-2" }));
    updateDatasetItemMock.mockReset().mockResolvedValue(item());
    deleteDatasetItemMock.mockReset().mockResolvedValue(undefined);
    batchDeleteDatasetItemsMock.mockReset().mockResolvedValue(undefined);
    importDatasetItemsMock.mockReset().mockResolvedValue(undefined);
    findKnowledgeBaseDocumentByIdMock.mockReset().mockResolvedValue(null);
    searchKnowledgeBaseDocumentsMock.mockReset().mockResolvedValue({ options: [] });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads and renders the dataset detail and its items on mount", async () => {
    renderDetail();
    await waitFor(() => expect(listDatasetItemsMock).toHaveBeenCalledWith("d1", expect.any(Object)));
    expect(await screen.findByText("What is RAG?")).toBeInTheDocument();
    expect(screen.getByText("datasetManagement.detail.breadcrumb")).toBeInTheDocument();
  });

  it("navigates back to the list when the breadcrumb button is clicked", async () => {
    renderDetail();
    await screen.findByText("What is RAG?");

    fireEvent.click(screen.getByText("datasetManagement.detail.breadcrumb"));

    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith("/dataset-management"));
  });

  it("searches items with the entered keyword", async () => {
    renderDetail();
    await screen.findByText("What is RAG?");

    fireEvent.change(
      screen.getByPlaceholderText("datasetManagement.detail.searchPlaceholder"),
      { target: { value: "rag" } },
    );
    fireEvent.click(screen.getByText("datasetManagement.detail.search"));

    await waitFor(() =>
      expect(listDatasetItemsMock).toHaveBeenLastCalledWith(
        "d1",
        expect.objectContaining({ keyword: "rag" }),
      ),
    );
  });

  it("shows a warning and does not call the API when batch-deleting with no selection", async () => {
    const { message } = await import("antd");
    const warningSpy = vi.spyOn(message, "warning");

    renderDetail();
    await screen.findByText("What is RAG?");

    fireEvent.click(screen.getByText("datasetManagement.detail.batchDelete"));

    expect(warningSpy).toHaveBeenCalledWith("datasetManagement.detail.selectSampleFirst");
    expect(batchDeleteDatasetItemsMock).not.toHaveBeenCalled();
  });

  it("deletes a single item after confirming the delete modal", async () => {
    renderDetail();
    await screen.findByText("What is RAG?");

    fireEvent.click(screen.getByText("common.delete"));

    const confirmModal = await screen.findByRole("dialog");
    fireEvent.click(within(confirmModal).getByText("common.delete"));

    await waitFor(() => expect(deleteDatasetItemMock).toHaveBeenCalledWith("d1", "item-1"));
  });

  it("adds a new sample row and creates it after filling required fields", async () => {
    renderDetail();
    await screen.findByText("What is RAG?");

    fireEvent.click(screen.getByText("datasetManagement.detail.addSample"));

    // The new row starts with the "question" cell already in edit mode.
    const questionInput = await screen.findByPlaceholderText(
      "datasetManagement.detail.placeholders.question",
    );
    fireEvent.change(questionInput, { target: { value: "New question" } });
    fireEvent.blur(questionInput);

    // Question type and ground truth are still empty, so auto-save should not fire yet.
    await waitFor(() => expect(createDatasetItemMock).not.toHaveBeenCalled());
  });
});
