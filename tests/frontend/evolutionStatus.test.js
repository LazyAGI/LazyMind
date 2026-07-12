import { describe, expect, it } from "vitest";

import {
  formatPersonalizationEvolutionTime,
  projectPersonalizationEvolutionState,
} from "../../frontend/src/modules/memory/evolutionStatus.ts";

describe("personalization evolution status", () => {
  it.each([
    [{ autoEvo: false }, "off"],
    [{ autoEvo: true }, "waiting"],
    [{ autoEvo: true, reviewStatus: "pending" }, "pending_review"],
    [{ autoEvo: true, autoEvoApplyStatus: "running" }, "applying"],
    [{ autoEvo: true, autoEvoFinishedAt: "2026-07-12T01:00:00Z" }, "applied"],
    [{ autoEvo: true, autoEvoApplyStatus: "failed", autoEvoError: "apply failed" }, "failed"],
  ])("projects %o as %s", (source, expected) => {
    expect(projectPersonalizationEvolutionState(source).state).toBe(expected);
  });

  it("gives failure and applying precedence over pending review", () => {
    expect(projectPersonalizationEvolutionState({
      autoEvo: true,
      reviewStatus: "pending",
      autoEvoApplyStatus: "failed",
      autoEvoError: "conflict",
      autoEvoFinishedAt: "2026-07-12T02:00:00Z",
    })).toEqual({
      state: "failed",
      error: "conflict",
      latestAt: "2026-07-12T02:00:00Z",
    });
  });

  it("does not render invalid evolution timestamps", () => {
    expect(formatPersonalizationEvolutionTime("not-a-date")).toBe("");
    expect(formatPersonalizationEvolutionTime()).toBe("");
  });
});
