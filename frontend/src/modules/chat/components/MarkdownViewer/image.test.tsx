import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const imageUrlMocks = vi.hoisted(() => ({
  resolveMarkdownImageUrlAsync: vi.fn(),
}));

vi.mock("@/modules/knowledge/utils/imageUrl", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/knowledge/utils/imageUrl")>()),
  resolveMarkdownImageUrlAsync: imageUrlMocks.resolveMarkdownImageUrlAsync,
}));

import MarkdownViewer from "./index";

describe("MarkdownViewer managed images", () => {
  afterEach(() => {
    imageUrlMocks.resolveMarkdownImageUrlAsync.mockReset();
  });

  it("shows the signed image after the initial unsigned URL fails", async () => {
    let finishSigning: (url: string) => void = () => {};
    imageUrlMocks.resolveMarkdownImageUrlAsync.mockReturnValue(
      new Promise<string>((resolve) => {
        finishSigning = resolve;
      }),
    );

    render(
      <MarkdownViewer>
        {"![Generated image](/static-files/external-agent-attachments/image.png)"}
      </MarkdownViewer>,
    );

    const unsignedImage = screen.getByAltText("Generated image");
    fireEvent.error(unsignedImage);
    expect(
      screen.queryByRole("button", { name: "Generated image" }),
    ).not.toBeInTheDocument();

    await act(async () => {
      finishSigning("/api/core/static-files/external-agent-attachments/image.png?token=signed");
    });

    expect(screen.getByAltText("Generated image")).toHaveAttribute(
      "src",
      "/api/core/static-files/external-agent-attachments/image.png?token=signed",
    );
  });
});
