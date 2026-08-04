import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, waitFor, fireEvent } from "@/test/testUtils";
import ContextUsageButton from "./ContextUsageButton";

const mockEstimateContextUsage = vi.fn();
const mockExportContextPrompt = vi.fn();

vi.mock("../../utils/request", () => ({
  estimateContextUsage: (...args: unknown[]) => mockEstimateContextUsage(...args),
  exportContextPrompt: (...args: unknown[]) => mockExportContextPrompt(...args),
}));

function baseReport(overrides: Record<string, unknown> = {}) {
  return {
    estimated_tokens: 1200,
    max_input_tokens: 8000,
    estimated_ratio: 0.15,
    requires_llm: false,
    preview_accuracy: "rule_only",
    categories: [
      {
        category_id: "conversation",
        title: "History",
        estimated_tokens: 1200,
        item_count: 2,
        items: [
          { item_id: "i1", title: "User message · hi", char_count: 2, estimated_tokens: 600, content: "hi" },
        ],
      },
    ],
    ...overrides,
  };
}

describe("ContextUsageButton", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the trigger button", () => {
    renderWithProviders(
      <ContextUsageButton staleKey="k1" resetKey="r1" buildRequest={() => ({})} />,
    );
    expect(screen.getByLabelText("chat.contextUsageShow")).toBeInTheDocument();
  });

  it("calculates usage when the popover is opened and shows the estimated token count", async () => {
    mockEstimateContextUsage.mockResolvedValue(baseReport());
    renderWithProviders(
      <ContextUsageButton staleKey="k1" resetKey="r1" buildRequest={() => ({ foo: "bar" })} />,
    );
    fireEvent.click(screen.getByLabelText("chat.contextUsageShow"));
    await waitFor(() => expect(mockEstimateContextUsage).toHaveBeenCalledWith({
      foo: "bar",
      context_preview_allow_llm_routing: false,
    }));
    await waitFor(() => expect(document.querySelector(".context-usage-summary strong")).toBeInTheDocument());
  });

  it("shows an error state and allows retry when the request fails", async () => {
    mockEstimateContextUsage.mockRejectedValueOnce(new Error("boom"));
    renderWithProviders(
      <ContextUsageButton staleKey="k1" resetKey="r1" buildRequest={() => ({})} />,
    );
    fireEvent.click(screen.getByLabelText("chat.contextUsageShow"));
    await waitFor(() => expect(screen.getByText("chat.contextUsageError")).toBeInTheDocument());

    mockEstimateContextUsage.mockResolvedValueOnce(baseReport());
    fireEvent.click(screen.getByText("chat.contextUsageRetry"));
    await waitFor(() => expect(document.querySelector(".context-usage-summary strong")).toBeInTheDocument());
  });

  it("marks the report stale when staleKey changes after a fresh calculation", async () => {
    mockEstimateContextUsage.mockResolvedValue(baseReport());
    const { rerender } = renderWithProviders(
      <ContextUsageButton staleKey="k1" resetKey="r1" buildRequest={() => ({})} />,
    );
    fireEvent.click(screen.getByLabelText("chat.contextUsageShow"));
    await waitFor(() => expect(document.querySelector(".context-usage-summary strong")).toBeInTheDocument());

    rerender(
      <ContextUsageButton staleKey="k2" resetKey="r1" buildRequest={() => ({})} />,
    );
    await waitFor(() => expect(screen.getByText("chat.contextUsageStale")).toBeInTheDocument());
  });

  it("resets all state when resetKey changes", async () => {
    mockEstimateContextUsage.mockResolvedValue(baseReport());
    const { rerender } = renderWithProviders(
      <ContextUsageButton staleKey="k1" resetKey="r1" buildRequest={() => ({})} />,
    );
    fireEvent.click(screen.getByLabelText("chat.contextUsageShow"));
    await waitFor(() => expect(document.querySelector(".context-usage-summary strong")).toBeInTheDocument());

    rerender(
      <ContextUsageButton staleKey="k1" resetKey="r2" buildRequest={() => ({})} />,
    );
    fireEvent.click(screen.getByLabelText("chat.contextUsageShow"));
    await waitFor(() => expect(mockEstimateContextUsage).toHaveBeenCalledTimes(2));
  });

  it("exports the context prompt as a downloadable blob", async () => {
    mockEstimateContextUsage.mockResolvedValue(baseReport());
    const blob = new Blob(["content"], { type: "text/markdown" });
    mockExportContextPrompt.mockResolvedValue(blob);
    const createObjectURL = vi.fn().mockReturnValue("blob:mock-url");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL });

    renderWithProviders(
      <ContextUsageButton staleKey="k1" resetKey="r1" buildRequest={() => ({})} />,
    );
    fireEvent.click(screen.getByLabelText("chat.contextUsageShow"));
    await waitFor(() => expect(screen.getByText("chat.contextUsageViewReport")).toBeInTheDocument());
    fireEvent.click(screen.getByText("chat.contextUsageViewReport"));

    fireEvent.click(await screen.findByText("chat.contextUsageExport"));
    await waitFor(() => expect(mockExportContextPrompt).toHaveBeenCalled());
    await waitFor(() => expect(createObjectURL).toHaveBeenCalledWith(blob));

    vi.unstubAllGlobals();
  });
});
