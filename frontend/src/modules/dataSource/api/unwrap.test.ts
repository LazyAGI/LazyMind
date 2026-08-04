import { describe, expect, it } from "vitest";
import { unwrapApiData } from "./unwrap";

describe("unwrapApiData", () => {
  it("unwraps a payload with a data envelope", () => {
    const result = unwrapApiData<{ id: string }>({ data: { id: "abc" } });
    expect(result).toEqual({ id: "abc" });
  });

  it("returns the payload as-is when there is no data key", () => {
    const result = unwrapApiData<{ id: string }>({ id: "direct" });
    expect(result).toEqual({ id: "direct" });
  });

  it("returns the payload as-is for primitive values", () => {
    expect(unwrapApiData<number>(42)).toBe(42);
    expect(unwrapApiData<string>("hello")).toBe("hello");
  });

  it("returns null/undefined payloads unchanged", () => {
    expect(unwrapApiData(null)).toBeNull();
    expect(unwrapApiData(undefined)).toBeUndefined();
  });

  it("returns undefined when the data key itself is undefined", () => {
    const result = unwrapApiData<{ id: string } | undefined>({ data: undefined });
    expect(result).toBeUndefined();
  });

  it("treats arrays as non-enveloped payloads", () => {
    const result = unwrapApiData<number[]>([1, 2, 3]);
    expect(result).toEqual([1, 2, 3]);
  });
});
