import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import * as echarts from "echarts/core";
import { PieChart } from "echarts/charts";
import { TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { EChartsOption } from "echarts";
import { formatPercent } from "../shared";

echarts.use([PieChart, TooltipComponent, CanvasRenderer]);

export type AnalysisCategoryPieItem = {
  key: string;
  category: string;
  count: number;
  ratio: string;
  ratioValue: number;
  color: string;
};

type AnalysisCategoryPieChartProps = {
  rows: AnalysisCategoryPieItem[];
  highlightedCategory?: string | null;
  onCategoryHover?: (categoryKey: string | null) => void;
  className?: string;
};

export function AnalysisCategoryPieChart({
  rows,
  highlightedCategory = null,
  onCategoryHover,
  className,
}: AnalysisCategoryPieChartProps) {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container || rows.length === 0) {
      return undefined;
    }

    const chart = echarts.init(container);
    chartRef.current = chart;
    const categoryUnit = t("selfEvolutionRun.categoryUnit");
    const centerLabel = `{count|${rows.length}}\n{unit|${categoryUnit}}`;

    const option: EChartsOption = {
      animationDuration: 420,
      animationEasing: "cubicOut",
      tooltip: {
        trigger: "item",
        appendToBody: true,
        borderWidth: 1,
        borderColor: "#d7e7fb",
        backgroundColor: "rgba(255, 255, 255, 0.96)",
        textStyle: {
          color: "#35506f",
          fontSize: 12,
        },
        formatter: (params) => {
          if (!params || typeof params !== "object" || Array.isArray(params)) {
            return "";
          }
          const value = typeof params.value === "number" ? params.value : Number(params.value);
          const percent = typeof params.percent === "number" ? formatPercent(params.percent / 100) : "-";
          return [
            `<strong>${params.name ?? "-"}</strong>`,
            `${t("selfEvolutionRun.analysisPieTooltipCount")}: ${value}`,
            `${t("selfEvolutionRun.analysisPieTooltipRatio")}: ${percent}`,
          ].join("<br/>");
        },
      },
      series: [
        {
          type: "pie",
          radius: ["46%", "72%"],
          center: ["50%", "50%"],
          selectedMode: "single",
          selectedOffset: 8,
          minAngle: 4,
          avoidLabelOverlap: true,
          itemStyle: {
            borderRadius: 6,
            borderColor: "#f8fbff",
            borderWidth: 2,
          },
          label: {
            show: true,
            position: "center",
            formatter: centerLabel,
            rich: {
              count: {
                color: "#244b77",
                fontSize: 24,
                fontWeight: 800,
                lineHeight: 28,
              },
              unit: {
                color: "#6b84a2",
                fontSize: 12,
                fontWeight: 700,
                lineHeight: 16,
              },
            },
          },
          labelLine: {
            show: false,
          },
          emphasis: {
            scale: true,
            scaleSize: 10,
            label: {
              show: true,
              formatter: centerLabel,
              rich: {
                count: {
                  color: "#244b77",
                  fontSize: 24,
                  fontWeight: 800,
                  lineHeight: 28,
                },
                unit: {
                  color: "#6b84a2",
                  fontSize: 12,
                  fontWeight: 700,
                  lineHeight: 16,
                },
              },
            },
            itemStyle: {
              shadowBlur: 14,
              shadowColor: "rgba(37, 102, 173, 0.22)",
            },
          },
          data: rows.map((item) => ({
            name: item.category,
            value: item.count,
            itemStyle: {
              color: item.color,
            },
          })),
        },
      ],
    };

    const handleMouseOver = (params: { dataIndex?: number }) => {
      const row = typeof params.dataIndex === "number" ? rows[params.dataIndex] : undefined;
      onCategoryHover?.(row?.key ?? null);
    };
    const handleGlobalOut = () => onCategoryHover?.(null);

    chart.setOption(option, true);
    chart.on("mouseover", handleMouseOver);
    chart.on("globalout", handleGlobalOut);

    const resizeObserver = new ResizeObserver(() => {
      chart.resize();
    });
    resizeObserver.observe(container);

    return () => {
      chart.off("mouseover", handleMouseOver);
      chart.off("globalout", handleGlobalOut);
      resizeObserver.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, [onCategoryHover, rows, t]);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || rows.length === 0) {
      return;
    }

    chart.dispatchAction({ type: "downplay", seriesIndex: 0 });
    if (!highlightedCategory) {
      return;
    }

    const dataIndex = rows.findIndex((item) => item.key === highlightedCategory);
    if (dataIndex >= 0) {
      chart.dispatchAction({ type: "highlight", seriesIndex: 0, dataIndex });
    }
  }, [highlightedCategory, rows]);

  return (
    <div
      ref={containerRef}
      className={className}
      role="img"
      aria-label={t("selfEvolutionRun.coarseCategoryPieAria")}
    />
  );
}
