import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import CreateKnowledgeBaseModal, {
  type CreateKnowledgeBaseModalRef,
} from "./index";

const datasetServiceListAlgos = vi.fn();
const datasetServiceAllDatasetTags = vi.fn();
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListAlgos,
    datasetServiceAllDatasetTags,
  }),
}));

vi.mock("@/modules/dataSource/components/management/DataSourceProviderPicker", () => ({
  default: (props: { vm: { handleCreateProviderSelect: (type: string) => void } }) => (
    <button
      type="button"
      data-testid="cloud-provider-picker"
      onClick={() => props.vm.handleCreateProviderSelect("feishu")}
    >
      pick-provider
    </button>
  ),
}));

vi.mock("@/modules/dataSource/index.scss", () => ({}));
vi.mock("./index.scss", () => ({}));

function makeSyncCreateVm(
  overrides: Partial<SyncKnowledgeBaseCreationVm> = {},
): SyncKnowledgeBaseCreationVm {
  return {
    wizardOpen: false,
    authSelectModalOpen: false,
    feishuSetupModalOpen: false,
    handleCreateProviderSelect: vi.fn(),
    ...overrides,
  } as SyncKnowledgeBaseCreationVm;
}

describe("CreateKnowledgeBaseModal", () => {
  beforeEach(() => {
    datasetServiceListAlgos.mockResolvedValue({
      data: { algos: [{ algo_id: "algo-1", display_name: "Algo One" }] },
    });
    datasetServiceAllDatasetTags.mockResolvedValue({ data: { tags: ["tag-a"] } });
  });

  it("is hidden until onOpen is invoked", () => {
    renderWithProviders(
      <CreateKnowledgeBaseModal onCreate={vi.fn()} syncCreateVm={makeSyncCreateVm()} />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens on the direct tab by default and loads tags/algorithms", async () => {
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={vi.fn()}
        syncCreateVm={makeSyncCreateVm()}
      />,
    );

    ref.current?.onOpen();

    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });
    expect(datasetServiceAllDatasetTags).toHaveBeenCalled();
    expect(datasetServiceListAlgos).toHaveBeenCalled();
    // Single algorithm result is auto-selected, so the selector is hidden.
    expect(screen.queryByText("knowledge.parseAlgorithm")).not.toBeInTheDocument();
  });

  it("opens directly on the cloud tab when requested and renders the provider picker", async () => {
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={vi.fn()}
        syncCreateVm={makeSyncCreateVm()}
      />,
    );

    ref.current?.onOpen("cloud");

    await waitFor(() => {
      expect(screen.getByTestId("cloud-provider-picker")).toBeInTheDocument();
    });
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
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={vi.fn()}
        syncCreateVm={makeSyncCreateVm()}
      />,
    );

    ref.current?.onOpen();

    await waitFor(() => {
      expect(screen.getByText("knowledge.parseAlgorithm")).toBeInTheDocument();
    });
  });

  it("submits form values via onCreate and closes the modal on success", async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={onCreate}
        syncCreateVm={makeSyncCreateVm()}
      />,
    );

    ref.current?.onOpen();
    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });

    fireEvent.change(
      screen.getByPlaceholderText("knowledge.knowledgeNameRule"),
      { target: { value: "My-KB" } },
    );

    // TagSelect (mode="tags" Select) requires typing then Enter to commit a tag.
    const tagInput = document.querySelector(
      ".knowledge-create-modal-tabs .ant-select-selection-search-input",
    ) as HTMLInputElement;
    fireEvent.change(tagInput, { target: { value: "new-tag" } });
    fireEvent.keyDown(tagInput, { key: "Enter", code: "Enter", keyCode: 13, which: 13 });

    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));

    await waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ display_name: "My-KB" }),
      );
    });
  });

  it("logs an error and keeps the modal open when onCreate rejects", async () => {
    const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const onCreate = vi.fn().mockRejectedValue(new Error("boom"));
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={onCreate}
        syncCreateVm={makeSyncCreateVm()}
      />,
    );

    ref.current?.onOpen();
    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });

    fireEvent.change(
      screen.getByPlaceholderText("knowledge.knowledgeNameRule"),
      { target: { value: "My-KB" } },
    );
    const tagInput = document.querySelector(
      ".knowledge-create-modal-tabs .ant-select-selection-search-input",
    ) as HTMLInputElement;
    fireEvent.change(tagInput, { target: { value: "new-tag" } });
    fireEvent.keyDown(tagInput, { key: "Enter", code: "Enter", keyCode: 13, which: 13 });

    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));

    await waitFor(() => {
      expect(onCreate).toHaveBeenCalled();
      expect(consoleErrorSpy).toHaveBeenCalled();
    });
    expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    consoleErrorSpy.mockRestore();
  });

  it("closes the modal when the cancel button is clicked", async () => {
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={vi.fn()}
        syncCreateVm={makeSyncCreateVm()}
      />,
    );

    ref.current?.onOpen();
    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));

    await waitFor(() => {
      document
        .querySelectorAll(".ant-fade-leave, .ant-zoom-leave")
        .forEach((node) => {
          fireEvent.animationEnd(node);
          fireEvent.transitionEnd(node);
        });
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("hides itself while a cloud sub-flow (wizard) is active", async () => {
    const ref = createRef<CreateKnowledgeBaseModalRef>();
    const syncCreateVm = makeSyncCreateVm();
    const { rerender } = renderWithProviders(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={vi.fn()}
        syncCreateVm={syncCreateVm}
      />,
    );

    ref.current?.onOpen();
    await waitFor(() => {
      expect(screen.getByText("knowledge.createKnowledgeBase")).toBeInTheDocument();
    });

    rerender(
      <CreateKnowledgeBaseModal
        ref={ref}
        onCreate={vi.fn()}
        syncCreateVm={makeSyncCreateVm({ wizardOpen: true })}
      />,
    );

    await waitFor(() => {
      document
        .querySelectorAll(".ant-fade-leave, .ant-zoom-leave")
        .forEach((node) => {
          fireEvent.animationEnd(node);
          fireEvent.transitionEnd(node);
        });
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });
});
