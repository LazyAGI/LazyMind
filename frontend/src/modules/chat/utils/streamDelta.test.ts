import { describe, expect, it } from "vitest";
import { mergeChatStreamDelta } from "./streamDelta";

describe("mergeChatStreamDelta", () => {
  it("appends live chunks byte-for-byte when Markdown delimiters repeat", () => {
    const previous = "|--------------------";
    const incoming = "|---------------------------|---------------------------------";

    expect(mergeChatStreamDelta(previous, incoming, "append")).toBe(
      previous + incoming,
    );
  });

  it.each([
    ["17:0", "0<br>", "17:00<br>"],
    ["6:0", "0-18", "6:00-18"],
    ["a", "abc", "aabc"],
  ])(
    "does not deduplicate valid live boundaries",
    (previous, incoming, expected) => {
      expect(mergeChatStreamDelta(previous, incoming, "append")).toBe(
        expected,
      );
    },
  );

  it("treats an omitted mode as append for live compatibility", () => {
    expect(mergeChatStreamDelta("17:0", "0<br>")).toBe("17:00<br>");
  });

  it("replaces an existing partial response with a resume snapshot", () => {
    expect(
      mergeChatStreamDelta("partial response", "complete response", "replace"),
    ).toBe("complete response");
  });

  it("honors an empty replace frame as a replay reset", () => {
    expect(mergeChatStreamDelta("stale response", "", "replace")).toBe("");
  });
});
