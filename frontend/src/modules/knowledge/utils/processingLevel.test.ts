import { describe, expect, it } from "vitest";
import {
  effectiveProcessingLevel,
  highestSupportedProcessingLevel,
  isProcessingLevelDowngrade,
  processingLevelSupportsSegments,
} from "./processingLevel";

describe("knowledge processing levels", () => {
  it("keeps legacy datasets compatible by defaulting to indexed", () => {
    expect(effectiveProcessingLevel()).toBe("indexed");
  });

  it("distinguishes upgrades from downgrades", () => {
    expect(isProcessingLevelDowngrade("indexed", "chunked")).toBe(true);
    expect(isProcessingLevelDowngrade("parsed", "indexed")).toBe(false);
  });

  it.each([
    ["stored", false],
    ["parsed", false],
    ["chunked", true],
    ["indexed", true],
  ] as const)("reports segment support for %s", (level, expected) => {
    expect(processingLevelSupportsSegments(level)).toBe(expected);
  });

  it("selects the highest level supported by the current configuration", () => {
    expect(highestSupportedProcessingLevel(false, true)).toBe("stored");
    expect(highestSupportedProcessingLevel(true, false)).toBe("chunked");
    expect(highestSupportedProcessingLevel(true, true)).toBe("indexed");
  });
});
