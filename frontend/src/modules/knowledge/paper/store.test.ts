import { describe, expect, it } from "vitest";
import { isPaperLibrary } from "./store";

describe("persisted scene resolution", () => {
 it("recognizes only the server paper scene", () => {
  expect(isPaperLibrary("academic_papers")).toBe(true);
  expect(isPaperLibrary("general")).toBe(false);
  expect(isPaperLibrary("general")).toBe(false);
  expect(isPaperLibrary()).toBe(false);
 });
});
