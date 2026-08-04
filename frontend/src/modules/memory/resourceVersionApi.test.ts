import { describe, expect, it } from "vitest";
import type { ResourceVersionType } from "./resourceVersionApi";

describe("ResourceVersionType", () => {
  it("accepts the three defined resource version type literals", () => {
    const types: ResourceVersionType[] = ["skill", "memory", "user_preference"];
    expect(types).toEqual(["skill", "memory", "user_preference"]);
  });
});
