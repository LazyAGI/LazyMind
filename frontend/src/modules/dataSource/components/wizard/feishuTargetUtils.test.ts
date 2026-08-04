import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { getFeishuTargetDisplayText, getFeishuTargetValuePath } from "./feishuTargetUtils";

const t = ((key: string) => key) as unknown as TFunction;

describe("getFeishuTargetDisplayText", () => {
  it("prefers the cached title map entry over the raw value", () => {
    const titleMap = new Map([["target-1", "My Wiki"]]);
    expect(getFeishuTargetDisplayText("target-1", "target-1", t, titleMap)).toBe(
      "My Wiki",
    );
  });

  it("falls back to the label text when it differs from the value", () => {
    expect(getFeishuTargetDisplayText("target-1", "Label Text", t)).toBe("Label Text");
  });

  it("parses a manual target value when no title or label is usable", () => {
    const manualValue = "__scan-feishu-manual-target__:wiki:node-1";
    expect(getFeishuTargetDisplayText(manualValue, manualValue, t)).toBe("node-1");
  });

  it("falls back to the raw value when nothing else resolves", () => {
    expect(getFeishuTargetDisplayText("plain-value", "plain-value", t)).toBe(
      "plain-value",
    );
  });
});

describe("getFeishuTargetValuePath", () => {
  it("prefers the cached breadcrumb path when available", () => {
    const pathMap = new Map([["target-1", "Root / Child"]]);
    expect(getFeishuTargetValuePath("target-1", "target-1", pathMap, t)).toBe(
      "Root / Child",
    );
  });

  it("falls back to the display text resolution when no path is cached", () => {
    expect(getFeishuTargetValuePath("target-1", "Label", new Map(), t)).toBe("Label");
  });
});
