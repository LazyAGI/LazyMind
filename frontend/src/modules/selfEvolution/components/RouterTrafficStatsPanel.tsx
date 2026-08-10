import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  DatePicker,
  Empty,
  Segmented,
  Skeleton,
  Statistic,
  Table,
  Typography,
  theme,
  type TableColumnsType,
} from "antd";
import { ArrowLeftOutlined, ReloadOutlined } from "@ant-design/icons";
import dayjs, { type Dayjs } from "dayjs";
import * as echarts from "echarts/core";
import { BarChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { EChartsOption } from "echarts";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  fetchRouterABStrategy,
  fetchRouterTrafficStats,
  getRouterApiErrorMessage,
  type RouterABStrategy,
  type RouterTrafficStats,
} from "../shared/routerApi";

echarts.use([BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

const { Text } = Typography;
const { RangePicker } = DatePicker;

type TrafficPreset = "today" | "7d" | "30d" | "custom";

type AlgorithmRow = RouterTrafficStats["algorithms"][number] & {
  configured_ratio: number | null;
};

type DislikeReasonRow = RouterTrafficStats["dislike_reasons"][number];

function trafficRange(
  preset: TrafficPreset,
  customRange: [Dayjs, Dayjs] | null,
) {
  const end = preset === "custom" ? customRange?.[1] : dayjs();
  const start = preset === "custom"
    ? customRange?.[0]
    : preset === "today"
      ? end?.startOf("day")
      : end?.subtract(preset === "7d" ? 7 : 30, "day");
  if (!start || !end || !start.isBefore(end)) {
    return null;
  }
  return {
    startTime: start.toISOString(),
    endTime: end.toISOString(),
    granularity: (end.diff(start, "hour", true) <= 24 ? "hour" : "day") as "hour" | "day",
  };
}

function percent(value: number | null) {
  return value === null ? "--" : `${(value * 100).toFixed(1)}%`;
}

function RouterTrafficTrend({ stats }: { stats: RouterTrafficStats }) {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container || !stats.trend.length) {
      return undefined;
    }
    const chart = echarts.init(container);
    const algorithmIds = Array.from(new Set([
      ...stats.algorithms.map((item) => item.algorithm_id),
      ...stats.trend.flatMap((point) => Object.keys(point.counts)),
    ])).sort();
    const timeFormat = stats.range.granularity === "hour" ? "MM-DD HH:00" : "MM-DD";
    const option: EChartsOption = {
      animationDuration: 220,
      backgroundColor: "transparent",
      color: [
        token.colorPrimary,
        token.colorSuccess,
        token.colorWarning,
        token.colorError,
        token.colorInfo,
      ],
      grid: { left: 12, right: 12, top: 48, bottom: 8, containLabel: true },
      legend: {
        top: 0,
        left: 0,
        right: 0,
        type: "scroll",
        icon: "roundRect",
        itemWidth: 12,
        itemHeight: 8,
        textStyle: { color: token.colorTextSecondary, fontSize: 12 },
      },
      tooltip: {
        trigger: "axis",
        axisPointer: { type: "shadow" },
        backgroundColor: token.colorBgElevated,
        borderColor: token.colorBorderSecondary,
        textStyle: { color: token.colorText },
        formatter: (rawParams) => {
          const items = (Array.isArray(rawParams) ? rawParams : [rawParams]) as Array<{
            axisValueLabel?: string;
            marker?: string;
            seriesName?: string;
            value?: number;
          }>;
          const total = items.reduce((sum, item) => sum + (Number(item.value) || 0), 0);
          return [
            items[0]?.axisValueLabel || "",
            ...items.map((item) => {
              const count = Number(item.value) || 0;
              return `${item.marker || ""}${item.seriesName || ""}: ${count} (${percent(total ? count / total : 0)})`;
            }),
          ].join("<br/>");
        },
      },
      xAxis: {
        type: "category",
        data: stats.trend.map((point) => dayjs(point.time).format(timeFormat)),
        axisTick: { show: false },
        axisLine: { lineStyle: { color: token.colorBorderSecondary } },
        axisLabel: { color: token.colorTextSecondary, hideOverlap: true, margin: 12 },
      },
      yAxis: {
        type: "value",
        minInterval: 1,
        name: t("selfEvolutionRun.routerTrafficAnswers"),
        nameTextStyle: { color: token.colorTextSecondary, align: "left" },
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { color: token.colorTextSecondary },
        splitLine: {
          lineStyle: { color: token.colorBorderSecondary, type: "dashed" },
        },
      },
      series: algorithmIds.map((algorithmId) => ({
        name: algorithmId,
        type: "bar",
        stack: "answers",
        barMaxWidth: 36,
        itemStyle: { borderRadius: [3, 3, 0, 0] },
        emphasis: { focus: "series" },
        data: stats.trend.map((point) => point.counts[algorithmId] || 0),
      })),
    };
    chart.setOption(option);
    const resizeObserver = new ResizeObserver(() => chart.resize());
    resizeObserver.observe(container);
    return () => {
      resizeObserver.disconnect();
      chart.dispose();
    };
  }, [stats, t, token]);

  return stats.trend.length ? (
    <div ref={containerRef} className="self-evolution-router-traffic-chart" role="img" aria-label={t("selfEvolutionRun.routerTrafficTrend")} />
  ) : (
    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("selfEvolutionRun.routerTrafficNoData")} />
  );
}

