import { describe, it, expect } from "vitest";
import { render, waitFor } from "@testing-library/react";
import RenderTxt from "./render-txt";

function toArrayBuffer(text: string): ArrayBuffer {
  return new TextEncoder().encode(text).buffer;
}

describe("RenderTxt", () => {
  it("decodes the file data and renders it as plain text", async () => {
    const { container } = render(
      <RenderTxt fileData={toArrayBuffer("hello world")} content={null} />,
    );

    await waitFor(() => {
      expect(container.querySelector("pre")?.textContent).toBe("hello world");
    });
  });

  it("wraps the matching content in a highlighted span", async () => {
    const { container } = render(
      <RenderTxt fileData={toArrayBuffer("prefix keyword suffix")} content="keyword" />,
    );

    await waitFor(() => {
      const mark = container.querySelector(".txt-keyword");
      expect(mark).toBeInTheDocument();
      expect(mark?.textContent).toBe("keyword");
    });
  });

  it("falls back to the raw text when content does not match", async () => {
    const { container } = render(
      <RenderTxt fileData={toArrayBuffer("no match here")} content="missing" />,
    );

    await waitFor(() => {
      expect(container.querySelector(".txt-keyword")).not.toBeInTheDocument();
      expect(container.querySelector("pre")?.textContent).toBe("no match here");
    });
  });
});
