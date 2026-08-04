import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/testUtils";
import { AnalysisCategoryPieChart, type AnalysisCategoryPieItem } from "./AnalysisCategoryPieChart";

const mockChart = {
  setOption: vi.fn(),
  resize: vi.fn(),
  dispose: vi.fn(),
  on: vi.fn(),
  off: vi.fn(),
  dispatchAction: vi.fn(),
};

vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: vi.fn(() => mockChart),
}));
vi.mock("echarts/charts", () => ({ PieChart: {} }));
vi.mock("echarts/components", () => ({ TooltipComponent: {} }));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

function makeRow(overrides: Partial<AnalysisCategoryPieItem> = {}): AnalysisCategoryPieItem {
  return {
    key: "cat-1",
    category: "Category A",
    count: 5,
    ratio: "50%",
    ratioValue: 0.5,
    color: "#123456",
    ...overrides,
  };
}

describe("AnalysisCategoryPieChart", () => {
  it("initializes the chart and calls setOption when rows are provided", () => {
    renderWithProviders(<AnalysisCategoryPieChart rows={[makeRow()]} />);
    expect(mockChart.setOption).toHaveBeenCalled();
  });

  it("does not attempt to init the chart when rows are empty", async () => {
    const echarts = await import("echarts/core");
    (echarts.init as ReturnType<typeof vi.fn>).mockClear();
    renderWithProviders(<AnalysisCategoryPieChart rows={[]} />);
    expect(echarts.init).not.toHaveBeenCalled();
  });

  it("renders a container with the pie chart aria label", () => {
    const { container } = renderWithProviders(<AnalysisCategoryPieChart rows={[makeRow()]} />);
    expect(container.querySelector('[role="img"]')).toHaveAttribute(
      "aria-label",
      "selfEvolutionRun.coarseCategoryPieAria",
    );
  });

  it("dispatches a highlight action for the matching row when highlightedCategory changes", () => {
    const rows = [makeRow({ key: "cat-1" }), makeRow({ key: "cat-2", category: "Category B" })];
    const { rerender } = renderWithProviders(<AnalysisCategoryPieChart rows={rows} />);
    mockChart.dispatchAction.mockClear();
    rerender(<AnalysisCategoryPieChart rows={rows} highlightedCategory="cat-2" />);
    expect(mockChart.dispatchAction).toHaveBeenCalledWith({ type: "downplay", seriesIndex: 0 });
    expect(mockChart.dispatchAction).toHaveBeenCalledWith({ type: "highlight", seriesIndex: 0, dataIndex: 1 });
  });

  it("applies the provided className to the container", () => {
    const { container } = renderWithProviders(
      <AnalysisCategoryPieChart rows={[makeRow()]} className="custom-chart" />,
    );
    expect(container.querySelector(".custom-chart")).toBeInTheDocument();
  });
});
