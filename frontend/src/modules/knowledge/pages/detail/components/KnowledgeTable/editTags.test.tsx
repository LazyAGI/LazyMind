import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import EditTags from "./editTags";

const allDocumentTagsMock = vi.fn();
const updateDocumentMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceAllDocumentTags: (...args: unknown[]) =>
      allDocumentTagsMock(...args),
    documentServiceUpdateDocument: (...args: unknown[]) =>
      updateDocumentMock(...args),
  }),
}));

describe("EditTags", () => {
  const record = {
    document_id: "doc-1",
    display_name: "report.pdf",
    tags: ["existing"],
  };

  beforeEach(() => {
    allDocumentTagsMock.mockReset().mockResolvedValue({ data: { tags: ["existing", "other"] } });
    updateDocumentMock.mockReset().mockResolvedValue({});
  });

  it("does not render the modal content when closed", () => {
    renderWithProviders(
      <EditTags
        open={false}
        record={record as any}
        datasetId="ds-1"
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    expect(screen.queryByText("common.edit")).not.toBeInTheDocument();
  });

  it("loads tag options and pre-fills the record's existing tags when open", async () => {
    renderWithProviders(
      <EditTags
        open
        record={record as any}
        datasetId="ds-1"
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(allDocumentTagsMock).toHaveBeenCalled();
    });
    expect(screen.getByText("common.edit")).toBeInTheDocument();
    expect(screen.getByText("existing")).toBeInTheDocument();
  });

  it("submits the updated tags and calls onSuccess/onCancel", async () => {
    const onSuccess = vi.fn();
    const onCancel = vi.fn();
    renderWithProviders(
      <EditTags
        open
        record={record as any}
        datasetId="ds-1"
        onCancel={onCancel}
        onSuccess={onSuccess}
      />,
    );

    await waitFor(() => expect(allDocumentTagsMock).toHaveBeenCalled());

    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => {
      expect(updateDocumentMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        document: "doc-1",
        doc: {
          display_name: "report.pdf",
          tags: ["existing"],
        },
      });
    });
    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalled();
      expect(onCancel).toHaveBeenCalled();
    });
  });

  it("calls onCancel when the cancel button is clicked", async () => {
    const onCancel = vi.fn();
    renderWithProviders(
      <EditTags
        open
        record={record as any}
        datasetId="ds-1"
        onCancel={onCancel}
        onSuccess={vi.fn()}
      />,
    );

    await waitFor(() => expect(allDocumentTagsMock).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalled();
  });
});
