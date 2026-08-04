import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import {
  DEFAULT_SCHEDULE_TIME,
  DEFAULT_SCHEDULE_WEEKDAYS,
  buildFeishuNextSyncLabel,
  buildFeishuScheduleLabel,
  buildScanNextSyncLabel,
  buildScanScheduleLabel,
  buildSchedulePolicy,
  getScheduleWeekdaysLabel,
  inferScheduleWeekdays,
  normalizeScheduleTime,
  normalizeScheduleWeekdays,
  parseFeishuScheduleExpr,
  parseReconcileSchedule,
} from "./schedule";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as unknown as TFunction;

describe("normalizeScheduleTime", () => {
  it("defaults to DEFAULT_SCHEDULE_TIME for empty or invalid input", () => {
    expect(normalizeScheduleTime(undefined)).toBe(DEFAULT_SCHEDULE_TIME);
    expect(normalizeScheduleTime("nonsense")).toBe(DEFAULT_SCHEDULE_TIME);
  });

  it("appends :00 to minute-precision times", () => {
    expect(normalizeScheduleTime("08:30")).toBe("08:30:00");
  });

  it("keeps already-valid HH:mm:ss times", () => {
    expect(normalizeScheduleTime("23:59:59")).toBe("23:59:59");
  });
});

describe("normalizeScheduleWeekdays", () => {
  it("returns defaults when the list is empty or all invalid", () => {
    expect(normalizeScheduleWeekdays(undefined)).toEqual(DEFAULT_SCHEDULE_WEEKDAYS);
    expect(normalizeScheduleWeekdays(["8", "0", "abc"])).toEqual(DEFAULT_SCHEDULE_WEEKDAYS);
  });

  it("dedupes and sorts valid weekday tokens", () => {
    expect(normalizeScheduleWeekdays(["3", "1", "3", "2"])).toEqual(["1", "2", "3"]);
  });
});

describe("buildSchedulePolicy", () => {
  it("uses 'everyday' shortcut when all 7 weekdays are selected", () => {
    const policy = buildSchedulePolicy(DEFAULT_SCHEDULE_WEEKDAYS, "08:00");
    expect(policy.rules[0].days).toEqual(["everyday"]);
    expect(policy.rules[0].time).toBe("08:00:00");
  });

  it("maps a partial weekday set to named days", () => {
    const policy = buildSchedulePolicy(["1", "3"], "09:00:00");
    expect(policy.rules[0].days).toEqual(["mon", "wed"]);
  });
});

describe("parseReconcileSchedule", () => {
  it("returns null for manual/empty/undefined expressions", () => {
    expect(parseReconcileSchedule(undefined)).toBeNull();
    expect(parseReconcileSchedule("")).toBeNull();
    expect(parseReconcileSchedule("manual")).toBeNull();
    expect(parseReconcileSchedule("MANUAL_ONLY")).toBeNull();
  });

  it("parses weekly expressions", () => {
    expect(parseReconcileSchedule("weekly:1,3,5@09:00:00")).toEqual({
      scheduleWeekdays: ["1", "3", "5"],
      scheduleTime: "09:00:00",
    });
  });

  it("parses legacy daily expressions", () => {
    expect(parseReconcileSchedule("daily@08:00")).toEqual({
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: "08:00:00",
    });
  });

  it("parses legacy everyNd expressions", () => {
    expect(parseReconcileSchedule("every2d@10:00")).toEqual({
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: "10:00:00",
    });
  });

  it("returns null for unrecognized formats", () => {
    expect(parseReconcileSchedule("weird-format")).toBeNull();
  });
});

describe("parseFeishuScheduleExpr", () => {
  it("wraps a parsed schedule with syncMode: scheduled", () => {
    expect(parseFeishuScheduleExpr("daily@08:00")).toEqual({
      syncMode: "scheduled",
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: "08:00:00",
    });
  });

  it("returns null when nothing parses", () => {
    expect(parseFeishuScheduleExpr("manual")).toBeNull();
  });
});

describe("getScheduleWeekdaysLabel", () => {
  it("returns the everyday label when all 7 days are selected", () => {
    expect(getScheduleWeekdaysLabel(DEFAULT_SCHEDULE_WEEKDAYS, t)).toBe(
      "admin.dataSourceScheduleEveryday",
    );
  });

  it("joins individual weekday labels with a Chinese comma", () => {
    expect(getScheduleWeekdaysLabel(["1", "3"], t)).toBe(
      "admin.dataSourceScheduleWeekday1、admin.dataSourceScheduleWeekday3",
    );
  });
});

describe("buildFeishuScheduleLabel / buildFeishuNextSyncLabel", () => {
  it("returns the manual label when there is no binding schedule", () => {
    expect(buildFeishuScheduleLabel(null, t)).toBe("admin.dataSourceSyncModeManual");
    expect(buildFeishuNextSyncLabel(null, t)).toBe("admin.dataSourceNextSyncManual");
  });

  it("formats a scheduled binding's cycle/time label", () => {
    const binding = { schedule_expr: "weekly:1,2@09:00:00" } as never;
    expect(buildFeishuScheduleLabel(binding, t)).toContain(
      "admin.dataSourceScheduleLabel",
    );
  });

  it("prefers a concrete next_sync_at over the derived schedule time", () => {
    const binding = { next_sync_at: "2026-01-01T09:00:00Z" } as never;
    expect(buildFeishuNextSyncLabel(binding, t)).toContain(
      "admin.dataSourceNextSyncPlanned",
    );
  });
});

describe("buildScanScheduleLabel / buildScanNextSyncLabel", () => {
  it("returns manual labels when the sync mode is manual", () => {
    const binding = { sync_mode: "manual" } as never;
    expect(buildScanScheduleLabel(binding, t)).toBe("admin.dataSourceSyncModeManual");
    expect(buildScanNextSyncLabel(binding, t)).toBe("admin.dataSourceNextSyncManual");
  });

  it("returns manual labels for a null/undefined binding", () => {
    expect(buildScanScheduleLabel(undefined, t)).toBe("admin.dataSourceSyncModeManual");
  });

  it("formats scheduled/watch bindings with a parsed schedule expression", () => {
    const binding = {
      sync_mode: "scheduled",
      schedule_expr: "daily@08:00",
    } as never;
    expect(buildScanScheduleLabel(binding, t)).toContain(
      "admin.dataSourceScheduleAutoSuffix",
    );
  });

  it("falls back to the generic scheduled label when the expr can't be parsed", () => {
    const binding = { sync_mode: "watch", schedule_expr: "" } as never;
    expect(buildScanScheduleLabel(binding, t)).toBe("admin.dataSourceSyncModeScheduled");
  });
});

describe("inferScheduleWeekdays", () => {
  it("detects daily/everyday keywords in Chinese and English", () => {
    expect(inferScheduleWeekdays("每天")).toEqual(DEFAULT_SCHEDULE_WEEKDAYS);
    expect(inferScheduleWeekdays("every day")).toEqual(DEFAULT_SCHEDULE_WEEKDAYS);
  });

  it("infers specific weekdays from mixed language labels", () => {
    expect(inferScheduleWeekdays("周一、周三")).toEqual(["1", "3"]);
    expect(inferScheduleWeekdays("Monday and Friday")).toEqual(["1", "5"]);
  });

  it("falls back to defaults when nothing matches", () => {
    expect(inferScheduleWeekdays("unrecognized")).toEqual(DEFAULT_SCHEDULE_WEEKDAYS);
  });
});
