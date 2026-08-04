import { describe, expect, it } from "vitest";
import { fileToBase64 } from "./upload";

describe("fileToBase64", () => {
  it("resolves with a data URL for the given file content", async () => {
    const file = new File(["hello world"], "hello.txt", { type: "text/plain" });

    const result = await fileToBase64(file);

    expect(typeof result).toBe("string");
    expect(result as string).toMatch(/^data:text\/plain;base64,/);
  });

  it("rejects when the FileReader emits an error", async () => {
    const file = new File(["boom"], "boom.txt", { type: "text/plain" });
    const originalReadAsDataURL = FileReader.prototype.readAsDataURL;
    FileReader.prototype.readAsDataURL = function mockReadAsDataURL(this: FileReader) {
      this.onerror?.(new ProgressEvent("error") as never);
    };

    try {
      await expect(fileToBase64(file)).rejects.toBeInstanceOf(ProgressEvent);
    } finally {
      FileReader.prototype.readAsDataURL = originalReadAsDataURL;
    }
  });

  it("produces different payloads for different file contents", async () => {
    const fileA = new File(["aaa"], "a.txt", { type: "text/plain" });
    const fileB = new File(["bbb"], "b.txt", { type: "text/plain" });

    const [resultA, resultB] = await Promise.all([fileToBase64(fileA), fileToBase64(fileB)]);

    expect(resultA).not.toBe(resultB);
  });
});
