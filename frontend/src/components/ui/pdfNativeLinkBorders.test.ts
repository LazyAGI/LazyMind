import { describe, expect, it } from "vitest";
import { restorePdfLinkAnnotationBorders } from "./pdfNativeLinkBorders";

describe("native PDF link borders", () => {
  it("shows the source PDF's red and green internal links even when /Border has zero width", () => {
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
    expect(result.map(item => item.dest)).toEqual([green.dest, red.dest]);
    expect(result.map(item => item.borderColor)).toEqual([green.borderColor, red.borderColor]);
    expect(green.borderStyle.width).toBe(0);
    expect(red.borderStyle.width).toBe(0);
  });

  it("leaves existing borders and uncolored or non-link annotations untouched", () => {
    const alreadyVisible = { subtype: "Link", borderColor: [0, 255, 0], borderStyle: { width: 2, style: 1 } };
    const uncolored = { subtype: "Link", borderColor: [80, 80, 80], borderStyle: { width: 0, style: 1 } };
    const noColor = { subtype: "Link", borderStyle: { width: 0, style: 1 } };
    const other = { subtype: "Text", borderColor: [255, 0, 0], borderStyle: { width: 0, style: 1 } };

    const result = restorePdfLinkAnnotationBorders({ annotations: [alreadyVisible, uncolored, noColor, other] });

    expect(result).toEqual([alreadyVisible, uncolored, noColor, other]);
    result.forEach((item, index) => expect(item).toBe([alreadyVisible, uncolored, noColor, other][index]));
  });
});
