import { expect, it } from "vitest";
import { paragraphSelectionsOverlap } from "./paragraphSelection";

it("rejects intersecting selections on the same page", () => {
  expect(paragraphSelectionsOverlap(
    { text:"第一段内容", page:1, bbox:[10,10,100,40] },
    { text:"重合内容", page:1, bbox:[80,20,140,50] },
  )).toBe(true);
  expect(paragraphSelectionsOverlap(
    { text:"第一页", page:1, bbox:[10,10,100,40] },
    { text:"第二页", page:2, bbox:[10,10,100,40] },
  )).toBe(false);
});

it("falls back to normalized text containment when coordinates are unavailable", () => {
  expect(paragraphSelectionsOverlap(
    { text:"道路 交通 安全", page:1 },
    { text:"交通安全", page:1 },
  )).toBe(true);
  expect(paragraphSelectionsOverlap(
    { text:"道路交通安全", page:1 },
    { text:"驾驶证申领", page:1 },
  )).toBe(false);
});
