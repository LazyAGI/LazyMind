import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { downloadFile, downloadStream, downloadUrl } from "./download";

describe("downloadStream", () => {
  beforeEach(() => {
    vi.stubGlobal("URL", {
      ...URL,
      createObjectURL: vi.fn(() => "blob:mock-url"),
      revokeObjectURL: vi.fn(),
    });
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("creates a temporary anchor, clicks it, and removes it from the DOM", () => {
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const blob = new Blob(["hello"], { type: "text/plain" });

    downloadStream(blob, "report.txt");

    expect(window.URL.createObjectURL).toHaveBeenCalledWith(blob);
    expect(clickSpy).toHaveBeenCalledTimes(1);
    expect(document.querySelector("a[download]")).toBeNull();
    clickSpy.mockRestore();
  });

  it("revokes the object URL after the timeout fires", () => {
    const blob = new Blob(["hello"], { type: "text/plain" });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    downloadStream(blob, "report.txt");
    expect(window.URL.revokeObjectURL).not.toHaveBeenCalled();

    vi.runAllTimers();
    expect(window.URL.revokeObjectURL).toHaveBeenCalledWith("blob:mock-url");
  });
});

describe("downloadUrl", () => {
  it("opens in _self by default and cleans up the anchor", () => {
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    downloadUrl("https://example.com/file.pdf");

    expect(clickSpy).toHaveBeenCalledTimes(1);
    expect(document.body.querySelector("a")).toBeNull();
    clickSpy.mockRestore();
  });

  it("respects a custom target", () => {
    let capturedTarget = "";
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(function mockClick(this: HTMLAnchorElement) {
        capturedTarget = this.target;
      });

    downloadUrl("https://example.com/file.pdf", "_blank");

    expect(capturedTarget).toBe("_blank");
    clickSpy.mockRestore();
  });
});

describe("downloadFile", () => {
  it("appends a hidden iframe pointing at the given url", () => {
    downloadFile("https://example.com/preview");

    const iframe = document.querySelector<HTMLIFrameElement>("iframe.downloadIframe");
    expect(iframe).not.toBeNull();
    expect(iframe?.src).toBe("https://example.com/preview");
    expect(iframe?.style.display).toBe("none");

    iframe?.remove();
  });
});
