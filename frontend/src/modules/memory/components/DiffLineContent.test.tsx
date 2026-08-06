import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { DiffLineContent } from "./DiffLineContent";
import type { DiffLine } from "../shared";

describe("DiffLineContent", () => {
  it("renders plain text when the line has no inlineSpans", () => {
    const line: DiffLine = { type: "same", text: "hello world" };
    const { container } = render(<DiffLineContent line={line} />);
    const code = container.querySelector("code");
    expect(code).toHaveTextContent("hello world");
    expect(container.querySelector("mark")).toBeNull();
  });

  it("highlights spans marked with highlight for an add line", () => {
    const line: DiffLine = {
      type: "add",
      text: "hello world",
      inlineSpans: [
        { text: "hello ", highlight: false },
        { text: "world", highlight: true },
      ],
    };
    const { container } = render(<DiffLineContent line={line} />);
    const mark = container.querySelector("mark");
    expect(mark).not.toBeNull();
    expect(mark).toHaveTextContent("world");
    expect(mark).toHaveClass("memory-diff-inline-add");
  });

  it("uses the remove highlight class for a remove line", () => {
    const line: DiffLine = {
      type: "remove",
      text: "old value",
      inlineSpans: [{ text: "old value", highlight: true }],
    };
    const { container } = render(<DiffLineContent line={line} />);
    const mark = container.querySelector("mark");
    expect(mark).toHaveClass("memory-diff-inline-remove");
  });

  it("renders non-highlighted spans as plain spans", () => {
    const line: DiffLine = {
      type: "add",
      text: "abc",
      inlineSpans: [{ text: "abc", highlight: false }],
    };
    const { container } = render(<DiffLineContent line={line} />);
    expect(container.querySelector("mark")).toBeNull();
    expect(container.querySelector("span")).toHaveTextContent("abc");
  });

  it("renders an empty code element for an empty inlineSpans array", () => {
    const line: DiffLine = { type: "same", text: "", inlineSpans: [] };
    const { container } = render(<DiffLineContent line={line} />);
    const code = container.querySelector("code");
    expect(code).not.toBeNull();
    expect(code?.textContent).toBe("");
  });
});
