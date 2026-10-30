import { afterEach, describe, expect, it, vi } from "vitest";
import { revealModelSetting } from "./revealModelSetting";

afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); });

describe("model settings deep-link scrolling", () => {
  it.each([
    { top: 150, bottom: 250, expected: null },
    { top: 650, bottom: 750, expected: 250 },
    { top: 50, bottom: 150, expected: 50 },
    { top: 150, bottom: 950, expected: 150 },
  ])("reveals row $top..$bottom only within the content pane", ({ top, bottom, expected }) => {
    const shell = document.createElement("div");
    shell.style.overflow = "hidden";
    const pane = document.createElement("div");
    pane.className = "settings-reference-scroll";
    const row = document.createElement("div");
    shell.append(pane); pane.append(row); document.body.append(shell);
    shell.scrollTop = 0;
    pane.scrollTop = 100;
    Object.defineProperty(pane, "clientHeight", { value: 500 });
    vi.spyOn(pane, "getBoundingClientRect").mockReturnValue({ top: 100, bottom: 600 } as DOMRect);
    vi.spyOn(row, "getBoundingClientRect").mockReturnValue({ top, bottom } as DOMRect);
    pane.scrollTo = vi.fn();
    row.scrollIntoView = vi.fn();
    revealModelSetting(row);
    if (expected === null) expect(pane.scrollTo).not.toHaveBeenCalled();
    else expect(pane.scrollTo).toHaveBeenCalledWith({ top: expected, behavior: "smooth" });
    expect(row.scrollIntoView).not.toHaveBeenCalled();
    expect(shell.scrollTop).toBe(0);
  });

  it("does not scroll the document when rendered without a settings pane", () => {
    const row = document.createElement("div");
    document.body.append(row);
    row.scrollIntoView = vi.fn();
    revealModelSetting(row);
    expect(row.scrollIntoView).not.toHaveBeenCalled();
  });
});
