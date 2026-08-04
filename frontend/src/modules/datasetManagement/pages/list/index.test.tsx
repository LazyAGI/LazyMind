import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor, within } from "@/test/testUtils";
import DatasetListPage from "./index";

vi.mock("../../index.scss", () => ({}));

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

const listDatasetsMock = vi.hoisted(() => vi.fn());
const listKnowledgeBasesMock = vi.hoisted(() => vi.fn());
const createDatasetMock = vi.hoisted(() => vi.fn());
const updateDatasetMock = vi.hoisted(() => vi.fn());
const deleteDatasetMock = vi.hoisted(() => vi.fn());
const getDatasetMock = vi.hoisted(() => vi.fn());

vi.mock("../../api", () => ({
  listDatasets: listDatasetsMock,
  listKnowledgeBases: listKnowledgeBasesMock,
  createDataset: createDatasetMock,
  updateDataset: updateDatasetMock,
  deleteDataset: deleteDatasetMock,
  getDataset: getDatasetMock,
}));

function datasetItem(overrides: Record<string, unknown> = {}) {
  return {
    id: "d1",
    name: "Support Dataset",
    description: "desc",
    knowledge_bases: [{ id: "kb1", name: "Docs KB" }],
    sample_count: 5,
    owner_name: "alice",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("DatasetListPage", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    listDatasetsMock.mockReset().mockResolvedValue([datasetItem()]);
    listKnowledgeBasesMock.mockReset().mockResolvedValue([{ id: "kb1", name: "Docs KB" }]);
    createDatasetMock.mockReset().mockResolvedValue(datasetItem({ id: "new-id" }));
    updateDatasetMock.mockReset().mockResolvedValue(datasetItem());
    deleteDatasetMock.mockReset().mockResolvedValue(undefined);
    getDatasetMock.mockReset().mockResolvedValue(datasetItem());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads and renders the dataset list on mount", async () => {
    renderWithProviders(<DatasetListPage />);
    await waitFor(() => expect(listDatasetsMock).toHaveBeenCalledWith(""));
    expect(await screen.findByText("Support Dataset")).toBeInTheDocument();
  });

  it("searches datasets by the entered keyword", async () => {
    renderWithProviders(<DatasetListPage />);
    await waitFor(() => expect(listDatasetsMock).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByPlaceholderText("datasetManagement.list.searchPlaceholder"), {
      target: { value: "support" },
    });
    fireEvent.click(screen.getByText("common.search"));

    await waitFor(() => expect(listDatasetsMock).toHaveBeenCalledWith("support"));
  });

  it("navigates to the dataset detail page when the name link is clicked", async () => {
    renderWithProviders(<DatasetListPage />);
    const nameLink = await screen.findByText("Support Dataset");
    fireEvent.click(nameLink);
    expect(navigateMock).toHaveBeenCalledWith("/dataset-management/d1");
  });

  it("creates a new dataset and navigates to its detail page", async () => {
    renderWithProviders(<DatasetListPage />);
    await screen.findByText("Support Dataset");

    fireEvent.click(screen.getByText("datasetManagement.list.createDataset"));
    fireEvent.change(screen.getByLabelText("datasetManagement.fields.datasetName"), {
      target: { value: "New_Dataset" },
    });

    const kbSelect = document.querySelector("#knowledge_base_ids") as HTMLElement;
    fireEvent.mouseDown(kbSelect);
    fireEvent.click(await screen.findByTitle("Docs KB"));

    fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() =>
      expect(createDatasetMock).toHaveBeenCalledWith(
        expect.objectContaining({ name: "New_Dataset" }),
      ),
    );
    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith("/dataset-management/new-id"));
  });

  it("deletes a dataset after confirmation and reloads the list", async () => {
    renderWithProviders(<DatasetListPage />);
    await screen.findByText("Support Dataset");

    fireEvent.click(screen.getByText("common.delete"));

    const confirmModal = await screen.findByRole("dialog");
    const modalOkButton = within(confirmModal).getByText("common.delete");
    fireEvent.click(modalOkButton);

    await waitFor(() => expect(deleteDatasetMock).toHaveBeenCalledWith("d1"));
    await waitFor(() => expect(listDatasetsMock).toHaveBeenCalledTimes(2));
  });
});
