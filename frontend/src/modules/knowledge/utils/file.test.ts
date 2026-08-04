import { afterEach, describe, expect, it, vi } from "vitest";
import FileUtils from "./file";

describe("FileUtils.formatFileSize", () => {
  it("returns 0B for falsy/zero/non-numeric input", () => {
    expect(FileUtils.formatFileSize(0)).toBe("0B");
    expect(FileUtils.formatFileSize(undefined)).toBe("0B");
    expect(FileUtils.formatFileSize("not-a-number")).toBe("0B");
  });

  it("formats bytes below 1024 as B", () => {
    expect(FileUtils.formatFileSize(512)).toBe("512.0B");
  });

  it("converts to KB/MB/GB with the default 1-decimal precision", () => {
    expect(FileUtils.formatFileSize(1024)).toBe("1.0KB");
    expect(FileUtils.formatFileSize(1024 * 1024)).toBe("1.0MB");
    expect(FileUtils.formatFileSize(1024 * 1024 * 1024)).toBe("1.0GB");
  });

  it("respects a custom digits parameter", () => {
    expect(FileUtils.formatFileSize(1536, 2)).toBe("1.50KB");
  });

  it("accepts numeric strings", () => {
    expect(FileUtils.formatFileSize("2048")).toBe("2.0KB");
  });
});

describe("FileUtils.getSuffix", () => {
  it("returns the lowercase suffix without the dot by default", () => {
    expect(FileUtils.getSuffix("report.PDF")).toBe("pdf");
  });

  it("returns the suffix with the dot when withDot is true", () => {
    expect(FileUtils.getSuffix("archive.tar.gz", true)).toBe(".gz");
  });

  it("returns an empty string when there is no extension", () => {
    expect(FileUtils.getSuffix("README")).toBe("");
  });
});

describe("FileUtils.getFileTypeFromURI", () => {
  it("returns empty string for empty uri", () => {
    expect(FileUtils.getFileTypeFromURI("")).toBe("");
  });

  it("extracts the suffix from an absolute URL", () => {
    expect(FileUtils.getFileTypeFromURI("https://example.com/path/file.PNG?x=1")).toBe("png");
  });

  it("extracts the suffix from a relative path via window.location.origin", () => {
    expect(FileUtils.getFileTypeFromURI("/static/doc.docx")).toBe("docx");
  });

  it("falls back to manual parsing when URL construction throws", () => {
    // A string with no scheme and invalid characters may still resolve via base origin;
    // simulate the catch path by spying on URL to throw.
    const originalURL = global.URL;
    // @ts-expect-error intentional override for the failure branch
    global.URL = function () {
      throw new Error("invalid url");
    };
    expect(FileUtils.getFileTypeFromURI("weird://uri.txt?y=2")).toBe("txt");
    global.URL = originalURL;
  });
});

describe("FileUtils.normalizeExtensionToLower", () => {
  it("lowercases only the extension part", () => {
    expect(FileUtils.normalizeExtensionToLower("MyFile.TXT")).toBe("MyFile.txt");
  });

  it("returns the original string when there is no extension", () => {
    expect(FileUtils.normalizeExtensionToLower("NoExtension")).toBe("NoExtension");
  });
});

describe("FileUtils.timeoutSignal", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns an AbortSignal that aborts after the timeout elapses", () => {
    vi.useFakeTimers();
    const signal = FileUtils.timeoutSignal(1000);
    expect(signal.aborted).toBe(false);
    vi.advanceTimersByTime(1000);
    expect(signal.aborted).toBe(true);
  });
});

describe("FileUtils.putFile", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("rejects immediately when url or file is missing", async () => {
    await expect(FileUtils.putFile({ file: undefined as any, url: "http://x" })).rejects.toEqual({});
    await expect(FileUtils.putFile({ file: new Blob(["a"]), url: "" })).rejects.toEqual({});
  });

  it("resolves with the response when the upload succeeds", async () => {
    const fakeResponse = { ok: true };
    vi.spyOn(global, "fetch").mockResolvedValue(fakeResponse as Response);

    const result = await FileUtils.putFile({ file: new Blob(["a"]), url: "http://x" });
    expect(result).toBe(fakeResponse);
    expect(global.fetch).toHaveBeenCalledWith(
      "http://x",
      expect.objectContaining({ method: "put" }),
    );
  });

  it("rejects when the response is not ok", async () => {
    vi.spyOn(global, "fetch").mockResolvedValue({ ok: false } as Response);

    await expect(FileUtils.putFile({ file: new Blob(["a"]), url: "http://x" })).rejects.toEqual({});
  });
});
