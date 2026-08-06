import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import DatasetImportModal from "./DatasetImportModal";

vi.mock("./DatasetTemplateDownload", () => ({
  default: () => <div data-testid="template-download" />,
}));

function jsonFile(content: unknown, name = "dataset.json") {
  const file = new File([JSON.stringify(content)], name, { type: "application/json" });
  return file;
}

describe("DatasetImportModal", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not render its content when closed", () => {
    renderWithProviders(
      <DatasetImportModal open={false} onCancel={vi.fn()} onImported={vi.fn()} />,
    );
    expect(screen.queryByText("datasetManagement.import.title")).not.toBeInTheDocument();
  });

  it("renders the select-file step with the upload dragger when open", () => {
    renderWithProviders(<DatasetImportModal open onCancel={vi.fn()} onImported={vi.fn()} />);
    expect(screen.getByText("datasetManagement.import.title")).toBeInTheDocument();
    expect(screen.getByText("datasetManagement.import.uploadText")).toBeInTheDocument();
    expect(screen.getByTestId("template-download")).toBeInTheDocument();
  });

  it("rejects an unsupported file type before it can be added", async () => {
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithProviders(<DatasetImportModal open onCancel={vi.fn()} onImported={vi.fn()} />);

    const input = document.querySelector("input[type='file']") as HTMLInputElement;
    const badFile = new File(["data"], "notes.numbers", { type: "application/octet-stream" });
    fireEvent.change(input, { target: { files: [badFile] } });

    await waitFor(() =>
      expect(errorSpy).toHaveBeenCalledWith("datasetManagement.import.numbersUnsupported"),
    );
  });

  it("parses a valid JSON file and advances to the preview step", async () => {
    renderWithProviders(<DatasetImportModal open onCancel={vi.fn()} onImported={vi.fn()} />);

    const input = document.querySelector("input[type='file']") as HTMLInputElement;
    fireEvent.change(input, {
      target: {
        files: [jsonFile([{ question: "What is RAG?", question_type: "事实问答", ground_truth: "Retrieval-augmented generation" }])],
      },
    });

    fireEvent.click(screen.getByText("datasetManagement.import.next"));

    await waitFor(() =>
      expect(screen.getByText("datasetManagement.import.confirmImport")).toBeInTheDocument(),
    );
    expect(screen.getByText("What is RAG?")).toBeInTheDocument();
  });

  it("shows an error and stays on the select-file step when parsing an empty JSON array", async () => {
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithProviders(<DatasetImportModal open onCancel={vi.fn()} onImported={vi.fn()} />);

    const input = document.querySelector("input[type='file']") as HTMLInputElement;
    fireEvent.change(input, { target: { files: [jsonFile([])] } });

    fireEvent.click(screen.getByText("datasetManagement.import.next"));

    await waitFor(() =>
      expect(errorSpy).toHaveBeenCalledWith("datasetManagement.import.noRows"),
    );
    expect(screen.getByText("datasetManagement.import.uploadText")).toBeInTheDocument();
  });

  it("confirms the import with only valid rows and calls onImported", async () => {
    const onImported = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(<DatasetImportModal open onCancel={vi.fn()} onImported={onImported} />);

    const input = document.querySelector("input[type='file']") as HTMLInputElement;
    fireEvent.change(input, {
      target: {
        files: [jsonFile([{ question: "What is RAG?", question_type: "事实问答", ground_truth: "Retrieval-augmented generation" }])],
      },
    });
    fireEvent.click(screen.getByText("datasetManagement.import.next"));
    await screen.findByText("datasetManagement.import.confirmImport");

    fireEvent.click(screen.getByText("datasetManagement.import.confirmImport"));

    await waitFor(() =>
      expect(onImported).toHaveBeenCalledWith(
        [expect.objectContaining({ question: "What is RAG?" })],
        expect.objectContaining({ successCount: 1, failedCount: 0 }),
        expect.any(File),
      ),
    );
    await waitFor(() =>
      expect(screen.getByText("datasetManagement.import.resultTitle")).toBeInTheDocument(),
    );
  });
});
