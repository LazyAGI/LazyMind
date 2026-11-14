import { expect, it } from "vitest";
import { rangeAtUnicodeOffset } from "./readingPosition";
it("restores a codepoint offset across text nodes without splitting supplementary characters", () => {
  const el = document.createElement("p");
  el.append(document.createTextNode("甲😀"));
  const span = document.createElement("span");
  span.textContent = "乙𐐀丙";
  el.append(span);
  const r = rangeAtUnicodeOffset(el, 3);
  expect(r.startContainer).toBe(span.firstChild);
  expect(r.startOffset).toBe(1);
  expect(r.toString()).toBe("𐐀");
});
