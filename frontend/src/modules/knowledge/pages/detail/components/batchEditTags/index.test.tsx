import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import BatchEditTags from "./index";

const allDocumentTagsMock = vi.fn();
const batchUpdateDocumentTagsMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceAllDocumentTags: (...args: unknown[]) =>
      allDocumentTagsMock(...args),
    documentServiceBatchUpdateDocumentTags: (...args: unknown[]) =>
      batchUpdateDocumentTagsMock(...args),
  }),
}));

describe("BatchEditTags", () => {
  beforeEach(() => {
    allDocumentTagsMock.mockReset().mockResolvedValue({ data: { tags: ["existing"] } });
    batchUpdateDocumentTagsMock.mockReset().mockResolvedValue({
      data: { affected_files: 3, truncated_docs: 0 },
    });
  });

  it("loads tag options when opened", async () => {
    renderWithProviders(
      <BatchEditTags
        open
        selectedFileCount={3}
        documentIds={["d1"]}
        folderIds={[]}
        datasetId="ds-1"
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(allDocumentTagsMock).toHaveBeenCalled();
    });
    expect(screen.getByText("knowledge.batchSetTags")).toBeInTheDocument();
  });

  it("warns when selectedFileCount is 0 on submit", async () => {
    renderWithProviders(
      <BatchEditTags
        open
        selectedFileCount={0}
        documentIds={[]}
        folderIds={[]}
        datasetId="ds-1"
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => {
      expect(batchUpdateDocumentTagsMock).not.toHaveBeenCalled();
    });
  });

  it("submits with append mode and the given document ids by default", async () => {
    const onSuccess = vi.fn();
    const onCancel = vi.fn();
    renderWithProviders(
      <BatchEditTags
        open
        selectedFileCount={3}
        documentIds={["d1", "d2"]}
        folderIds={[]}
        datasetId="ds-1"
        onCancel={onCancel}
        onSuccess={onSuccess}
      />,
    );

    await waitFor(() => expect(allDocumentTagsMock).toHaveBeenCalled());

    // Fill in the tags field via the antd tags select (type + Enter creates a tag).
    const combobox = screen.getByRole("combobox");
    fireEvent.change(combobox, { target: { value: "urgent" } });
    fireEvent.keyDown(combobox, { key: "Enter", code: "Enter", keyCode: 13, which: 13 });

    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => {
      expect(batchUpdateDocumentTagsMock).toHaveBeenCalled();
    });
    const request = batchUpdateDocumentTagsMock.mock.calls[0][0];
    expect(request.dataset).toBe("ds-1");
    expect(request.batchUpdateDocumentTagsRequest.mode).toBe("APPEND");
    expect(request.batchUpdateDocumentTagsRequest.document_ids).toEqual(["d1", "d2"]);

    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalled();
      expect(onCancel).toHaveBeenCalled();
    });
  });

  it("calls onCancel when the cancel button is clicked", () => {
    const onCancel = vi.fn();
    renderWithProviders(
      <BatchEditTags
        open
        selectedFileCount={1}
        documentIds={["d1"]}
        folderIds={[]}
        datasetId="ds-1"
        onCancel={onCancel}
        onSuccess={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalled();
  });
});
