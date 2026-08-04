import { describe, expect, it } from "vitest";
import { defaultSchema } from "rehype-sanitize";
import { customSchema } from "./config";

describe("customSchema", () => {
  it("extends the default sanitize schema's global attributes with style/class/id", () => {
    expect(customSchema.attributes?.["*"]).toEqual(
      expect.arrayContaining(["class", "className", "style", "id"]),
    );
    // Should still keep whatever rehype-sanitize allows by default.
    (defaultSchema.attributes?.["*"] || []).forEach((attr) => {
      expect(customSchema.attributes?.["*"]).toContain(attr);
    });
  });

  it("allows KaTeX math tag names not present in the default schema", () => {
    expect(customSchema.tagNames).toEqual(
      expect.arrayContaining(["math", "semantics", "mrow", "annotation"]),
    );
  });

  it("allows svg-related tags and attributes needed for mermaid/katex output", () => {
    expect(customSchema.tagNames).toEqual(
      expect.arrayContaining(["svg", "path", "g", "use"]),
    );
    expect(customSchema.attributes?.svg).toEqual(
      expect.arrayContaining(["viewBox", "preserveAspectRatio"]),
    );
    expect(customSchema.attributes?.path).toEqual(
      expect.arrayContaining(["d", "fill", "stroke"]),
    );
  });
});
