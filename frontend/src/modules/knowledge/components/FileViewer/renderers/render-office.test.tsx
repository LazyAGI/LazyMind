import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import RenderOffice from "./render-office";

const previewMock = vi.fn();
const destroyMock = vi.fn();
const initMock = vi.fn(() => ({ preview: previewMock, destroy: destroyMock }));

vi.mock("@js-preview/docx", () => ({
  default: { init: (...args: unknown[]) => initMock(...args) },
}));
vi.mock("@js-preview/docx/lib/index.css", () => ({}));

describe("RenderOffice", () => {
  beforeEach(() => {
    initMock.mockClear();
    previewMock.mockReset();
    destroyMock.mockClear();
  });

  it("initializes the jsPreviewDocx reader for docx files", async () => {
    previewMock.mockResolvedValue(undefined);
    const fileData = new ArrayBuffer(4);

    render(
      <RenderOffice
        fileData={fileData}
        fileType="docx"
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

    const { unmount } = render(
      <RenderOffice
        fileData={new ArrayBuffer(4)}
        fileType="docx"
        content={null}
        metadata={null}
      />,
    );

    await waitFor(() => expect(initMock).toHaveBeenCalledTimes(1));

    unmount();

    expect(destroyMock).toHaveBeenCalledTimes(1);
  });

  it("does nothing when no reader type is resolvable (e.g. excel without global)", async () => {
    render(
      <RenderOffice
        fileData={new ArrayBuffer(4)}
        fileType="excel"
        content={null}
        metadata={null}
      />,
    );

    // "excel" resolves the reader from `window.jsPreviewExcel`, which is not
    // set up in this test environment, so init should never run.
    await waitFor(() => {
      expect(initMock).not.toHaveBeenCalled();
    });
  });
});
