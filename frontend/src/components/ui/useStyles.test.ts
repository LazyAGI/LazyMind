import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { injectStyles, useStyles } from "./useStyles";

describe("injectStyles", () => {
  it("creates a new style element with the given id and css", () => {
    injectStyles("style-a", ".foo { color: red; }");
    const styleEl = document.getElementById("style-a") as HTMLStyleElement;
    expect(styleEl).not.toBeNull();
    expect(styleEl.textContent).toBe(".foo { color: red; }");
  });

  it("updates the content of an existing style element instead of duplicating it", () => {
    injectStyles("style-b", ".foo { color: red; }");
    injectStyles("style-b", ".foo { color: blue; }");
    const matches = document.querySelectorAll("#style-b");
    expect(matches.length).toBe(1);
    expect(matches[0].textContent).toBe(".foo { color: blue; }");
  });

  it("does not touch the DOM node when the css content is unchanged", () => {
    injectStyles("style-c", ".foo { color: green; }");
    const styleEl = document.getElementById("style-c") as HTMLStyleElement;
    const before = styleEl.textContent;
    injectStyles("style-c", ".foo { color: green; }");
    expect(styleEl.textContent).toBe(before);
  });
});

describe("useStyles", () => {
  it("injects the style into the document head on mount", async () => {
    renderHook(() => useStyles("style-hook", ".bar { color: purple; }"));
    await waitFor(() => {
      const styleEl = document.getElementById("style-hook");
      expect(styleEl).not.toBeNull();
      expect(styleEl?.textContent).toBe(".bar { color: purple; }");
    });
  });

  it("re-injects when the css argument changes across renders", async () => {
    const { rerender } = renderHook(
      ({ css }: { css: string }) => useStyles("style-hook-2", css),
      { initialProps: { css: ".a {}" } },
    );
    await waitFor(() => {
      expect(document.getElementById("style-hook-2")?.textContent).toBe(".a {}");
    });

    rerender({ css: ".b {}" });
    await waitFor(() => {
      expect(document.getElementById("style-hook-2")?.textContent).toBe(".b {}");
    });
  });
});
