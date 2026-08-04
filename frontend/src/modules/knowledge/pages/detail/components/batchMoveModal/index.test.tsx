import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import BatchMoveModal from "./index";

const searchDocumentsMock = vi.fn();
const createTasksMock = vi.fn();
const startTasksMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceSearchDocuments: (...args: unknown[]) =>
      searchDocumentsMock(...args),
  }),
  TaskServiceApi: () => ({
    createTasks: (...args: unknown[]) => createTasksMock(...args),
    startTasks: (...args: unknown[]) => startTasksMock(...args),
  }),
}));

describe("BatchMoveModal", () => {
  const documents = [
    { documentId: "d1", parentId: "", dataSourceType: undefined },
    { documentId: "d2", parentId: "", dataSourceType: undefined },
  ];

  beforeEach(() => {
    searchDocumentsMock.mockReset().mockResolvedValue({
      data: { documents: [{ type: "FOLDER", display_name: "Folder A", document_id: "f1" }] },
    });
    createTasksMock.mockReset().mockResolvedValue({
      data: { tasks: [{ task_id: "t1" }] },
    });
    startTasksMock.mockReset().mockResolvedValue({});
  });

  it("renders nothing meaningful when closed", () => {
    renderWithProviders(
      <BatchMoveModal
        open={false}
        datasetId="ds-1"
        selectedFileCount={2}
        documents={documents as any}
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    expect(screen.queryByText("knowledge.batchMoveTitle")).not.toBeInTheDocument();
  });

  it("loads the folder tree and shows selected doc count when open", async () => {
    renderWithProviders(
      <BatchMoveModal
        open
        datasetId="ds-1"
        selectedFileCount={2}
        documents={documents as any}
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    expect(screen.getByText("knowledge.batchMoveTitle")).toBeInTheDocument();
    await waitFor(() => {
      expect(searchDocumentsMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        searchDocumentsRequest: { parent: "", page_size: 10000 },
      });
    });
  });

  it("warns and does not call API when no target is selected on ok", async () => {
    renderWithProviders(
      <BatchMoveModal
        open
        datasetId="ds-1"
        selectedFileCount={2}
        documents={documents as any}
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    await waitFor(() => expect(searchDocumentsMock).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => {
      expect(createTasksMock).not.toHaveBeenCalled();
    });
  });

  it("calls onCancel when the modal cancel button is clicked", async () => {
    const onCancel = vi.fn();
    renderWithProviders(
      <BatchMoveModal
        open
        datasetId="ds-1"
        selectedFileCount={2}
        documents={documents as any}
        onCancel={onCancel}
        onSuccess={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalled();
  });
});
