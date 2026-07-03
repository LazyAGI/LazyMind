import type { TFunction } from "i18next";
import { formatDateTime } from "./format";
import { getBindingSchedule, type ScanV2Binding } from "./scanAccessors";

export const DEFAULT_SCHEDULE_TIME = "02:00:00";
export const SCHEDULE_TIME_PATTERN = /^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$/;
export const DEFAULT_SCHEDULE_WEEKDAYS = ["1", "2", "3", "4", "5", "6", "7"];
export const SCHEDULE_WEEKDAY_API_MAP: Record<string, string> = {
  "1": "mon",
  "2": "tue",
  "3": "wed",
  "4": "thu",
  "5": "fri",
  "6": "sat",
  "7": "sun",
};

export function normalizeScheduleTime(scheduleTime?: string) {
  const value = `${scheduleTime || ""}`.trim();
  const minutePrecisionMatch = value.match(/^([01]\d|2[0-3]):[0-5]\d$/);
  if (minutePrecisionMatch) {
    return `${value}:00`;
  }
  return SCHEDULE_TIME_PATTERN.test(value) ? value : DEFAULT_SCHEDULE_TIME;
}

export function normalizeScheduleWeekdays(scheduleWeekdays?: string[]) {
  const uniqueDays = Array.from(
    new Set((scheduleWeekdays || []).map((day) => `${day}`.trim())),
  ).filter((day) => /^[1-7]$/.test(day));
  if (uniqueDays.length === 0) {
    return DEFAULT_SCHEDULE_WEEKDAYS;
  }
  return uniqueDays.sort((left, right) => Number(left) - Number(right));
}

export function buildSchedulePolicy(scheduleWeekdays?: string[], scheduleTime?: string) {
  const weekdays = normalizeScheduleWeekdays(scheduleWeekdays);
  const days =
    weekdays.length === DEFAULT_SCHEDULE_WEEKDAYS.length
      ? ["everyday"]
      : weekdays.map((day) => SCHEDULE_WEEKDAY_API_MAP[day]).filter(Boolean);
  return {
    timezone: "Asia/Shanghai",
    calendar: "weekly",
    rules: [
      {
        days,
        time: normalizeScheduleTime(scheduleTime),
      },
    ],
  };
}

// Shared schedule expression helpers (used by both local reconcile_schedule and
// cloud schedule_expr). New weekly format is `weekly:1,2,3@HH:MM:SS`;
// legacy `daily@HH:MM:SS`, `every2d@HH:MM:SS`, and `every7d@HH:MM:SS`
// are still parsed for existing records.
export function parseReconcileSchedule(expr?: string): {
  scheduleWeekdays: string[];
  scheduleTime: string;
} | null {
  if (!expr) return null;
  const trimmed = expr.trim();
  if (!trimmed) return null;
  const lower = trimmed.toLowerCase();
  if (lower === "manual" || lower === "manual_only") return null;

  const weeklyMatch = trimmed.match(
    /^weekly:([1-7](?:,[1-7])*)@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i,
  );
  if (weeklyMatch) {
    return {
      scheduleWeekdays: normalizeScheduleWeekdays(weeklyMatch[1].split(",")),
      scheduleTime: normalizeScheduleTime(weeklyMatch[2]),
    };
  }
  const dailyMatch = trimmed.match(/^daily@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i);
  if (dailyMatch) {
    return {
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: normalizeScheduleTime(dailyMatch[1]),
    };
  }
  const everyMatch = trimmed.match(/^every(\d+)d@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i);
  if (everyMatch) {
    return {
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: normalizeScheduleTime(everyMatch[2]),
    };
  }
  return null;
}

export function parseFeishuScheduleExpr(expr?: string) {
  const parsed = parseReconcileSchedule(expr);
  if (!parsed) {
    return null;
  }
  return {
    syncMode: "scheduled" as const,
    scheduleWeekdays: parsed.scheduleWeekdays,
    scheduleTime: parsed.scheduleTime,
  };
}

