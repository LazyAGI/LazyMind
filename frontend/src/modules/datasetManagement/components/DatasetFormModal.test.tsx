import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import DatasetFormModal from "./DatasetFormModal";
import type { DatasetListItem, KnowledgeBaseOption } from "../shared";

const knowledgeBases: KnowledgeBaseOption[] = [
  { id: "kb1", name: "Docs KB" },
  { id: "kb2", name: "Support KB" },
];

describe("DatasetFormModal", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the create title and an empty form when in create mode", () => {
    renderWithProviders(
      <DatasetFormModal
        open
        mode="create"
        knowledgeBases={knowledgeBases}
        onCancel={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );
    expect(screen.getByText("datasetManagement.form.createTitle")).toBeInTheDocument();
  });

  it("submits the entered name, description, and selected knowledge base", async () => {
    const onSubmit = vi.fn();
    renderWithProviders(
      <DatasetFormModal
        open
        mode="create"
        knowledgeBases={knowledgeBases}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />,
    );

    fireEvent.change(screen.getByLabelText("datasetManagement.fields.datasetName"), {
      target: { value: "My_Dataset" },
    });

    const kbSelect = screen.getByRole("combobox");
    fireEvent.mouseDown(kbSelect);
    fireEvent.click(await screen.findByTitle("Docs KB"));

    fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ name: "My_Dataset", knowledge_base_ids: ["kb1"] }),
      ),
    );
  });

  it("shows a validation error and does not submit when the name is blank", async () => {
    const onSubmit = vi.fn();
    renderWithProviders(
      <DatasetFormModal
        open
        mode="create"
        knowledgeBases={knowledgeBases}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() =>
      expect(
        screen.getByText("datasetManagement.form.validation.nameRequired"),
      ).toBeInTheDocument(),
    );
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("prefills fields from the dataset when editing and shows a warning for orphaned knowledge bases", () => {
    const dataset: DatasetListItem = {
      id: "d1",
      name: "Existing Dataset",
      description: "desc",
      knowledge_bases: [{ id: "kb-missing", name: "Deleted KB" }],
    } as DatasetListItem;

    renderWithProviders(
      <DatasetFormModal
        open
        mode="edit"
        dataset={dataset}
        knowledgeBases={knowledgeBases}
        onCancel={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );

    expect(screen.getByDisplayValue("Existing Dataset")).toBeInTheDocument();
    expect(
      screen.getByText("datasetManagement.form.kbDeletedWarning"),
    ).toBeInTheDocument();
  });

  it("calls onCancel when the cancel button is clicked", () => {
    const onCancel = vi.fn();
    renderWithProviders(
      <DatasetFormModal
        open
        mode="create"
        knowledgeBases={knowledgeBases}
        onCancel={onCancel}
        onSubmit={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
