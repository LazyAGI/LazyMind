import { describe, expect, it } from "vitest";
import {
  isSameWeekdaySet,
  normalizeSelectedWeekdays,
  SCHEDULE_WEEKENDS,
  toggleShortcutWeekdays,
} from "./scheduleUtils";

describe("normalizeSelectedWeekdays", () => {
  it("dedupes, filters invalid entries, and sorts numerically", () => {
    expect(normalizeSelectedWeekdays(["3", "1", "3", "9"])).toEqual(["1", "3"]);
  });

  it("returns an empty array for undefined input", () => {
    expect(normalizeSelectedWeekdays(undefined)).toEqual([]);
  });
});

describe("isSameWeekdaySet", () => {
  it("returns true for identical ordered sets", () => {
    expect(isSameWeekdaySet(["1", "2"], ["1", "2"])).toBe(true);
  });

  it("returns false for different lengths or ordering", () => {
    expect(isSameWeekdaySet(["1", "2"], ["1"])).toBe(false);
    expect(isSameWeekdaySet(["1", "2"], ["2", "1"])).toBe(false);
  });
});

describe("toggleShortcutWeekdays", () => {
  it("clears the selection when it matches the target set exactly", () => {
    expect(toggleShortcutWeekdays(SCHEDULE_WEEKENDS, SCHEDULE_WEEKENDS)).toEqual([]);
  });

  it("applies the target set when the current selection differs", () => {
    expect(toggleShortcutWeekdays(["1"], SCHEDULE_WEEKENDS)).toEqual(SCHEDULE_WEEKENDS);
  });
});
