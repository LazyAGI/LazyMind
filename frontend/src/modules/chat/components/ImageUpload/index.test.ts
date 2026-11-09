import { createElement, createRef } from "react";
import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import ImageUpload, {
  type ImageUploadImperativeProps,
  allowedFileTypes,
  allowedImageTypes,
  allowedSpreadsheetTypes,
  allowedTextTypes,
  allowedUploadTypes,
} from ".";

vi.mock("@/modules/chat/utils/chunkUpload", () => ({
  uploadFileInChunks: vi.fn().mockResolvedValue("/uploads/photo.png"),
}));

describe("chat attachment file types", () => {
  it("reports only image files actually added after validation", async () => {
    const ref = createRef<ImageUploadImperativeProps>();
    const onFilesAdded = vi.fn();
    render(createElement(ImageUpload, {
      ref,
      max: 1,
      types: allowedImageTypes,
      icon: createElement("span"),
      updateFiles: vi.fn(),
      listNum: 0,
      onFilesAdded,
    }));

    await act(async () => ref.current?.uploadFiles([
      new File([new Uint8Array(5 * 1024 * 1024 + 1)], "too-large.png", { type: "image/png" }),
    ]));
    expect(onFilesAdded).not.toHaveBeenCalled();

    await act(async () => ref.current?.uploadFiles([
      new File(["image"], "photo.png", { type: "image/png" }),
    ]));
    expect(onFilesAdded).toHaveBeenCalledOnce();
    expect(onFilesAdded.mock.calls[0][0].map((file: File) => file.name)).toEqual(["photo.png"]);

    await act(async () => ref.current?.uploadFiles([
      new File(["image"], "ignored.png", { type: "image/png" }),
    ]));
    expect(onFilesAdded).toHaveBeenCalledOnce();
  });

  it("uploads SVG as a file instead of a raster vision input", () => {
    expect(allowedTextTypes).toContain(".svg");
    expect(allowedUploadTypes).toContain(".svg");
    expect(allowedImageTypes).not.toContain(".svg");
  });
  it("exposes CSV and Excel formats in the file picker whitelist", () => {
    expect(allowedSpreadsheetTypes).toEqual([".csv", ".tsv", ".xls", ".xlsx"]);
    expect(allowedFileTypes).toEqual(expect.arrayContaining(allowedSpreadsheetTypes));
    expect(allowedUploadTypes).toEqual(expect.arrayContaining(allowedSpreadsheetTypes));
  });

  it("keeps spreadsheet extensions in one explicit category", () => {
    expect(allowedTextTypes).not.toEqual(
      expect.arrayContaining(allowedSpreadsheetTypes),
    );
    expect(new Set(allowedUploadTypes).size).toBe(allowedUploadTypes.length);
  });
});
