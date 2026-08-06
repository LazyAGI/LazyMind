import { describe, expect, it } from "vitest";
import {
  formatThinkingForDisplay,
  hasThinkingPreviewTags,
  splitThinkingContent,
  stripThinkTags,
} from "./thinking";

describe("hasThinkingPreviewTags", () => {
  it("returns false for undefined or plain text", () => {
    expect(hasThinkingPreviewTags(undefined)).toBe(false);
    expect(hasThinkingPreviewTags("just plain text")).toBe(false);
  });

  it("detects <tp> and <trp> tags", () => {
    expect(hasThinkingPreviewTags('<tp id="1">step</tp>')).toBe(true);
    expect(hasThinkingPreviewTags('<trp id="1">result</trp>')).toBe(true);
  });
});

describe("splitThinkingContent", () => {
  it("treats the whole text as content when there is no thinking boundary", () => {
    const result = splitThinkingContent("hello world", "fallback reasoning");
    expect(result).toEqual({ reasoning_content: "fallback reasoning", content: "hello world" });
  });

  it("splits reasoning_content from content at the last </trp> boundary", () => {
    const raw = '<trp id="1">thinking part</trp>final answer';
    const result = splitThinkingContent(raw);
    expect(result.reasoning_content).toBe('<trp id="1">thinking part</trp>');
    expect(result.content).toBe("final answer");
  });

  it("falls back to the last </tp> boundary when there is no </trp>", () => {
    const raw = '<tp id="1">thinking</tp>the answer';
    const result = splitThinkingContent(raw);
    expect(result.reasoning_content).toBe('<tp id="1">thinking</tp>');
    expect(result.content).toBe("the answer");
  });

  it("strips tool_call/tool_result payloads before computing the boundary", () => {
    const raw = '<trp id="1">thinking</trp><tool_call>{"x":1}</tool_call>answer';
    const result = splitThinkingContent(raw);
    expect(result.content).toBe("answer");
    expect(result.reasoning_content).toContain("thinking");
  });

  it("strips unfinished tool payloads mid-stream", () => {
    const raw = '<trp id="1">thinking</trp><tool_call>{"partial":';
    const result = splitThinkingContent(raw);
    expect(result.content).toBe("");
  });
});

describe("formatThinkingForDisplay", () => {
  it("returns an empty string for falsy input", () => {
    expect(formatThinkingForDisplay(undefined)).toBe("");
    expect(formatThinkingForDisplay("")).toBe("");
  });

  it("extracts inner content of closed tp/trp pairs", () => {
    const raw = '<tp id="1">step one</tp>';
    expect(formatThinkingForDisplay(raw)).toBe("step one");
  });

  it("inserts a paragraph break between adjacent thinking blocks", () => {
    const raw = '<tp id="1">step1</tp><trp id="1">result1</trp>';
    const result = formatThinkingForDisplay(raw);
    expect(result).toBe("step1\n\nresult1");
  });

  it("converts leftover orphan tags (streaming) into paragraph breaks", () => {
    const raw = '<trp id="1">unfinished';
    const result = formatThinkingForDisplay(raw);
    expect(result).toBe("unfinished");
  });

  it("strips tool payloads and collapses excess blank lines", () => {
    const raw = 'a<tool_call>{"x":1}</tool_call>\n\n\n\nb';
    const result = formatThinkingForDisplay(raw);
    expect(result).toBe("a\n\nb");
  });
});

describe("stripThinkTags", () => {
  it("returns an empty string for falsy input", () => {
    expect(stripThinkTags(undefined)).toBe("");
  });

  it("removes <think> blocks entirely", () => {
    const raw = "before<think>secret reasoning</think>after";
    expect(stripThinkTags(raw)).toBe("beforeafter");
  });

  it("trims surrounding whitespace after stripping", () => {
    const raw = "  <think>x</think>  visible text  ";
    expect(stripThinkTags(raw)).toBe("visible text");
  });
});
