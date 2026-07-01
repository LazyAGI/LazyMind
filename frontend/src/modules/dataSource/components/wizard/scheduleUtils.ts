export const SCHEDULE_WEEKDAYS = ["1", "2", "3", "4", "5", "6", "7"];
export const SCHEDULE_WEEKDAY_DISPLAY_ORDER = ["7", "1", "2", "3", "4", "5", "6"];
export const SCHEDULE_WORKDAYS = ["1", "2", "3", "4", "5"];
export const SCHEDULE_WEEKENDS = ["6", "7"];

export function normalizeSelectedWeekdays(value?: string[]) {
  return Array.from(new Set(value || []))
    .filter((day) => SCHEDULE_WEEKDAYS.includes(day))
    .sort((left, right) => Number(left) - Number(right));
}

export function isSameWeekdaySet(left: string[], right: string[]) {
  if (left.length !== right.length) {
    return false;
  }

  return left.every((value, index) => value === right[index]);
}

export function toggleShortcutWeekdays(current: string[], target: string[]) {
  return isSameWeekdaySet(current, target) ? [] : target;
}
