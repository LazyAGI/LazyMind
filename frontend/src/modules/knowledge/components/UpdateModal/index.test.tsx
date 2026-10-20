import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import UpdateModal, { type UpdateImperativeProps } from "./index";
import { getKnowledgeBaseCapabilities, getLearningCatalog, saveKnowledgeBaseCapabilities } from "@/modules/learning/api";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceAllDatasetTags: vi.fn().mockResolvedValue({ data: { tags: ["test"] } }),
    datasetServiceListAlgos: vi.fn().mockResolvedValue({ data: { algos: [{ algo_id: "general_algo" }] } }),
  }),
}));
vi.mock("@/modules/learning/api", () => ({
  getKnowledgeBaseCapabilities: vi.fn(), getLearningCatalog: vi.fn(), saveKnowledgeBaseCapabilities: vi.fn(),
}));
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getKnowledgeBaseCapabilities).mockResolvedValue([]);
  vi.mocked(getLearningCatalog).mockResolvedValue({ capabilities: [], profiles: [], question_types: [], local_available: true });
  vi.mocked(saveKnowledgeBaseCapabilities).mockRejectedValue(new Error("at least one capability is required"));
});
afterEach(cleanup);

async function open(reject = false) {
  const ref = createRef<UpdateImperativeProps>();
  const onUpdate = reject ? vi.fn().mockRejectedValue(new Error("at least one capability is required")) : vi.fn().mockResolvedValue(undefined);
  render(<ConfigProvider theme={{ token: { motion: false } }}><UpdateModal ref={ref} onUpdate={onUpdate} /></ConfigProvider>);
  await act(async () => ref.current!.onOpen({ dataset_id: "mcp-library", display_name: "MCP-library", desc: "before", tags: ["test"], algo: { algo_id: "general_algo" } }));
  await screen.findByRole("dialog");
  return onUpdate;
}

describe("metadata-only library editing", () => {
  it("closes after saving an MCP-created library without adding learning capabilities", async () => {
    const onUpdate = await open();
    fireEvent.change(screen.getByLabelText("knowledge.knowledgeDesc"), { target: { value: "after" } });
    fireEvent.click(screen.getByRole("button", { name: "OK" }));
    await waitFor(() => expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ desc: "after" })));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
  });
  it("does not resave unchanged configured learning capabilities", async () => {
    vi.mocked(getKnowledgeBaseCapabilities).mockResolvedValue([{ id: "cap-1", dataset_id: "mcp-library", capability_key: "existing", capability_version: 1, enabled: true, display_order: 4, settings_json: "{}" }]);
    await open();
    fireEvent.click(screen.getByRole("button", { name: "OK" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
  });

  it("still submits clearing existing capabilities and leaves validation to the service", async () => {
    vi.mocked(getKnowledgeBaseCapabilities).mockResolvedValue([{ id: "cap-1", dataset_id: "mcp-library", capability_key: "existing", capability_version: 1, enabled: true, display_order: 1, settings_json: "{}" }]);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      const onUpdate = await open(true);
      const control = screen.getByRole("combobox", { name: "learning.knowledgeBaseCapabilities" });
      fireEvent.click(control.closest(".ant-select")!.querySelector(".ant-select-selection-item-remove")!);
      fireEvent.click(screen.getByRole("button", { name: "OK" }));
      await waitFor(() => expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({reading_capabilities: []})));
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    } finally { consoleError.mockRestore(); }
  });

});
