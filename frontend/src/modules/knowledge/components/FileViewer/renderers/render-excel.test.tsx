import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import RenderExcel from "./render-excel";

const previewMock = vi.fn();
const destroyMock = vi.fn();
const initMock = vi.fn(() => ({ preview: previewMock, destroy: destroyMock }));

vi.mock("@js-preview/excel", () => ({
  default: { init: (...args: unknown[]) => initMock(...args) },
}));

vi.mock("xlsx", () => ({
  read: vi.fn(),
  utils: {
    decode_range: vi.fn(),
    encode_cell: vi.fn(),
  },
}));

function toArrayBuffer(bytes: number[]): ArrayBuffer {
  return new Uint8Array(bytes).buffer;
}

describe("RenderExcel", () => {
  beforeEach(() => {
    initMock.mockClear();
    previewMock.mockReset();
    destroyMock.mockClear();
  });

  it("initializes the js-preview excel reader for a standard xlsx buffer", async () => {
    previewMock.mockResolvedValue(undefined);
    // Non-OLE header so the component delegates straight to jsPreviewExcel.
    const fileData = toArrayBuffer([0x50, 0x4b, 0x03, 0x04]);

    render(
      <RenderExcel
        fileData={fileData}
        fileType="excel"
        content={null}
        metadata={null}
      />,
    );

    await waitFor(() => {
      expect(initMock).toHaveBeenCalledTimes(1);
      expect(previewMock).toHaveBeenCalledWith(fileData);
    });
  });

  it("destroys the previous reader instance on unmount", async () => {
    previewMock.mockResolvedValue(undefined);
    const fileData = toArrayBuffer([0x50, 0x4b, 0x03, 0x04]);

    const { unmount } = render(
      <RenderExcel
        fileData={fileData}
        fileType="excel"
        content={null}
        metadata={null}
      />,
    );

    await waitFor(() => expect(initMock).toHaveBeenCalledTimes(1));

    unmount();

    expect(destroyMock).toHaveBeenCalledTimes(1);
  });
});