function RouterTrafficStatsPanel({ strategy }: { strategy: RouterABStrategy | null }) {
  const { t } = useTranslation();
  const requestIdRef = useRef(0);
  const [preset, setPreset] = useState<TrafficPreset>("7d");
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [stats, setStats] = useState<RouterTrafficStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [updatedAt, setUpdatedAt] = useState<Dayjs | null>(null);
  const range = useMemo(
    () => trafficRange(preset, customRange),
    [preset, customRange],
  );

  useEffect(() => {
    if (!range) {
      setLoading(false);
      return;
    }
    const requestId = ++requestIdRef.current;
    setLoading(true);
    setError("");
    void fetchRouterTrafficStats(range)
      .then((result) => {
        if (requestId !== requestIdRef.current) {
          return;
        }
        if (!result) {
          throw new Error(t("selfEvolutionRun.routerTrafficInvalidResponse"));
        }
        setStats(result);
        setUpdatedAt(dayjs());
      })
      .catch((loadError) => {
        if (requestId === requestIdRef.current) {
          setError(getRouterApiErrorMessage(loadError, t("selfEvolutionRun.routerTrafficLoadFailed")));
        }
      })
      .finally(() => {
        if (requestId === requestIdRef.current) {
          setLoading(false);
        }
      });
  }, [range, refreshKey, t]);

  const algorithmRows = useMemo<AlgorithmRow[]>(() => {
    const statsByID = new Map(stats?.algorithms.map((item) => [item.algorithm_id, item]) || []);
    const algorithmIDs = Array.from(new Set([
      ...statsByID.keys(),
      ...Object.keys(strategy?.weights || {}),
    ])).sort();
    const configuredTotal = Object.values(strategy?.weights || {}).reduce((sum, value) => sum + value, 0);
    return algorithmIDs.map((algorithmID) => {
      const item = statsByID.get(algorithmID);
      return {
        algorithm_id: algorithmID,
        answer_count: item?.answer_count || 0,
        actual_ratio: item?.actual_ratio || 0,
        user_count: item?.user_count || 0,
        conversation_count: item?.conversation_count || 0,
        like_count: item?.like_count || 0,
        dislike_count: item?.dislike_count || 0,
        feedback_rate: item?.feedback_rate || 0,
        positive_rate: item?.positive_rate ?? null,
        configured_ratio: strategy?.active && configuredTotal > 0
          ? (strategy.weights[algorithmID] || 0) / configuredTotal
          : null,
      };
    });
  }, [stats, strategy]);

  const algorithmColumns: TableColumnsType<AlgorithmRow> = [
    {
      title: t("selfEvolutionRun.routerTrafficAlgorithm"),
      dataIndex: "algorithm_id",
      fixed: "left",
      render: (value: string) => <span className="self-evolution-algorithm-mono">{value}</span>,
    },
    {
      title: t("selfEvolutionRun.routerTrafficConfiguredRatio"),
      dataIndex: "configured_ratio",
      align: "right",
      render: percent,
    },
    { title: t("selfEvolutionRun.routerTrafficAnswers"), dataIndex: "answer_count", align: "right" },
    {
      title: t("selfEvolutionRun.routerTrafficActualRatio"),
      dataIndex: "actual_ratio",
      align: "right",
      render: percent,
    },
    { title: t("selfEvolutionRun.routerTrafficUsers"), dataIndex: "user_count", align: "right" },
    { title: t("selfEvolutionRun.routerTrafficConversations"), dataIndex: "conversation_count", align: "right" },
    { title: t("selfEvolutionRun.routerTrafficLikes"), dataIndex: "like_count", align: "right" },
    { title: t("selfEvolutionRun.routerTrafficDislikes"), dataIndex: "dislike_count", align: "right" },
    {
      title: t("selfEvolutionRun.routerTrafficFeedbackRate"),
      dataIndex: "feedback_rate",
      align: "right",
      render: percent,
    },
    {
      title: t("selfEvolutionRun.routerTrafficPositiveRate"),
      dataIndex: "positive_rate",
      align: "right",
      render: percent,
    },
  ];

  const reasonColumns: TableColumnsType<DislikeReasonRow> = [
    {
      title: t("selfEvolutionRun.routerTrafficAlgorithm"),
      dataIndex: "algorithm_id",
      render: (value: string) => <span className="self-evolution-algorithm-mono">{value}</span>,
    },
    {
      title: t("selfEvolutionRun.routerTrafficDislikeReason"),
      dataIndex: "reason",
      render: (value: string) => value || t("selfEvolutionRun.routerTrafficReasonMissing"),
    },
    { title: t("selfEvolutionRun.routerTrafficCount"), dataIndex: "count", align: "right" },
    {
      title: t("selfEvolutionRun.routerTrafficReasonRatio"),
      dataIndex: "ratio",
      align: "right",
      render: percent,
    },
  ];

  return (
    <section className="self-evolution-router-traffic" aria-labelledby="router-traffic-title">
      <div className="self-evolution-router-traffic-header">
        <div>
          <h2 id="router-traffic-title">{t("selfEvolutionRun.routerTrafficTitle")}</h2>
          <Text type="secondary">{t("selfEvolutionRun.routerTrafficDescription")}</Text>
        </div>
        <div className="self-evolution-router-traffic-actions">
          <Segmented<TrafficPreset>
            size="small"
            value={preset}
            options={[
              { value: "today", label: t("selfEvolutionRun.routerTrafficToday") },
              { value: "7d", label: t("selfEvolutionRun.routerTrafficSevenDays") },
              { value: "30d", label: t("selfEvolutionRun.routerTrafficThirtyDays") },
              { value: "custom", label: t("selfEvolutionRun.routerTrafficCustom") },
            ]}
            onChange={setPreset}
          />
          {preset === "custom" ? (
            <RangePicker
              showTime
              value={customRange}
              onChange={(value: [Dayjs | null, Dayjs | null] | null) => (
                setCustomRange(value?.[0] && value[1] ? [value[0], value[1]] : null)
              )}
            />
          ) : null}
          <Button
            size="small"
            icon={<ReloadOutlined />}
            loading={loading}
            disabled={!range}
            onClick={() => setRefreshKey((value) => value + 1)}
          >
            {t("selfEvolutionRun.routerTrafficRefresh")}
          </Button>
        </div>
      </div>
      {updatedAt ? (
        <Text type="secondary" className="self-evolution-router-traffic-updated">
          {t("selfEvolutionRun.routerTrafficUpdatedAt", { time: updatedAt.format("YYYY-MM-DD HH:mm:ss") })}
        </Text>
      ) : null}

      {error ? (
        <Alert
          type="error"
          showIcon
          message={t("selfEvolutionRun.routerTrafficLoadFailed")}
          description={error}
          action={<Button size="small" onClick={() => setRefreshKey((value) => value + 1)}>{t("selfEvolutionRun.retry")}</Button>}
        />
      ) : loading && !stats ? (
        <Skeleton active paragraph={{ rows: 8 }} />
      ) : stats ? (
        <div className="self-evolution-router-traffic-content">
          <div className="self-evolution-router-traffic-summary">
            <Statistic title={t("selfEvolutionRun.routerTrafficTotalAnswers")} value={stats.summary.answer_count} />
            <Statistic title={t("selfEvolutionRun.routerTrafficUsers")} value={stats.summary.user_count} />
            <Statistic title={t("selfEvolutionRun.routerTrafficConversations")} value={stats.summary.conversation_count} />
            <Statistic title={t("selfEvolutionRun.routerTrafficFeedbackCoverage")} value={percent(stats.summary.feedback_rate)} />
          </div>

          <div className="self-evolution-router-traffic-block">
            <h3>{t("selfEvolutionRun.routerTrafficComparison")}</h3>
            <Table<AlgorithmRow>
              rowKey="algorithm_id"
              size="small"
              pagination={false}
              columns={algorithmColumns}
              dataSource={algorithmRows}
              scroll={{ x: 1180 }}
              locale={{ emptyText: t("selfEvolutionRun.routerTrafficNoData") }}
            />
          </div>

          <div className="self-evolution-router-traffic-block">
            <h3>{t("selfEvolutionRun.routerTrafficTrend")}</h3>
            <RouterTrafficTrend stats={stats} />
          </div>

          <div className="self-evolution-router-traffic-block">
            <h3>{t("selfEvolutionRun.routerTrafficDislikeReasons")}</h3>
            <Table<DislikeReasonRow>
              rowKey={(row: DislikeReasonRow) => `${row.algorithm_id}:${row.reason}`}
              size="small"
              pagination={false}
              columns={reasonColumns}
              dataSource={stats.dislike_reasons}
              locale={{ emptyText: t("selfEvolutionRun.routerTrafficNoFeedback") }}
            />
          </div>
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("selfEvolutionRun.routerTrafficChooseRange")} />
      )}
    </section>
  );
}

