import { describe, it, expect } from "vitest";
import { render, waitFor } from "@testing-library/react";
import RenderHtml from "./render-html";

function toArrayBuffer(text: string): ArrayBuffer {
  return new TextEncoder().encode(text).buffer;
}

describe("RenderHtml", () => {
  it("decodes the file data and highlights it as markup", async () => {
    const { container } = render(
      <RenderHtml fileData={toArrayBuffer("<div>hi</div>")} content={null} />,
    );

    await waitFor(() => {
      expect(container.querySelector("code")?.textContent).toBe("<div>hi</div>");
    });
  });

  it("wraps matching keyword occurrences in a <mark> element", async () => {
    const { container } = render(
      <RenderHtml fileData={toArrayBuffer("<p>needle in haystack</p>")} content="needle" />,
    );

    await waitFor(() => {
      const mark = container.querySelector("mark.keyword");
      expect(mark).toBeInTheDocument();
      expect(mark?.textContent).toBe("needle");
    });
  });

  it("renders nothing highlighted when content is empty", async () => {
    const { container } = render(
      <RenderHtml fileData={toArrayBuffer("<span>plain</span>")} content="" />,
    );

    await waitFor(() => {
      expect(container.querySelector("code")?.textContent).toBe("<span>plain</span>");
    });
    expect(container.querySelector("mark.keyword")).not.toBeInTheDocument();
  });
});
