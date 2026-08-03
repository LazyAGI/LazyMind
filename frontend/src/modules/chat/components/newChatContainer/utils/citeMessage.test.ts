import { describe, expect, it } from "vitest";
import { RoleTypes } from "@/modules/chat/constants/common";
import {
  buildCitedMessageText,
  findLastUserMessageIndex,
  getCiteMessages,
  splitCiteMessages,
} from "./citeMessage";

describe("buildCitedMessageText", () => {
  it("returns the trimmed text when there are no cite messages", () => {
    expect(buildCitedMessageText("  hello  ")).toBe("hello");
  });

  it("prefixes the text with cite_message tags when cite messages exist", () => {
    const result = buildCitedMessageText("hello", ["foo", "bar"]);
    expect(result).toBe(
      "<cite_message>foo</cite_message>\n<cite_message>bar</cite_message>\nhello",
    );
  });

  it("filters out blank cite messages before building the text", () => {
    const result = buildCitedMessageText("hello", ["  ", "foo", ""]);
    expect(result).toBe("<cite_message>foo</cite_message>\nhello");
  });
});

describe("splitCiteMessages", () => {
  it("returns an empty array for undefined input", () => {
    expect(splitCiteMessages(undefined)).toEqual([]);
  });

  it("splits on two or more consecutive newlines and trims each part", () => {
    expect(splitCiteMessages("first\n\nsecond\n\n\nthird")).toEqual([
      "first",
      "second",
      "third",
    ]);
  });

  it("filters out empty segments", () => {
    expect(splitCiteMessages("first\n\n\n\nsecond")).toEqual(["first", "second"]);
  });
});

describe("findLastUserMessageIndex", () => {
  it("returns -1 when there is no user message", () => {
    const list = [{ role: RoleTypes.ASSISTANT }, { role: RoleTypes.ASSISTANT }];
    expect(findLastUserMessageIndex(list)).toBe(-1);
  });

  it("returns the index of the last user message", () => {
    const list = [
      { role: RoleTypes.USER },
      { role: RoleTypes.ASSISTANT },
      { role: RoleTypes.USER },
      { role: RoleTypes.ASSISTANT },
    ];
    expect(findLastUserMessageIndex(list)).toBe(2);
  });
});

describe("getCiteMessages", () => {
  it("returns an empty array when message is undefined", () => {
    expect(getCiteMessages(undefined)).toEqual([]);
  });

  it("prefers cite_messages array when present", () => {
    const result = getCiteMessages({ cite_messages: ["a", " b ", ""] });
    expect(result).toEqual(["a", "b"]);
  });

  it("extracts cite_message tags from a text input when cite_messages is absent", () => {
    const message = {
      inputs: [
        {
          input_type: "text",
          text: "<cite_message>foo</cite_message><cite_message>bar</cite_message>",
        },
      ],
    };
    expect(getCiteMessages(message)).toEqual(["foo", "bar"]);
  });

  it("falls back to splitting cite_message when no tags are found in inputs", () => {
    const message = {
      cite_message: "first\n\nsecond",
      inputs: [{ input_type: "text", text: "no tags here" }],
    };
    expect(getCiteMessages(message)).toEqual(["first", "second"]);
  });
});