export function getScheduleWeekdaysLabel(scheduleWeekdays: string[], t: TFunction): string {
  const weekdays = normalizeScheduleWeekdays(scheduleWeekdays);
  if (weekdays.length === 7) {
    return t("admin.dataSourceScheduleEveryday");
  }
  return weekdays.map((day) => t(`admin.dataSourceScheduleWeekday${day}`)).join("、");
}

export function buildFeishuScheduleLabel(binding: ScanV2Binding | null, t: TFunction) {
  const parsed = parseFeishuScheduleExpr(getBindingSchedule(binding));
  if (!parsed) {
    return t("admin.dataSourceSyncModeManual");
  }

  return t("admin.dataSourceScheduleLabel", {
    cycle: getScheduleWeekdaysLabel(parsed.scheduleWeekdays, t),
    time: parsed.scheduleTime,
  });
}

export function buildFeishuNextSyncLabel(binding: ScanV2Binding | null, t: TFunction) {
  const nextSyncAt = formatDateTime(binding?.next_sync_at || binding?.nextSyncAt);
  if (nextSyncAt !== "-") {
    return t("admin.dataSourceNextSyncPlanned", {
      time: nextSyncAt,
    });
  }

  const parsed = parseFeishuScheduleExpr(getBindingSchedule(binding));
  if (!parsed) {
    return t("admin.dataSourceNextSyncManual");
  }

  return t("admin.dataSourceNextSyncPlanned", {
    time: parsed.scheduleTime,
  });
}

export function buildScanScheduleLabel(binding: ScanV2Binding | null | undefined, t: TFunction) {
  if (binding?.sync_mode !== "scheduled" && binding?.sync_mode !== "watch") {
    return t("admin.dataSourceSyncModeManual");
  }

  const parsed = parseReconcileSchedule(getBindingSchedule(binding));
  if (parsed) {
    const cycleLabel = getScheduleWeekdaysLabel(parsed.scheduleWeekdays, t);
    return `${cycleLabel} ${parsed.scheduleTime} ${t("admin.dataSourceScheduleAutoSuffix")}`;
  }

  return t("admin.dataSourceSyncModeScheduled");
}

export function buildScanNextSyncLabel(binding: ScanV2Binding | null | undefined, t: TFunction) {
  if (binding?.sync_mode !== "scheduled" && binding?.sync_mode !== "watch") {
    return t("admin.dataSourceNextSyncManual");
  }
  const parsed = parseReconcileSchedule(getBindingSchedule(binding));
  if (parsed) {
    return t("admin.dataSourceNextSyncPlanned", { time: parsed.scheduleTime });
  }
  return t("admin.dataSourceNextSyncPlanned", { time: "-" });
}

export function inferScheduleWeekdays(scheduleLabel: string) {
  const normalized = scheduleLabel.toLowerCase();
  if (
    scheduleLabel.includes("每天") ||
    scheduleLabel.includes("全天") ||
    normalized.includes("daily") ||
    normalized.includes("every day")
  ) {
    return DEFAULT_SCHEDULE_WEEKDAYS;
  }
  const weekdayMap: Array<[string, string[]]> = [
    ["1", ["周一", "星期一", "monday", "mon"]],
    ["2", ["周二", "星期二", "tuesday", "tue"]],
    ["3", ["周三", "星期三", "wednesday", "wed"]],
    ["4", ["周四", "星期四", "thursday", "thu"]],
    ["5", ["周五", "星期五", "friday", "fri"]],
    ["6", ["周六", "星期六", "saturday", "sat"]],
    ["7", ["周日", "周天", "星期日", "星期天", "sunday", "sun"]],
  ];
  const matchedDays = weekdayMap
    .filter(([, labels]) =>
      labels.some((label) => normalized.includes(label.toLowerCase())),
    )
    .map(([day]) => day);
  return normalizeScheduleWeekdays(matchedDays);
}