export function RouterTrafficStatsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [strategy, setStrategy] = useState<RouterABStrategy | null>(null);

  useEffect(() => {
    let active = true;
    void fetchRouterABStrategy()
      .then((result) => {
        if (active) {
          setStrategy(result);
        }
      })
      .catch(() => {
        if (active) {
          setStrategy(null);
        }
      });
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="self-evolution-algorithm-page self-evolution-router-traffic-page">
      <div className="self-evolution-algorithm-shell">
        <header className="self-evolution-algorithm-header">
          <div className="self-evolution-algorithm-title-group">
            <Button
              type="text"
              className="self-evolution-algorithm-back"
              icon={<ArrowLeftOutlined />}
              onClick={() => navigate("/self-evolution/algorithms")}
            >
              {t("common.back")}
            </Button>
            <div className="self-evolution-algorithm-title-copy">
              <Typography.Title level={4}>{t("selfEvolutionRun.routerTrafficPageTitle")}</Typography.Title>
              <Text type="secondary" className="self-evolution-algorithm-subtitle">
                {t("selfEvolutionRun.routerTrafficDescription")}
              </Text>
            </div>
          </div>
        </header>
        <RouterTrafficStatsPanel strategy={strategy} />
      </div>
    </div>
  );
}

export default RouterTrafficStatsPage;
