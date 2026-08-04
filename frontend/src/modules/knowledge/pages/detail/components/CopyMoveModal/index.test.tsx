import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import CopyMoveModal from "./index";

const searchDocumentsMock = vi.fn();
const createTasksMock = vi.fn();
const startTasksMock = vi.fn();

// `@/components/ui`'s barrel file re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). This component only needs
// CommonModal, so stub the barrel with a minimal implementation.
vi.mock("@/components/ui", () => ({
  CommonModal: (props: {
    title?: ReactNode;
    contentText?: ReactNode;
    successFn?: () => void;
    cancelFn?: () => void;
  }) => (
    <div>
      <div>{props.title}</div>
      <div>{props.contentText}</div>
      <button onClick={props.cancelFn}>common.cancel</button>
      <button onClick={props.successFn}>common.confirm</button>
    </div>
  ),
}));

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

describe("CopyMoveModal", () => {
  const currentData = {
    dataset_id: "ds-1",
    document_id: "doc-1",
    display_name: "report.pdf",
    data_source_type: "DATA_SOURCE_TYPE_UNSPECIFIED",
  };

  beforeEach(() => {
    searchDocumentsMock.mockReset().mockResolvedValue({
      data: { documents: [{ type: "FOLDER", display_name: "Folder A", document_id: "f1" }] },
    });
    createTasksMock.mockReset().mockResolvedValue({
      data: { tasks: [{ task_id: "t1" }] },
    });
    startTasksMock.mockReset().mockResolvedValue({});
  });

  it("renders the move title and loads the root folder tree", async () => {
    renderWithProviders(
      <CopyMoveModal
        cancelFn={vi.fn()}
        currentData={currentData as any}
        action="move"
      />,
    );

    expect(screen.getByText("knowledge.moveTo")).toBeInTheDocument();
    await waitFor(() => {
      expect(searchDocumentsMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        searchDocumentsRequest: { parent: "", page_size: 10000 },
      });
    });
  });

  it("renders the copy title when action is copy", () => {
    renderWithProviders(
      <CopyMoveModal
        cancelFn={vi.fn()}
        currentData={currentData as any}
        action="copy"
      />,
    );

    expect(screen.getByText("knowledge.copyTo")).toBeInTheDocument();
  });

  it("calls cancelFn when the cancel button is clicked", () => {
    const cancelFn = vi.fn();
    renderWithProviders(
      <CopyMoveModal cancelFn={cancelFn} currentData={currentData as any} action="move" />,
    );

    fireEvent.click(screen.getByText("common.cancel"));
    expect(cancelFn).toHaveBeenCalled();
  });

  it("creates and starts a move task, then calls onSuccess and cancelFn", async () => {
    const cancelFn = vi.fn();
    const onSuccess = vi.fn();
    renderWithProviders(
      <CopyMoveModal
        cancelFn={cancelFn}
        currentData={currentData as any}
        action="move"
        onSuccess={onSuccess}
      />,
    );

    fireEvent.click(screen.getByText("common.confirm"));

    await waitFor(() => {
      expect(createTasksMock).toHaveBeenCalled();
    });
    const createArgs = createTasksMock.mock.calls[0];
    expect(createArgs[0]).toBe("ds-1");
    expect(createArgs[1].items[0].task.task_type).toBe("TASK_TYPE_MOVE");

    await waitFor(() => {
      expect(startTasksMock).toHaveBeenCalledWith("ds-1", { task_ids: ["t1"] });
    });
    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalled();
      expect(cancelFn).toHaveBeenCalled();
    });
  });
});
