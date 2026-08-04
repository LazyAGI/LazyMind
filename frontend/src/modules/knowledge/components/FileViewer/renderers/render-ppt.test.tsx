import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import RenderPpt from "./render-ppt";

const previewMock = vi.fn();
const initMock = vi.fn(() => ({ preview: previewMock }));

vi.mock("pptx-preview", () => ({
  init: (...args: unknown[]) => initMock(...args),
}));

describe("RenderPpt", () => {
  beforeEach(() => {
    initMock.mockClear();
    previewMock.mockReset();
  });

  it("initializes pptx-preview and appends the rendered slide container", async () => {
    previewMock.mockResolvedValue(undefined);

    const { container } = render(<RenderPpt fileData={new ArrayBuffer(8)} />);

    await waitFor(() => {
      expect(initMock).toHaveBeenCalledTimes(1);
      expect(previewMock).toHaveBeenCalledTimes(1);
    });
    expect(container.querySelector(".file-viewer-content")?.childElementCount).toBe(1);
  });

  it("renders a fallback error message when preview fails", async () => {
    previewMock.mockRejectedValue(new Error("bad pptx"));
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});

    const { container, findByText } = render(
      <RenderPpt fileData={new ArrayBuffer(8)} />,
    );

    // This component imports the real (non-test) i18n singleton, so its text
    // resolves to the actual zh-CN translation rather than a raw i18n key.
    await findByText("PowerPoint 文件预览失败");
    expect(container.textContent).toContain("bad pptx");
    consoleError.mockRestore();
  });
});
