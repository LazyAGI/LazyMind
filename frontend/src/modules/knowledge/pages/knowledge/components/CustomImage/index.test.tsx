import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, render, screen } from "@testing-library/react";
import CustomImage from "./index";

const resolveMarkdownImageUrlAsyncMock = vi.fn();

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveMarkdownImageUrlAsync: (...args: unknown[]) =>
    resolveMarkdownImageUrlAsyncMock(...args),
}));

describe("CustomImage", () => {
  beforeEach(() => {
    resolveMarkdownImageUrlAsyncMock.mockReset();
  });

  it("renders an img element with the given src and a non-empty default alt", () => {
    render(<CustomImage src="https://example.com/a.png" />);

    const img = screen.getByRole("img") as HTMLImageElement;
    expect(img.src).toBe("https://example.com/a.png");
    expect(img.alt).toBe(" ");
  });

  it("resets display style on successful load", () => {
    render(<CustomImage src="https://example.com/a.png" />);

    const img = screen.getByRole("img") as HTMLImageElement;
    img.style.display = "none";
    fireEvent.load(img);

    expect(img.style.display).toBe("");
  });

  it("retries with a refreshed url once on error, and swaps the src if different", async () => {
    resolveMarkdownImageUrlAsyncMock.mockResolvedValue(
      "https://example.com/refreshed.png",
    );
    render(<CustomImage src="https://example.com/broken.png" />);

    const img = screen.getByRole("img") as HTMLImageElement;
    fireEvent.error(img);

    await waitFor(() => {
      expect(img.src).toBe("https://example.com/refreshed.png");
    });
    expect(resolveMarkdownImageUrlAsyncMock).toHaveBeenCalledTimes(1);
  });

  it("hides the image on a second error when no error image is configured", async () => {
    resolveMarkdownImageUrlAsyncMock.mockResolvedValue("");
    render(<CustomImage src="https://example.com/broken.png" />);

    const img = screen.getByRole("img") as HTMLImageElement;
    fireEvent.error(img);
    await waitFor(() => {
      expect(resolveMarkdownImageUrlAsyncMock).toHaveBeenCalledTimes(1);
    });

    fireEvent.error(img);

    await waitFor(() => {
      expect(img.style.display).toBe("none");
    });
    // Retry only happens once; the second error should not call resolve again.
    expect(resolveMarkdownImageUrlAsyncMock).toHaveBeenCalledTimes(1);
  });

  it("shows the configured error image when showErrorImage is set", async () => {
    resolveMarkdownImageUrlAsyncMock.mockRejectedValue(new Error("fail"));
    render(
      <CustomImage
        src="https://example.com/broken.png"
        showErrorImage
        errorUrl="https://example.com/error.png"
      />,
    );

    const img = screen.getByRole("img") as HTMLImageElement;
    fireEvent.error(img);

    await waitFor(() => {
      expect(img.src).toBe("https://example.com/error.png");
    });
  });
});
