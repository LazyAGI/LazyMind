import { foldSessionPerformanceStats } from "./performanceStats";
import { RoleTypes } from "@/modules/chat/constants/common";

describe("foldSessionPerformanceStats", () => {
  it("counts distinct seq as turns and reports session vs current-turn cache rates", () => {
    const stats = foldSessionPerformanceStats([
      {
        role: RoleTypes.ASSISTANT,
        seq: 1,
        performance_metrics: {
            steps: 2,
            wall_ms: 1800,
            model_ms: 1000,
            tool_ms: 500,
            input_tokens: 100,
            cache_input_tokens: 100,
            output_tokens: 10,
            cached_tokens: 80,
            cache_hit_rate: 0.8,
            context_ratio: 0.1,
            model: "deepseek-chat",
        },
      },
      {
        role: RoleTypes.ASSISTANT,
        seq: 2,
        performance_metrics: {
            steps: 3,
            wall_ms: 2700,
            model_ms: 2000,
            input_tokens: 50,
            cache_input_tokens: 50,
            output_tokens: 20,
            cached_tokens: 0,
            model: "deepseek-chat",
        },
      },
    ]);
    expect(stats?.turns).toBe(2);
    expect(stats?.steps).toBe(5);
    expect(stats?.wallMs).toBe(4500);
    expect(stats?.promptTokens).toBe(150);
    expect(stats?.sessionCacheHitRate).toBeCloseTo(80 / 150);
    expect(stats?.turnCacheHitRate).toBe(0);
    expect(stats?.model).toBe("deepseek-chat");
    expect(stats?.toolMs).toBe(500);
  });

  it("averages multiple metrics in the current turn", () => {
    const stats = foldSessionPerformanceStats([
      {
        role: RoleTypes.ASSISTANT,
        seq: 1,
        performance_metrics: {
            steps: 1,
            input_tokens: 100,
            cached_tokens: 100,
        },
      },
      {
        role: RoleTypes.ASSISTANT,
        seq: 2,
        performance_metrics: {
            steps: 1,
            input_tokens: 40,
            cached_tokens: 20,
        },
        answers: [
          {
            performance_metrics: {
                steps: 1,
                input_tokens: 60,
                cached_tokens: 0,
            },
          },
        ],
      },
    ]);
    expect(stats?.sessionCacheHitRate).toBeCloseTo(120 / 200);
    expect(stats?.turnCacheHitRate).toBeCloseTo(20 / 100);
  });

  it("treats explicit cached_tokens 0 as observable", () => {
    const stats = foldSessionPerformanceStats([
      {
        role: RoleTypes.ASSISTANT,
        seq: 1,
        performance_metrics: {
            steps: 1,
            input_tokens: 100,
            cached_tokens: 0,
        },
      },
    ]);
    expect(stats?.sessionCacheHitRate).toBe(0);
    expect(stats?.turnCacheHitRate).toBe(0);
  });

  it("excludes calls with unknown cache usage from the cache-rate denominator", () => {
    const stats = foldSessionPerformanceStats([
      {
        role: RoleTypes.ASSISTANT,
        seq: 1,
        performance_metrics: {
          steps: 2,
          input_tokens: 200,
          cache_input_tokens: 100,
          cached_tokens: 80,
        },
      },
    ]);
    expect(stats?.sessionCacheHitRate).toBeCloseTo(0.8);
    expect(stats?.turnCacheHitRate).toBeCloseTo(0.8);
  });

  it("does not coerce explicit null observations to measured zero", () => {
    const stats = foldSessionPerformanceStats([
      {
        role: RoleTypes.ASSISTANT,
        seq: 1,
        performance_metrics: {
          steps: 1,
          input_tokens: 100,
          cached_tokens: null,
        } as never,
      },
    ]);
    expect(stats?.sessionCacheHitRate).toBeUndefined();
    expect(stats?.turnCacheHitRate).toBeUndefined();
  });

  it("preserves measured zero token counts and throughput", () => {
    const stats = foldSessionPerformanceStats([
      {
        role: RoleTypes.ASSISTANT,
        seq: 1,
        performance_metrics: {
          steps: 1,
          model_ms: 100,
          input_tokens: 0,
          output_tokens: 0,
        },
      },
    ]);
    expect(stats?.promptTokens).toBe(0);
    expect(stats?.completionTokens).toBe(0);
    expect(stats?.tokS).toBe(0);
  });
});
