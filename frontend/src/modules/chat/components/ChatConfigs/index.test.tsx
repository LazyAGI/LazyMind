import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ChatConfigs, { type ChatConfig } from "./index";

const mockListDatasets = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: mockListDatasets,
  }),
  DatabaseBaseServiceApi: () => ({}),
}));

vi.mock("../KnowledgeBaseConfigModal", () => ({
  default: () => null,
}));

describe("ChatConfigs", () => {
  beforeEach(() => {
    mockListDatasets.mockReset();
    mockListDatasets.mockResolvedValue({
      data: {
        datasets: [
          { dataset_id: "ds-1", display_name: "Dataset One" },
          { dataset_id: "ds-2", display_name: "Dataset Two" },
        ],
      },
    });
  });

  it("loads knowledge base options on mount", async () => {
    render(<ChatConfigs configs={{}} onChange={vi.fn()} />);
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalledWith({ pageSize: 1000 }));
  });

  it("selects every knowledge base and toggles back when the select-all label is clicked", async () => {
    render(<ChatConfigs configs={{}} onChange={vi.fn()} />);
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalled());

    fireEvent.click(await screen.findByText("chat.selectAll"));
    expect(await screen.findByText("chat.cancelSelectAll")).toBeInTheDocument();

    fireEvent.click(screen.getByText("chat.cancelSelectAll"));
    expect(await screen.findByText("chat.selectAll")).toBeInTheDocument();
  });

  it("applies incoming configs to the form fields", async () => {
    const configs: ChatConfig = {
      knowledgeBaseId: ["ds-1"],
      databaseBaseId: "db-1",
    };
    render(<ChatConfigs configs={configs} onChange={vi.fn()} />);
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalled());

    // In jsdom, antd's maxTagCount="responsive" collapses selected chips into
    // a "+N ..." summary because it cannot measure real layout, so open the
    // dropdown and assert the pre-selected option is marked selected instead
    // of relying on a visible chip label in the closed selector.
    fireEvent.mouseDown(document.querySelector("#knowledgeBaseId") as Element);
    const selectedOption = await screen.findByText("Dataset One");
    expect(selectedOption.closest('[aria-selected="true"]')).toBeInTheDocument();
  });
});
