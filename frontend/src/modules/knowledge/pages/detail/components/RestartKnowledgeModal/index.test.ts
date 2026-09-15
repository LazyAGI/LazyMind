import { describe, expect, it } from "vitest";
import {
  supportsSegmentReparse,
  supportsVectorReparse,
} from "./index";

describe("reparse capability by processing level", () => {
  it.each([
    ["stored", false, false],
    ["parsed", false, false],
    ["chunked", true, false],
    ["indexed", true, true],
  ])("limits %s to its materialization boundary", (level, segments, vectors) => {
    expect(supportsSegmentReparse(level)).toBe(segments);
    expect(supportsVectorReparse(level)).toBe(vectors);
  });

  it("keeps legacy datasets fully capable when the level is absent", () => {
    expect(supportsSegmentReparse()).toBe(true);
    expect(supportsVectorReparse()).toBe(true);
  });
});
