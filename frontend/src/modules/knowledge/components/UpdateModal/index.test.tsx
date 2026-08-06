import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import UpdateAppModel, { UpdateImperativeProps } from "./index";

const datasetServiceListAlgos = vi.fn();
const datasetServiceAllDatasetTags = vi.fn();
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListAlgos,
    datasetServiceAllDatasetTags,
  }),
}));

describe("UpdateAppModel", () => {
  beforeEach(() => {
    datasetServiceListAlgos.mockResolvedValue({
      data: { algos: [{ algo_id: "algo-1", display_name: "Algo One" }] },
    });
    datasetServiceAllDatasetTags.mockResolvedValue({ data: { tags: ["tag-a"] } });
  });

  it("is hidden until onOpen is invoked", () => {
    renderWithProviders(<UpdateAppModel onUpdate={vi.fn()} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows the create title when opened without existing data", async () => {
    const ref = createRef<UpdateImperativeProps>();
    renderWithProviders(<UpdateAppModel ref={ref} onUpdate={vi.fn()} />);

    ref.current?.onOpen();

    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });
  });

  it("shows the edit title and pre-fills fields when opened with existing dataset data", async () => {
    const ref = createRef<UpdateImperativeProps>();
    renderWithProviders(<UpdateAppModel ref={ref} onUpdate={vi.fn()} />);

    ref.current?.onOpen({
      dataset_id: "ds-1",
      display_name: "My-KB",
      desc: "description",
      algo: { algo_id: "algo-1" },
    } as any);

    await waitFor(() => {
      expect(screen.getByText("knowledge.editKnowledgeBase")).toBeInTheDocument();
    });
    expect(screen.getByDisplayValue("My-KB")).toBeInTheDocument();
  });

  it("auto-selects the sole algorithm and hides the algorithm selector when only one exists", async () => {
    const ref = createRef<UpdateImperativeProps>();
    renderWithProviders(<UpdateAppModel ref={ref} onUpdate={vi.fn()} />);

    ref.current?.onOpen();

    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });
    expect(screen.queryByText("knowledge.parseAlgorithm")).not.toBeInTheDocument();
  });

  it("shows the algorithm selector when multiple algorithms are available", async () => {
    datasetServiceListAlgos.mockResolvedValue({
      data: {
        algos: [
          { algo_id: "algo-1", display_name: "Algo One" },
          { algo_id: "algo-2", display_name: "Algo Two" },
        ],
      },
    });
    const ref = createRef<UpdateImperativeProps>();
    renderWithProviders(<UpdateAppModel ref={ref} onUpdate={vi.fn()} />);

    ref.current?.onOpen();

    await waitFor(() => {
      expect(screen.getByText("knowledge.parseAlgorithm")).toBeInTheDocument();
    });
  });

  it("calls onUpdate with form values including the dataset_id when confirmed", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    const ref = createRef<UpdateImperativeProps>();
    renderWithProviders(<UpdateAppModel ref={ref} onUpdate={onUpdate} />);

    ref.current?.onOpen({
      dataset_id: "ds-1",
      display_name: "My-KB",
      tags: ["tag-a"],
      algo: { algo_id: "algo-1" },
    } as any);

    await waitFor(() => {
      expect(screen.getByDisplayValue("My-KB")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /ok|确定/i }));

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ dataset_id: "ds-1", display_name: "My-KB" }),
      );
    });
  });
});
