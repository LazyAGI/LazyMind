import { describe, expect, it } from "vitest";
import TimeUtils from "./time";

describe("TimeUtils.padZero", () => {
  it("pads a positive number with leading zeros to the default digit width", () => {
    expect(TimeUtils.padZero(5)).toBe("05");
  });

  it("does not pad when the number already has enough digits", () => {
    expect(TimeUtils.padZero(42)).toBe("42");
    expect(TimeUtils.padZero(123, 2)).toBe("123");
  });

  it("supports a custom digit width", () => {
    expect(TimeUtils.padZero(7, 3)).toBe("007");
  });

  it("returns a zero-filled string for falsy or negative input", () => {
    expect(TimeUtils.padZero(0)).toBe("00");
    expect(TimeUtils.padZero(-1)).toBe("00");
    expect(TimeUtils.padZero(NaN)).toBe("00");
  });
});
