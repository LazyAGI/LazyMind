import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { replaceImagesWithKeys, useAutoImageFigure } from "./util";

describe("replaceImagesWithKeys (re-export of collapseImagesToKeys)", () => {
  it("collapses a matching image url back to its storage key basename", () => {
    const md = "![alt](http://localhost/api/core/static-files/abc.png?expires=1)";
    const result = replaceImagesWithKeys(md, ["/static-files/abc.png"]);
    expect(result).toBe("![alt](abc.png)");
  });

  it("leaves the markdown unchanged when no key matches", () => {
    const md = "![alt](/some/other/path.png)";
    expect(replaceImagesWithKeys(md, ["/static-files/abc.png"])).toBe(md);
  });

  it("returns the source unchanged when keys is not an array", () => {
    expect(replaceImagesWithKeys("text", undefined as unknown as string[])).toBe(
      "text",
    );
  });
});

describe("useAutoImageFigure", () => {
  it("wraps unmarked <img> elements with a <figure>/<figcaption> and marks them as handled", () => {
    document.body.innerHTML =
      '<div class="mdx-editor-root"><p><img src="a.png" alt="caption text" /></p></div>';

    renderHook(() => useAutoImageFigure());

    const figure = document.querySelector("figure.mdx-figure");
    expect(figure).not.toBeNull();
    const img = figure?.querySelector("img");
    expect(img?.getAttribute("data-has-figure")).toBe("true");
    expect(
      (figure?.querySelector("figcaption") as HTMLElement | null)?.innerText,
    ).toBe("caption text");
  });

  it("does not re-wrap an image that already has data-has-figure set", () => {
    document.body.innerHTML =
      '<div class="mdx-editor-root"><p><img src="a.png" data-has-figure="true" /></p></div>';

    renderHook(() => useAutoImageFigure());

    expect(document.querySelectorAll("figure.mdx-figure").length).toBe(0);
  });
});
