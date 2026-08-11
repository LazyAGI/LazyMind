import { describe, expect, it } from "vitest";

import { DefaultApiFactory } from "../../frontend/src/api/generated/core-client/index.ts";

const MEMORY_METHODS = [
  "apiCoreMemoryEpisodesEpisodeIdDelete",
  "apiCoreMemoryEpisodesEpisodeIdGet",
  "apiCoreMemoryEpisodesGet",
  "apiCoreMemoryPreferencesGet",
  "apiCoreMemoryPreferencesNameDelete",
  "apiCoreMemoryPreferencesNameGet",
  "apiCoreMemoryPreferencesOrderPut",
  "apiCoreMemoryProfileAvatarDelete",
  "apiCoreMemoryProfileAvatarGet",
  "apiCoreMemoryProfileAvatarPut",
  "apiCoreMemoryProfileGet",
  "apiCoreMemoryProfilePatch",
  "apiCoreMemorySoulAvatarDelete",
  "apiCoreMemorySoulAvatarGet",
  "apiCoreMemorySoulAvatarPut",
  "apiCoreMemorySoulGet",
  "apiCoreMemorySoulPatch",
];

describe("generated Core Memory SDK contract", () => {
  it("exposes all Memory operations on the real DefaultApiFactory", () => {
    const client = DefaultApiFactory();

    for (const method of MEMORY_METHODS) {
      expect(client[method], method).toBeTypeOf("function");
    }
  });
});
