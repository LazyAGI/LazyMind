import { describe, expect, it } from "vitest";
import { appendPromptToDraft, canManagePrompt, setPromptFavorite } from "./promptLibrary";
import type { PromptItem } from "@/api/generated/core-client";

describe("appendPromptToDraft", () => {
  it("returns the draft unchanged when the prompt is blank", () => {
    expect(appendPromptToDraft("hello", "   ")).toBe("hello");
  });

  it("returns the trimmed prompt when the draft is empty", () => {
    expect(appendPromptToDraft("", "  prompt text  ")).toBe("prompt text");
  });

  it("appends the prompt on a new line, trimming trailing whitespace from the draft", () => {
    expect(appendPromptToDraft("existing draft   ", "new prompt")).toBe(
      "existing draft\nnew prompt",
    );
  });
});

describe("canManagePrompt", () => {
  it("returns true for custom-sourced prompts", () => {
    expect(canManagePrompt({ source: "custom" } as PromptItem)).toBe(true);
  });

  it("returns false for preset prompts", () => {
    expect(canManagePrompt({ source: "preset" } as PromptItem)).toBe(false);
  });
});

describe("setPromptFavorite", () => {
  const prompts: PromptItem[] = [
    { id: "p1", is_favorite: false } as PromptItem,
    { id: "p2", is_favorite: true } as PromptItem,
  ];

  it("updates the is_favorite flag for the matching prompt only", () => {
    const result = setPromptFavorite(prompts, "p1", true);
    expect(result.find((p) => p.id === "p1")?.is_favorite).toBe(true);
    expect(result.find((p) => p.id === "p2")?.is_favorite).toBe(true);
    // Original array is not mutated.
    expect(prompts[0].is_favorite).toBe(false);
  });

  it("leaves the list unchanged when the id does not match any prompt", () => {
    const result = setPromptFavorite(prompts, "unknown", true);
    expect(result).toEqual(prompts);
  });
});
