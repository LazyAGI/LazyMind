import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import DatasetExpandedRowEditor from "./DatasetExpandedRowEditor";
import type { DatasetItem } from "../shared";

describe("DatasetExpandedRowEditor", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("prefills the form from the given item", () => {
    const item: DatasetItem = {
      id: "i1",
      question: "What is RAG?",
      ground_truth: "Retrieval-augmented generation",
      question_type: "事实问答",
    } as DatasetItem;

    renderWithProviders(
      <DatasetExpandedRowEditor item={item} onSave={vi.fn()} onCancel={vi.fn()} />,
    );

    expect(screen.getByDisplayValue("What is RAG?")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Retrieval-augmented generation")).toBeInTheDocument();
  });

  it("calls onDirtyChange(false) on mount and onDirtyChange(true) after an edit", () => {
    const onDirtyChange = vi.fn();
    renderWithProviders(
      <DatasetExpandedRowEditor onSave={vi.fn()} onCancel={vi.fn()} onDirtyChange={onDirtyChange} />,
    );
    expect(onDirtyChange).toHaveBeenCalledWith(false);

    fireEvent.change(screen.getByPlaceholderText("datasetManagement.detail.placeholders.question"), {
      target: { value: "New question" },
    });
    expect(onDirtyChange).toHaveBeenCalledWith(true);
  });

  it("submits the validated form values on save", async () => {
    const onSave = vi.fn();
    renderWithProviders(<DatasetExpandedRowEditor onSave={onSave} onCancel={vi.fn()} isNew />);

    fireEvent.change(screen.getByPlaceholderText("datasetManagement.detail.placeholders.question"), {
      target: { value: "New question" },
    });
    fireEvent.change(screen.getByPlaceholderText("datasetManagement.detail.placeholders.groundTruth"), {
      target: { value: "New answer" },
    });

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "事实问答" } });

    fireEvent.click(screen.getByText("common.save"));

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({ question: "New question", ground_truth: "New answer" }),
      ),
    );
  });

  it("shows a validation error and does not save when the question is blank", async () => {
    const onSave = vi.fn();
    renderWithProviders(<DatasetExpandedRowEditor onSave={onSave} onCancel={vi.fn()} isNew />);

    fireEvent.click(screen.getByText("common.save"));

    await waitFor(() =>
      expect(
        screen.getByText("datasetManagement.validation.questionRequired"),
      ).toBeInTheDocument(),
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it("calls onCancel when the cancel button is clicked", () => {
    const onCancel = vi.fn();
    renderWithProviders(<DatasetExpandedRowEditor onSave={vi.fn()} onCancel={onCancel} />);
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
