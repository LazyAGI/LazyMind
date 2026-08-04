import { describe, expect, it } from "vitest";
import UIUtils from "./ui";

describe("UIUtils.jsonParser", () => {
  it("parses valid JSON strings", () => {
    expect(UIUtils.jsonParser('{"a":1}')).toEqual({ a: 1 });
  });

  it("returns the default value for invalid JSON strings", () => {
    expect(UIUtils.jsonParser("not json", { fallback: true })).toEqual({ fallback: true });
  });

  it("returns plain objects unchanged", () => {
    const obj = { foo: "bar" };
    expect(UIUtils.jsonParser(obj)).toBe(obj);
  });

  it("returns the default value for non-object, non-string content", () => {
    expect(UIUtils.jsonParser(42, { fallback: true })).toEqual({ fallback: true });
    expect(UIUtils.jsonParser(null, { fallback: true })).toEqual({ fallback: true });
  });

  it("defaults to an empty object when no default value is provided", () => {
    expect(UIUtils.jsonParser("not json")).toEqual({});
  });
});

describe("UIUtils.isReachBottom", () => {
  function makeElement(scrollTop: number, clientHeight: number, scrollHeight: number) {
    const el = document.createElement("div");
    Object.defineProperty(el, "scrollTop", { value: scrollTop, configurable: true });
    Object.defineProperty(el, "clientHeight", { value: clientHeight, configurable: true });
    Object.defineProperty(el, "scrollHeight", { value: scrollHeight, configurable: true });
    return el;
  }

  it("returns false when the element is falsy", () => {
    expect(UIUtils.isReachBottom(null as unknown as HTMLElement)).toBe(false);
  });

  it("returns false when scrollTop is 0 even if visually at the bottom", () => {
    const el = makeElement(0, 100, 100);
    expect(UIUtils.isReachBottom(el)).toBe(false);
  });

  it("returns true when scrolled within the 2px tolerance of the bottom", () => {
    const el = makeElement(50, 100, 150);
    expect(UIUtils.isReachBottom(el)).toBe(true);
  });

  it("returns false when there is still meaningful distance to the bottom", () => {
    const el = makeElement(10, 100, 500);
    expect(UIUtils.isReachBottom(el)).toBe(false);
  });
});
