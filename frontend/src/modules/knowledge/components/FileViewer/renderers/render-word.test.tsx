import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import RenderWord from "./render-word";

const renderAsyncMock = vi.fn();

vi.mock("docx-preview", () => ({
  renderAsync: (...args: unknown[]) => renderAsyncMock(...args),
}));

describe("RenderWord", () => {
  beforeEach(() => {
    renderAsyncMock.mockReset();
  });

  it("shows a spinner while docx-preview is rendering", async () => {
    let resolveRender: () => void = () => {};
    renderAsyncMock.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveRender = resolve;
        }),
    );

    const { container } = render(
      <RenderWord fileData={new ArrayBuffer(4)} content={null} />,
    );

    expect(container.querySelector(".ant-spin")).toBeInTheDocument();

    resolveRender();
    await waitFor(() => {
      expect(container.querySelector(".ant-spin")).not.toBeInTheDocument();
    });
  });

  it("invokes docx-preview's renderAsync with the file data and container", async () => {
    renderAsyncMock.mockResolvedValue(undefined);

    render(<RenderWord fileData={new ArrayBuffer(4)} content={null} />);

    await waitFor(() => {
      expect(renderAsyncMock).toHaveBeenCalledTimes(1);
    });
    const [fileDataArg, containerArg] = renderAsyncMock.mock.calls[0];
    expect(fileDataArg).toBeInstanceOf(ArrayBuffer);
    expect(containerArg).toBeInstanceOf(HTMLElement);
  });

  it("logs and stops loading when renderAsync throws", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    renderAsyncMock.mockRejectedValue(new Error("boom"));

    const { container } = render(
      <RenderWord fileData={new ArrayBuffer(4)} content={null} />,
    );

    await waitFor(() => {
      expect(container.querySelector(".ant-spin")).not.toBeInTheDocument();
    });
    expect(consoleError).toHaveBeenCalled();
    consoleError.mockRestore();
  });
});
