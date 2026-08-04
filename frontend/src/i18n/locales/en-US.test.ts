import { describe, expect, it } from "vitest";
import enUS from "./en-US";
import { enUSErrorMessages } from "./error-codes";

describe("en-US translation dictionary", () => {
  it("wires the generated error messages under the errors namespace", () => {
    expect(enUS.errors).toBe(enUSErrorMessages);
  });

  it("exposes a default export matching the named structure", () => {
    expect(enUS.common.save).toBe("Save");
    expect(enUS.common.cancel).toBe("Cancel");
  });

  it("does not contain duplicate top-level namespace declarations", () => {
    const topLevelKeys = Object.keys(enUS);
    const unique = new Set(topLevelKeys);
    expect(unique.size).toBe(topLevelKeys.length);
  });
});
