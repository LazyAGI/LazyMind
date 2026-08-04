import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { SkillDiffLineContent } from "./SkillDiffLineContent";
import type { SkillDiffEntryLine } from "./skillDiffUtils";

const makeLine = (overrides: Partial<SkillDiffEntryLine>): SkillDiffEntryLine => ({
  rawType: "CONTEXT",
  type: "same",
  text: "",
  ...overrides,
});

describe("SkillDiffLineContent", () => {
  it("renders raw html when the line has an html field", () => {
    const line = makeLine({ html: '<span class="foo">bar</span>' });
    const { container } = render(<SkillDiffLineContent line={line} />);
    const code = container.querySelector("code.memory-skill-diff-line-html");
    expect(code).not.toBeNull();
    expect(code?.innerHTML).toBe('<span class="foo">bar</span>');
  });

  it("falls back to plain DiffLineContent rendering when there is no html", () => {
    const line = makeLine({ type: "add", text: "hello" });
    const { container } = render(<SkillDiffLineContent line={line} />);
    expect(container.querySelector("code.memory-skill-diff-line-html")).toBeNull();
    expect(container.querySelector("code")).toHaveTextContent("hello");
  });

  it("maps the hunk type to 'same' text rendering via toDiffLine", () => {
    const line = makeLine({ type: "hunk" as never, text: "@@ -1,2 +1,2 @@" });
    const { container } = render(<SkillDiffLineContent line={line} />);
    expect(container.querySelector("code")).toHaveTextContent("@@ -1,2 +1,2 @@");
  });
});
