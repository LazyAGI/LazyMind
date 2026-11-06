import { describe, expect, it } from "vitest";
import { restorePdfLinkAnnotationBorders } from "./pdfNativeLinkBorders";

describe("native PDF link borders", () => {
  it("uses restrained red and green PDF.js borders for internal links with a zero-width /Border", () => {
    const green = {
      id: "628R", subtype: "Link", dest: "cite.liang2022mind",
      borderColor: new Uint8ClampedArray([0, 255, 0]),
      borderStyle: { width: 0, rawWidth: 1, style: 1 },
    };
    const red = {
      id: "629R", subtype: "Link", dest: "figure.caption.20",
      borderColor: new Uint8ClampedArray([255, 0, 0]),
      borderStyle: { width: 0, rawWidth: 1, style: 1 },
    };

    const result = restorePdfLinkAnnotationBorders({ annotations: [green, red] });

    expect(result.map(item => item.borderStyle.width)).toEqual([1, 1]);
    expect(result.map(item => Array.from(item.borderColor))).toEqual([[54, 154, 78], [200, 68, 68]]);
    expect(result.map(item => item.dest)).toEqual([green.dest, red.dest]);
    expect(Array.from(green.borderColor)).toEqual([0, 255, 0]);
    expect(Array.from(red.borderColor)).toEqual([255, 0, 0]);
    expect(green.borderStyle.width).toBe(0);
    expect(red.borderStyle.width).toBe(0);
  });

  it("tones visible native internal borders without changing their width", () => {
    const alreadyVisible = { subtype: "Link", borderColor: [0, 255, 0], borderStyle: { width: 2, style: 1 } };
    const result = restorePdfLinkAnnotationBorders({ annotations: [{ ...alreadyVisible, dest: "cite" }] });
    expect(result[0].borderStyle.width).toBe(2);
    expect(Array.from(result[0].borderColor)).toEqual([54, 154, 78]);
  });

  it("leaves uncolored, external, authored-appearance and non-link annotations untouched", () => {
    const alreadyVisible = { subtype: "Link", borderColor: [0, 255, 0], borderStyle: { width: 2, style: 1 } };
    const uncolored = { subtype: "Link", borderColor: [80, 80, 80], borderStyle: { width: 0, style: 1 } };
    const noColor = { subtype: "Link", borderStyle: { width: 0, style: 1 } };
    const other = { subtype: "Text", borderColor: [255, 0, 0], borderStyle: { width: 0, style: 1 } };
    const appearance = { subtype: "Link", dest: "ref", hasAppearance: true, borderColor: [0, 255, 0], borderStyle: { width: 0, style: 1 } };
    const external = { subtype: "Link", url: "https://example.org", borderColor: [0, 255, 0], borderStyle: { width: 0, style: 1 } };

    const result = restorePdfLinkAnnotationBorders({ annotations: [alreadyVisible, uncolored, noColor, other, appearance, external] });

    expect(result).toEqual([alreadyVisible, uncolored, noColor, other, appearance, external]);
    result.forEach((item, index) => expect(item).toBe([alreadyVisible, uncolored, noColor, other, appearance, external][index]));
  });
});
